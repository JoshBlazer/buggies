package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/router"
	"github.com/joshblazer/kernel/sandbox"
	"github.com/joshblazer/kernel/triad"
)

// Purposes are the router labels the policy maps to cascade tiers.
const (
	PurposePropose = "propose"
	PurposeAttack  = "attack"
	PurposeProve   = "prove"
)

// Caller is the slice of the router the agents need.
type Caller interface {
	Call(ctx context.Context, tier router.Tier, req router.Request) (router.Response, error)
}

// Tiers says which cascade rung each purpose runs on. Supplied by policy.
type Tiers struct {
	Propose, Attack, Prove router.Tier
}

// Agents bundles Buggies' three triad agents over one router.
type Agents struct {
	Router Caller
	Tiers  Tiers
	// MaxCandidates caps what the proposer may return. Conservative by design.
	MaxCandidates int
}

// Proposer returns the propose agent.
func (a *Agents) Proposer() triad.Proposer { return proposer{a} }

// Skeptic returns the attack agent.
func (a *Agents) Skeptic() triad.Skeptic { return skeptic{a} }

// Prover returns the prove agent.
func (a *Agents) Prover() triad.Prover { return prover{a} }

const personality = `You are part of Buggies, a code reviewer whose entire reputation rests on never crying wolf.
Every claim you make will be checked by a skeptic and then must be reproduced by a failing unit test in a sandbox before a developer ever sees it. Unreproducible claims are discarded, so speculation is wasted effort.
Prefer silence to a guess. Style, naming, formatting, performance opinions and "consider adding" suggestions are never findings.
Respond with a single JSON object and nothing else: no prose, no markdown fences.`

// ---- propose -------------------------------------------------------------

type proposer struct{ a *Agents }

type proposeOut struct {
	Candidates []struct {
		Type      string `json:"type"`
		Impact    string `json:"impact"`
		Claim     string `json:"claim"`
		Path      string `json:"path"`
		Symbol    string `json:"symbol"`
		Line      int    `json:"line"`
		Excerpt   string `json:"excerpt"`
		Rationale string `json:"rationale"`
	} `json:"candidates"`
}

func (p proposer) Propose(ctx context.Context, scope triad.Scope) ([]triad.Candidate, error) {
	max := p.a.MaxCandidates
	if max <= 0 {
		max = 3
	}
	sys := personality + fmt.Sprintf(`

Task: read a pull-request diff together with the code it can affect (its blast radius) and name the defects a unit test could demonstrate.

Only two types are in scope:
- "correctness": the changed code computes a wrong result, mishandles null/undefined/empty input, has an off-by-one, or throws where it should not.
- "contract": the change breaks an existing caller shown in the blast radius (changed signature, return shape, thrown error, or behaviour a caller relies on).

Rules:
- At most %d candidates. Zero is a perfectly good answer; most diffs have no reproducible defect.
- Each candidate must be demonstrable by calling the changed or affected function directly in a unit test with concrete inputs and asserting a concrete expected value. If you cannot picture the exact assertion, do not propose it.
- "excerpt" is the exact offending source line(s), copied verbatim.
- "line" is the line number in the file at the head revision.
- "impact" is what the defect does to a user when it fires: "cosmetic" (wrong text/log), "degraded" (wrong result or error a user sees), "data-loss" (persists wrong data, loses or corrupts state, wrong money), "exploitable" (an attacker can abuse it).

Output: {"candidates":[{"type":"correctness"|"contract","impact":"cosmetic"|"degraded"|"data-loss"|"exploitable","claim":"one sentence a developer reads first","path":"repo-relative path","symbol":"enclosing function or Class.method","line":N,"excerpt":"verbatim code","rationale":"why it is wrong and what input exposes it"}]}`, max)

	resp, err := p.a.Router.Call(ctx, p.a.Tiers.Propose, router.Request{
		Purpose:   PurposePropose,
		MaxTokens: 4000,
		Messages: []router.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: renderScope(scope)},
		},
	})
	if err != nil {
		return nil, err
	}
	var out proposeOut
	if err := parseJSON(resp.Text, &out); err != nil {
		return nil, fmt.Errorf("propose: %w", err)
	}
	var cands []triad.Candidate
	for _, c := range out.Candidates {
		typ := finding.Type(strings.ToLower(c.Type))
		if typ != finding.Correctness && typ != finding.Contract {
			continue // out-of-scope types are dropped silently: v0 proves only these two
		}
		if c.Path == "" || c.Claim == "" {
			continue
		}
		cands = append(cands, triad.Candidate{
			Type:      typ,
			Impact:    parseImpact(c.Impact),
			Claim:     c.Claim,
			Location:  finding.Location{Repo: scope.Repo, Path: c.Path, Symbol: c.Symbol, Line: c.Line},
			Excerpt:   c.Excerpt,
			Rationale: c.Rationale,
		})
		if len(cands) == max {
			break
		}
	}
	return cands, nil
}

