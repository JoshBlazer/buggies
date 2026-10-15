package eval

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/joshblazer/kernel/finding"
	gsqlite "github.com/joshblazer/kernel/graph/sqlite"
	"github.com/joshblazer/kernel/sandbox"
	"github.com/joshblazer/kernel/sandbox/adapter"
	"github.com/joshblazer/kernel/sandbox/adapter/node"
	"github.com/joshblazer/kernel/sandbox/runtime"

	"github.com/joshblazer/buggies/internal/agents"
	"github.com/joshblazer/buggies/internal/agents/skills"
	"github.com/joshblazer/buggies/internal/claudecli"
	"github.com/joshblazer/buggies/internal/pipeline"
	"github.com/joshblazer/buggies/internal/policy"
	"github.com/joshblazer/buggies/internal/store"
)

// Options configure a harness run.
type Options struct {
	// DataDir is the scratch root for per-fixture graph/findings/sandbox data.
	// Each fixture gets a fresh subdirectory so runs do not share dedup or
	// throttle state (the harness measures a cold review, not a warmed one).
	DataDir string
	// AllowRunc runs the sandbox on runc instead of gVisor (development only).
	// It mirrors BUGGIES_ALLOW_RUNC=1 in the review command.
	AllowRunc bool
	// SkillsOff disables the agent skill pack, mirroring BUGGIES_SKILLS=off.
	SkillsOff bool
	// Log receives the pipeline's progress lines; nil discards them.
	Log io.Writer
}

// FixtureResult is one fixture's outcome.
type FixtureResult struct {
	Fixture Fixture           `json:"-"`
	Name    string            `json:"name"`
	Repo    string            `json:"repo"`
	Score   Score             `json:"score"`
	Found   []finding.Finding `json:"-"`
	Elapsed time.Duration     `json:"elapsedMs"`
	// ModelCalls and CostUSD are the on-device model usage attributable to
	// this fixture (a delta of the process-wide counter).
	ModelCalls int64   `json:"modelCalls"`
	CostUSD    float64 `json:"costUsd"`
	// Skipped/Err record a fixture the harness could not score (e.g. the
	// project was not a supported ecosystem, or git/sandbox setup failed).
	Skipped bool   `json:"skipped,omitempty"`
	Err     string `json:"error,omitempty"`
}

// Report is the full machine-readable eval result.
type Report struct {
	Fixtures  []FixtureResult `json:"fixtures"`
	Aggregate Aggregate       `json:"aggregate"`
	// Pass is true iff every scored fixture passed (no misses, no noise).
	Pass bool `json:"pass"`
	// Runtime is the sandbox runtime used ("runsc" for gVisor, "runc").
	Runtime string `json:"runtime"`
}

// CheckRuntime verifies a container runtime is available before any fixture
// runs, so an environment problem fails with one clear message rather than
// once per fixture. It returns the runtime name it will use.
func CheckRuntime(ctx context.Context, opts Options) (string, error) {
	ex := newExecutor(filepath.Join(opts.DataDir, "_probe"), opts)
	if err := ex.CheckRuntime(ctx); err != nil {
		return "", err
	}
	if ex.AllowRunc {
		return "runc", nil
	}
	return "runsc", nil
}

// Run grades every fixture and aggregates the result. A fixture whose project
// is an unsupported ecosystem, or whose setup fails, is recorded as skipped or
// errored and left out of the aggregate (it is an environment problem, not a
// precision signal).
func Run(ctx context.Context, fxs []Fixture, opts Options) (Report, error) {
	rep := Report{}
	rt, err := CheckRuntime(ctx, opts)
	if err != nil {
		return rep, err
	}
	rep.Runtime = rt

	var scored []Fixture
	var scores []Score
	for _, fx := range fxs {
		fr := runOne(ctx, fx, opts)
		rep.Fixtures = append(rep.Fixtures, fr)
		if fr.Skipped || fr.Err != "" {
			continue
		}
		scored = append(scored, fx)
		scores = append(scores, fr.Score)
	}
	rep.Aggregate = aggregate(scored, scores)
	rep.Pass = true
	for _, fr := range rep.Fixtures {
		if fr.Err != "" || (!fr.Skipped && !fr.Score.Pass()) {
			rep.Pass = false
		}
	}
	return rep, nil
}

func runOne(ctx context.Context, fx Fixture, opts Options) FixtureResult {
	fr := FixtureResult{Fixture: fx, Name: fx.Name, Repo: fx.Repo}
	if fx.Ecosystem != "node" {
		fr.Skipped = true
		fr.Err = fmt.Sprintf("unsupported ecosystem %q (v0 is node only)", fx.Ecosystem)
		return fr
	}

	root, base, head, err := materialize(fx)
	if err != nil {
		fr.Err = err.Error()
		return fr
	}
	defer os.RemoveAll(root)

	dd := filepath.Join(opts.DataDir, sanitize(fx.Name))
	if err := os.MkdirAll(dd, 0o700); err != nil {
		fr.Err = err.Error()
		return fr
	}

	found, elapsed, calls, cost, err := review(ctx, fx, root, base, head, dd, opts)
	if err != nil {
		if _, ok := err.(*pipeline.ErrUnsupportedProject); ok {
			fr.Skipped = true
		}
		fr.Err = err.Error()
		return fr
	}
	fr.Found = found
	fr.Elapsed = elapsed
	fr.ModelCalls = calls
	fr.CostUSD = cost
	fr.Score = grade(fx, found)
	return fr
}

