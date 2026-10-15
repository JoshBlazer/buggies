package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/graph"
	"github.com/joshblazer/kernel/sandbox"
	"github.com/joshblazer/kernel/sandbox/adapter"
	"github.com/joshblazer/kernel/triad"

	"github.com/joshblazer/buggies/internal/agents"
	"github.com/joshblazer/buggies/internal/policy"
)

// KernelVersion is stamped on every finding for audit. Kept in sync with
// go.mod by hand until the kernel is published.
const KernelVersion = "v0.1.0+dev"

// ErrUnsupportedProject reports that the repository is not a project of the
// configured adapter's ecosystem, so the sandbox reproduction cannot run. This
// is not a review failure to retry: callers should skip the PR (e.g. post a
// neutral note) rather than treat it as an error.
type ErrUnsupportedProject struct {
	Root      string
	Ecosystem sandbox.Ecosystem
}

func (e *ErrUnsupportedProject) Error() string {
	return fmt.Sprintf("repository at %s is not a %s project", e.Root, e.Ecosystem)
}

// ErrDiffTooLarge reports that a PR changed more files than the reviewer takes
// on in one pass. A sweeping change (a dependency bump, a generated-file churn,
// a mass rename) is slow and low-signal to review and would burn the budget, so
// callers skip it with a neutral note rather than treat it as a failure.
type ErrDiffTooLarge struct{ Files, Limit int }

func (e *ErrDiffTooLarge) Error() string {
	return fmt.Sprintf("diff too large: %d files changed (limit %d)", e.Files, e.Limit)
}

// DefaultMaxChangedFiles is the out-of-the-box cap on a PR's changed-file count.
const DefaultMaxChangedFiles = 60

// Pipeline wires the kernel components with Buggies' policy.
type Pipeline struct {
	Repo     string // "owner/name", used in finding IDs
	Root     string // absolute path to a checkout at the head revision
	Graph    graph.Graph
	Executor sandbox.Executor
	Adapter  adapter.Adapter // the ecosystem adapter for Root
	Agents   *agents.Agents
	Store    finding.Store
	// Throttled reports whether a type is currently suppressed for Repo.
	Throttled func(ctx context.Context, typ finding.Type) (bool, error)
	Limits    triad.Limits
	Log       io.Writer

	// RadiusDepth is how many reverse hops the blast radius follows.
	RadiusDepth int
	// MaxContextBytes caps the rendered blast-radius source shown to agents.
	MaxContextBytes int
	// MaxChangedFiles caps how many changed files a PR may have before it is
	// skipped as too large (ErrDiffTooLarge). Zero means DefaultMaxChangedFiles.
	MaxChangedFiles int
}

// Result is what a review produced.
type Result struct {
	Base, Head string
	Findings   []finding.Finding // reproduced, stored, not throttled
	Throttled  []finding.Finding // reproduced but suppressed by self-throttle
	Traces     []triad.Trace     // every candidate's path, for audit
	Radius     graph.BlastRadius
	Elapsed    time.Duration
}