// ---- attack --------------------------------------------------------------

type skeptic struct{ a *Agents }

type attackOut struct {
	Survives bool   `json:"survives"`
	Reason   string `json:"reason"`
}

func (s skeptic) Attack(ctx context.Context, c triad.Candidate, scope triad.Scope) (triad.Verdict, error) {
	sys := personality + `

Task: you are the skeptic. A colleague claims the diff contains a defect. Your job is to kill the claim. It survives only if you cannot.

Kill it if any of these hold:
- the "bug" is guarded elsewhere in the shown code (validation upstream, a type that makes the input impossible, a caller that never passes that shape);
- the behaviour is clearly intentional or matches how every caller in the blast radius uses it;
- the claim depends on code, data or environment that is not shown;
- a unit test could not demonstrate it deterministically with concrete inputs;
- it is a style, naming, performance or "could be nicer" opinion.

If the claim survives, say precisely which input exposes it and what the wrong output is, so the prover can write the test.

Output: {"survives":true|false,"reason":"one or two sentences"}`

	user := renderScope(scope) + "\n\n## Claim under attack\n" + renderCandidate(c)
	resp, err := s.a.Router.Call(ctx, s.a.Tiers.Attack, router.Request{
		Purpose:   PurposeAttack,
		MaxTokens: 1500,
		Messages: []router.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return triad.Verdict{}, err
	}
	var out attackOut
	if err := parseJSON(resp.Text, &out); err != nil {
		return triad.Verdict{}, fmt.Errorf("attack: %w", err)
	}
	return triad.Verdict{Survives: out.Survives, Reason: out.Reason}, nil
}

// ---- prove ---------------------------------------------------------------

type prover struct{ a *Agents }

type proveOut struct {
	TestPath string `json:"test_path"`
	Test     string `json:"test"`
	FixDiff  string `json:"fix_diff"`
}

func (p prover) Prove(ctx context.Context, c triad.Candidate, scope triad.Scope, prior []triad.Outcome) (triad.Attempt, error) {
	sys := personality + `

Task: you are the prover. Write the smallest unit test that FAILS on the current code because of the claimed defect and would PASS once the defect is fixed. The test is the proof a developer will run.

Rules:
- Import the real code under test by relative path from the test file; never re-implement or mock it.
- One test, concrete inputs, one assertion on a concrete expected value. No snapshots, no network, no filesystem, no timers, no randomness.
- The test must fail by assertion, not by import error, syntax error or missing dependency. If the project's own test runner is not available, use node's built-in "node:test" and "node:assert/strict".
- Put the test at test_path under a "__buggies__" directory next to the file under test, with the runner's expected suffix (".test.ts" / ".test.js").
- fix_diff is a minimal unified diff against the head revision that makes the test pass. It may be empty if you are not confident.
- If a previous attempt is shown with its sandbox output, fix what went wrong: an "errored" outcome means the test could not run (imports, syntax, runner) - repair that; a "passed" outcome means the assertion did not expose the defect - reconsider the inputs, or return an empty test if the defect is not real.

Output: {"test_path":"path/__buggies__/name.test.ts","test":"full file contents","fix_diff":"unified diff or empty"}`

	var sb strings.Builder
	sb.WriteString(renderScope(scope))
	sb.WriteString("\n\n## Defect to prove\n")
	sb.WriteString(renderCandidate(c))
	for i, o := range prior {
		fmt.Fprintf(&sb, "\n\n## Previous attempt %d (%s%s)\n", i+1, o.Result.Outcome, reproNote(o.Result))
		fmt.Fprintf(&sb, "test_path: %s\n```\n%s\n```\n", o.Attempt.Test.Path, string(o.Attempt.Test.Content))
		ev := o.Result.Evidence()
		if o.Err != nil {
			fmt.Fprintf(&sb, "executor error: %v\n", o.Err)
		}
		fmt.Fprintf(&sb, "stdout:\n```\n%s\n```\nstderr:\n```\n%s\n```\n", tail(ev.Stdout, 3000), tail(ev.Stderr, 3000))
	}

	resp, err := p.a.Router.Call(ctx, p.a.Tiers.Prove, router.Request{
		Purpose:   PurposeProve,
		MaxTokens: 8000,
		Messages: []router.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: sb.String()},
		},
	})
	if err != nil {
		return triad.Attempt{}, err
	}
	var out proveOut
	if err := parseJSON(resp.Text, &out); err != nil {
		return triad.Attempt{}, fmt.Errorf("prove: %w", err)
	}
	if out.TestPath == "" || strings.TrimSpace(out.Test) == "" {
		return triad.Attempt{}, errors.New("prove: model declined to write a test")
	}
	out.TestPath = strings.TrimPrefix(strings.ReplaceAll(out.TestPath, "\\", "/"), "./")
	if !strings.Contains(out.TestPath, "__buggies__/") || strings.Contains(out.TestPath, "..") {
		return triad.Attempt{}, fmt.Errorf("prove: refusing test path %q", out.TestPath)
	}
	return triad.Attempt{
		Test:            sandbox.Artifact{Path: out.TestPath, Content: []byte(out.Test)},
		ExpectedOutcome: sandbox.Failed,
		SuggestedFix:    out.FixDiff,
	}, nil
}