// review builds the SAME pipeline the `buggies review` command builds and runs
// it over base..head. Keeping this in lockstep with cmd/buggies/main.go is the
// point: the harness must measure the shipped reviewer, not a near copy.
func review(ctx context.Context, fx Fixture, root, base, head, dd string, opts Options) (found []finding.Finding, elapsed time.Duration, calls int64, cost float64, err error) {
	g, err := gsqlite.Open(filepath.Join(dd, "graph.db"), fx.Repo)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer g.Close()
	st, err := store.Open(filepath.Join(dd, "findings.db"))
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer st.Close()

	ex := newExecutor(dd, opts)
	if err := ex.CheckRuntime(ctx); err != nil {
		return nil, 0, 0, 0, err
	}

	rt := policy.NewRouter(filepath.Join(dd, "model-cache"))
	ag := &agents.Agents{Router: rt, Tiers: policy.TriadTiers, MaxCandidates: policy.MaxCandidates}
	if !opts.SkillsOff {
		sk, err := skills.Load()
		if err != nil {
			return nil, 0, 0, 0, err
		}
		ag.Skills = sk
	}
	p := &pipeline.Pipeline{
		Repo:     fx.Repo,
		Root:     root,
		Graph:    g,
		Executor: ex,
		Adapter:  node.Adapter{},
		Agents:   ag,
		Store:    st,
		Throttled: func(ctx context.Context, typ finding.Type) (bool, error) {
			d, n, err := st.DismissalRate(ctx, fx.Repo, typ)
			return policy.DefaultThrottle.Suppress(typ, d, n), err
		},
		Limits: pipeline.DefaultLimits(),
		Log:    opts.Log,
	}

	calls0, cost0 := claudecli.Usage()
	res, err := p.Review(ctx, base, head)
	calls1, cost1 := claudecli.Usage()
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return res.Findings, res.Elapsed, calls1 - calls0, cost1 - cost0, nil
}

func newExecutor(dd string, opts Options) *runtime.Docker {
	ex := &runtime.Docker{
		Adapters:   map[sandbox.Ecosystem]adapter.Adapter{sandbox.Node: node.Adapter{}},
		DepsCache:  filepath.Join(dd, "deps"),
		Workspaces: filepath.Join(dd, "ws"),
		Egress:     &runtime.Egress{},
		Log:        opts.Log,
	}
	if opts.AllowRunc {
		ex.Runtime, ex.AllowRunc = "runc", true
	}
	return ex
}

// materialize turns a fixture's base/ and head/ trees into a throwaway git
// repository with two commits, and returns its path and the two revisions.
// The working tree is left at head, which is what the pipeline reviews.
func materialize(fx Fixture) (root, base, head string, err error) {
	root, err = os.MkdirTemp("", "buggies-eval-"+sanitize(fx.Name)+"-")
	if err != nil {
		return "", "", "", err
	}
	cleanup := func(e error) (string, string, string, error) {
		os.RemoveAll(root)
		return "", "", "", e
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=buggies-eval", "GIT_AUTHOR_EMAIL=eval@buggies",
			"GIT_COMMITTER_NAME=buggies-eval", "GIT_COMMITTER_EMAIL=eval@buggies")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := git("init", "-q"); err != nil {
		return cleanup(err)
	}

	baseDir := filepath.Join(fx.Dir, "base")
	headDir := filepath.Join(fx.Dir, "head")
	if !isDir(baseDir) || !isDir(headDir) {
		return cleanup(fmt.Errorf("eval: %s: fixture needs both base/ and head/ trees", fx.Name))
	}

	if err := copyTree(baseDir, root); err != nil {
		return cleanup(err)
	}
	if _, err := git("add", "-A"); err != nil {
		return cleanup(err)
	}
	if _, err := git("commit", "-q", "-m", "base"); err != nil {
		return cleanup(err)
	}
	base, err = git("rev-parse", "HEAD")
	if err != nil {
		return cleanup(err)
	}

	if err := clearTree(root); err != nil {
		return cleanup(err)
	}
	if err := copyTree(headDir, root); err != nil {
		return cleanup(err)
	}
	if _, err := git("add", "-A"); err != nil {
		return cleanup(err)
	}
	if _, err := git("commit", "-q", "-m", "head"); err != nil {
		return cleanup(err)
	}
	head, err = git("rev-parse", "HEAD")
	if err != nil {
		return cleanup(err)
	}
	return root, base, head, nil
}

// copyTree copies every regular file under src into dst, preserving structure.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// clearTree removes everything under dir except the .git directory.
func clearTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func sanitize(name string) string {
	return strings.NewReplacer("/", "__", " ", "_").Replace(name)
}