// Review runs the left door for base..head.
func (p *Pipeline) Review(ctx context.Context, base, head string) (Result, error) {
	start := time.Now()
	res := Result{Base: base, Head: head}
	if p.RadiusDepth <= 0 {
		p.RadiusDepth = 2
	}
	if p.MaxContextBytes <= 0 {
		p.MaxContextBytes = 60_000
	}

	// 1. The diff, twice: unified=0 to locate hunks precisely, unified=3 for
	// the model to read.
	hunks, changedFiles, err := p.hunks(ctx, base, head)
	if err != nil {
		return res, err
	}
	if len(hunks) == 0 {
		p.logf("no reviewable changes between %s and %s", base, head)
		res.Elapsed = time.Since(start)
		return res, nil
	}
	// Guard against sweeping PRs before paying for indexing and the triad: a
	// change touching dozens of files is slow and low-signal to reproduce.
	maxFiles := p.MaxChangedFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxChangedFiles
	}
	if len(changedFiles) > maxFiles {
		return res, &ErrDiffTooLarge{Files: len(changedFiles), Limit: maxFiles}
	}
	diff, err := p.git(ctx, "diff", "-U3", "--no-color", base, head, "--")
	if err != nil {
		return res, err
	}

	// 2. SCOPE: index (incremental) and expand the diff to its blast radius.
	if err := p.Graph.Index(ctx, p.Root, nil); err != nil {
		return res, fmt.Errorf("index: %w", err)
	}
	radius, err := p.Graph.BlastRadius(ctx, hunks, p.RadiusDepth)
	if err != nil {
		return res, fmt.Errorf("blast radius: %w", err)
	}
	res.Radius = radius
	p.logf("scope: %d changed symbol(s), %d affected, %d file(s)", len(radius.Changed), len(radius.Affected), len(radius.Files))

	// 3. TRIAD with sandbox VERIFY inside the loop.
	det, err := p.Adapter.Detect(ctx, sandbox.Snapshot{Root: p.Root, Revision: head})
	if err != nil {
		return res, err
	}
	if !det.Matched {
		return res, &ErrUnsupportedProject{Root: p.Root, Ecosystem: p.Adapter.Ecosystem()}
	}
	scope := triad.Scope{
		Repo:      p.Repo,
		Snapshot:  sandbox.Snapshot{Root: p.Root, Revision: head},
		Ecosystem: p.Adapter.Ecosystem(),
		Diff:      diff,
		Radius:    p.renderRadius(radius, changedFiles),
		Scaffold:  projectFacts(det),
	}
	m := &triad.Machine{
		Proposer: p.Agents.Proposer(),
		Skeptic:  p.Agents.Skeptic(),
		Prover:   p.Agents.Prover(),
		Executor: p.Executor,
		Limits:   p.Limits,
	}
	rep, err := m.Run(ctx, scope)
	res.Traces = rep.Traces
	if err != nil {
		return res, fmt.Errorf("triad: %w", err)
	}
	for _, tr := range rep.Traces {
		p.logf("candidate %q → %s%s", tr.Candidate.Claim, tr.Stage, verdictNote(tr))
	}

	// 4. STORE: only confirmed findings become findings. Unconfirmed
	// candidates are discarded here and never surface.
	for _, c := range rep.Confirmed {
		f := finding.Finding{
			ID:            finding.Fingerprint(c.Candidate.Location, c.Candidate.Type, c.Candidate.Excerpt),
			Type:          c.Candidate.Type,
			Confidence:    finding.Reproduced,
			Impact:        c.Candidate.Impact,
			State:         finding.Verified,
			Claim:         c.Candidate.Claim,
			Location:      c.Candidate.Location,
			Proof:         &c.Proof,
			KernelVersion: KernelVersion,
		}
		f.Proof = &finding.Proof{Test: c.Proof.Test, Result: c.Proof.Result, SuggestedFix: c.Proof.SuggestedFix}
		if p.Throttled != nil {
			if q, err := p.Throttled(ctx, f.Type); err != nil {
				return res, err
			} else if q {
				p.logf("self-throttle: %s findings are suppressed for %s (dismissal rate over threshold)", f.Type, p.Repo)
				res.Throttled = append(res.Throttled, f)
				continue
			}
		}
		if err := p.Store.Put(ctx, f); err != nil {
			return res, fmt.Errorf("store: %w", err)
		}
		stored, err := p.Store.Get(ctx, f.ID)
		if err != nil {
			return res, err
		}
		if stored.State == finding.Dismissed || stored.State == finding.Fixed || stored.State == finding.Closed {
			// Known and already decided: dedup means we do not re-report it.
			p.logf("finding %s already %s; not re-reporting", f.ID, stored.State)
			continue
		}
		res.Findings = append(res.Findings, stored)
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.Impact > b.Impact
	})
	res.Elapsed = time.Since(start)
	return res, nil
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// hunks parses `git diff -U0` into head-side line ranges.
func (p *Pipeline) hunks(ctx context.Context, base, head string) ([]graph.Hunk, []string, error) {
	out, err := p.git(ctx, "diff", "-U0", "--no-color", "--no-renames", base, head, "--")
	if err != nil {
		return nil, nil, err
	}
	var hunks []graph.Hunk
	var files []string
	cur := ""
	deleted := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			name := strings.TrimPrefix(line, "+++ ")
			if name == "/dev/null" {
				deleted = true
				continue
			}
			deleted = false
			cur = strings.TrimPrefix(name, "b/")
			files = append(files, cur)
		case strings.HasPrefix(line, "--- ") && deleted:
			// handled at +++ /dev/null; the --- line names the removed file
		case strings.HasPrefix(line, "--- a/"):
			if deleted {
				hunks = append(hunks, graph.Hunk{Path: strings.TrimPrefix(line, "--- a/"), Deleted: true})
			}
		case strings.HasPrefix(line, "@@") && cur != "" && !deleted:
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			startLine, _ := strconv.Atoi(m[1])
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			if n == 0 { // pure deletion: point at the line after
				n = 1
			}
			hunks = append(hunks, graph.Hunk{Path: cur, StartLine: startLine, EndLine: startLine + n - 1})
		}
	}
	return hunks, files, nil
}