// ---- helpers -------------------------------------------------------------

func renderScope(s triad.Scope) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Repository: %s (ecosystem: %s)\n", s.Repo, s.Ecosystem)
	if s.Scaffold != "" {
		sb.WriteString("\n## Project facts and deterministic tool output\n")
		sb.WriteString(s.Scaffold)
		sb.WriteString("\n")
	}
	sb.WriteString("\n## Diff\n```diff\n")
	sb.WriteString(s.Diff)
	sb.WriteString("\n```\n")
	if s.Radius != "" {
		sb.WriteString("\n## Blast radius (code the change can affect, with current contents)\n")
		sb.WriteString(s.Radius)
		sb.WriteString("\n")
	}
	return sb.String()
}

func parseImpact(s string) finding.Impact {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "exploitable":
		return finding.Exploitable
	case "data-loss", "data_loss", "dataloss":
		return finding.DataLoss
	case "degraded":
		return finding.Degraded
	}
	return finding.Cosmetic
}

func renderCandidate(c triad.Candidate) string {
	return fmt.Sprintf("type: %s (impact: %s)\nclaim: %s\nlocation: %s:%d (%s)\nexcerpt:\n```\n%s\n```\nrationale: %s",
		c.Type, c.Impact, c.Claim, c.Location.Path, c.Location.Line, c.Location.Symbol, c.Excerpt, c.Rationale)
}

func reproNote(r sandbox.Result) string {
	if len(r.Runs) > 1 && !r.Reproducible {
		return ", not reproducible across runs"
	}
	return ""
}

// parseJSON tolerates a fenced or prose-wrapped object by taking the
// outermost {...}. Anything else is an error, never a guess.
func parseJSON(text string, v any) error {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return fmt.Errorf("no JSON object in model output: %q", tail(text, 200))
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), v); err != nil {
		return fmt.Errorf("invalid JSON from model: %w", err)
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