func (p *Pipeline) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", p.Root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// renderRadius shows the agents the current contents of every file the
// change touches or can affect, changed files first, within a byte budget.
func (p *Pipeline) renderRadius(br graph.BlastRadius, changed []string) string {
	var sb strings.Builder
	if len(br.Affected) > 0 {
		sb.WriteString("Affected symbols (reverse dependencies of the change):\n")
		for _, a := range br.Affected {
			fmt.Fprintf(&sb, "- %s in %s (depth %d via %s)\n", a.Symbol.Name, a.Symbol.Path, a.Depth, a.Via)
		}
	}
	seen := map[string]bool{}
	order := append([]string{}, changed...)
	order = append(order, br.Files...)
	budget := p.MaxContextBytes
	for _, f := range order {
		if seen[f] {
			continue
		}
		seen[f] = true
		b, err := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		if len(b) > budget {
			fmt.Fprintf(&sb, "\n### %s\n(omitted: %d bytes exceeds remaining context budget)\n", f, len(b))
			continue
		}
		budget -= len(b)
		fmt.Fprintf(&sb, "\n### %s\n```\n%s\n```\n", f, numbered(string(b)))
	}
	return sb.String()
}

func numbered(src string) string {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	var sb strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&sb, "%4d| %s\n", i+1, l)
	}
	return sb.String()
}

func projectFacts(det adapter.Detection) string {
	runner := det.TestRunner
	note := ""
	if det.Lockfile == "" {
		note = " (no lockfile: dependencies are NOT installed in the sandbox; use node:test)"
	}
	return fmt.Sprintf("Package manager: %s\nLockfile: %s\nTest runner available in the sandbox: %s%s\nSandbox: no network, read-only checkout, only the generated test file runs.",
		det.PackageManager, orNone(det.Lockfile), runner, note)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func verdictNote(tr triad.Trace) string {
	switch tr.Stage {
	case triad.StageAttacked:
		return " (" + tr.Verdict.Reason + ")"
	case triad.StageUnproved:
		if n := len(tr.Attempts); n > 0 {
			last := tr.Attempts[n-1]
			if last.Err != nil {
				return fmt.Sprintf(" (%d attempt(s); last error: %v)", n, last.Err)
			}
			return fmt.Sprintf(" (%d attempt(s); last outcome %s, reproducible=%v)", n, last.Result.Outcome, last.Result.Reproducible)
		}
	}
	return ""
}

func (p *Pipeline) logf(format string, a ...any) {
	if p.Log != nil {
		fmt.Fprintf(p.Log, format+"\n", a...)
	}
}

// DefaultLimits are Buggies' triad and sandbox bounds.
func DefaultLimits() triad.Limits {
	return triad.Limits{
		MaxCandidates: policy.MaxCandidates,
		ProveRetries:  policy.ProveRetries,
		Sandbox: sandbox.Limits{
			WallClock: 90 * time.Second,
			CPUs:      1,
			MemoryMB:  1024,
			DiskMB:    256,
			Pids:      256,
		},
	}
}

// Scope indexes and returns the blast radius for base..head without
// running the triad. Used by `buggies radius` for inspection.
func (p *Pipeline) Scope(ctx context.Context, base, head string) (graph.BlastRadius, error) {
	if p.RadiusDepth <= 0 {
		p.RadiusDepth = 2
	}
	hunks, _, err := p.hunks(ctx, base, head)
	if err != nil {
		return graph.BlastRadius{}, err
	}
	if err := p.Graph.Index(ctx, p.Root, nil); err != nil {
		return graph.BlastRadius{}, err
	}
	return p.Graph.BlastRadius(ctx, hunks, p.RadiusDepth)
}
