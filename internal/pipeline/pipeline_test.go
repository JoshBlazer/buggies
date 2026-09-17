package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buggies-dev/kernel/finding"
	gsqlite "github.com/buggies-dev/kernel/graph/sqlite"
	"github.com/buggies-dev/kernel/router"
	"github.com/buggies-dev/kernel/sandbox"
	"github.com/buggies-dev/kernel/sandbox/adapter/node"

	"github.com/buggies-dev/buggies/internal/agents"
	"github.com/buggies-dev/buggies/internal/store"
)

func gitRepo(t *testing.T) (root, base, head string) {
	t.Helper()
	root = t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(p, body string) {
		full := filepath.Join(root, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("package.json", `{"name":"fixture","private":true,"type":"module"}`)
	write("src/discount.js", "export function applyDiscount(user, price) {\n  if (user.tier === 'gold') return price * 0.8;\n  return price;\n}\n")
	write("src/cart.js", "import { applyDiscount } from './discount.js';\nexport function total(user, items) {\n  return applyDiscount(user, items.reduce((a, b) => a + b, 0));\n}\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base = run("rev-parse", "HEAD")
	write("src/discount.js", "export function applyDiscount(user, price) {\n  if (user.tier === 'Gold') return price * 0.8;\n  return price;\n}\n")
	run("commit", "-q", "-am", "break gold tier")
	head = run("rev-parse", "HEAD")
	return
}

type scripted map[string]string

func (s scripted) Call(_ context.Context, _ router.Tier, req router.Request) (router.Response, error) {
	return router.Response{Text: s[req.Purpose]}, nil
}

type fakeExec struct{ calls int }

func (f *fakeExec) Run(_ context.Context, req sandbox.Request) (sandbox.Result, error) {
	f.calls++
	if !strings.Contains(req.Artifact.Path, "__buggies__") {
		return sandbox.Result{Outcome: sandbox.Errored}, nil
	}
	return sandbox.Result{Outcome: sandbox.Failed, Reproducible: true, Runs: []sandbox.Evidence{{ExitCode: 1, Stderr: "AssertionError: 100 !== 80"}, {ExitCode: 1}}}, nil
}

func newPipeline(t *testing.T, root string, ex sandbox.Executor, st finding.Store) *Pipeline {
	t.Helper()
	g, err := gsqlite.Open(filepath.Join(t.TempDir(), "graph.db"), "acme/shop")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	scripts := scripted{
		agents.PurposePropose: `{"candidates":[{"type":"correctness","impact":"data-loss","claim":"gold tier no longer receives its discount","path":"src/discount.js","symbol":"applyDiscount","line":2,"excerpt":"if (user.tier === 'Gold') return price * 0.8;","rationale":"tier strings are lowercase everywhere else"}]}`,
		agents.PurposeAttack:  `{"survives":true,"reason":"cart.js passes lowercase tiers"}`,
		agents.PurposeProve:   `{"test_path":"src/__buggies__/discount.test.js","test":"import { test } from 'node:test';\nimport assert from 'node:assert/strict';\nimport { applyDiscount } from '../discount.js';\ntest('gold', () => assert.equal(applyDiscount({tier:'gold'}, 100), 80));\n","fix_diff":"--- a/src/discount.js\n+++ b/src/discount.js\n@@ -2 +2 @@\n-  if (user.tier === 'Gold') return price * 0.8;\n+  if (user.tier === 'gold') return price * 0.8;\n"}`,
	}
	return &Pipeline{
		Repo:     "acme/shop",
		Root:     root,
		Graph:    g,
		Executor: ex,
		Adapter:  node.Adapter{},
		Agents:   &agents.Agents{Router: scripts, Tiers: agents.Tiers{}, MaxCandidates: 3},
		Store:    st,
		Limits:   DefaultLimits(),
		Log:      testLog{t},
	}
}

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func TestReviewProducesReproducedFindingWithStableID(t *testing.T) {
	root, base, head := gitRepo(t)
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ex := &fakeExec{}
	p := newPipeline(t, root, ex, st)

	res, err := p.Review(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %d (traces: %+v)", len(res.Findings), res.Traces)
	}
	f := res.Findings[0]
	if f.Confidence != finding.Reproduced || f.Impact != finding.DataLoss || f.Proof == nil || len(f.Proof.Test.Content) == 0 {
		t.Fatalf("finding = %+v", f)
	}
	if !f.Blocking() {
		t.Fatal("reproduced data-loss should block")
	}
	if len(res.Radius.Affected) == 0 || res.Radius.Affected[0].Symbol.Name != "total" {
		t.Fatalf("blast radius did not reach cart.total: %+v", res.Radius)
	}
	if ex.calls != 1 {
		t.Fatalf("sandbox calls = %d", ex.calls)
	}

	// Same code, same finding, same ID — and the store dedups it.
	res2, err := p.Review(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Findings) != 1 || res2.Findings[0].ID != f.ID {
		t.Fatalf("second run: %+v", res2.Findings)
	}
	all, _ := st.List(context.Background(), finding.Query{Repo: "acme/shop"})
	if len(all) != 1 {
		t.Fatalf("store has %d findings, want 1", len(all))
	}

	// Once a human dismisses it, a re-run stays quiet.
	if err := st.Transition(context.Background(), f.ID, finding.Dismissed, "intended"); err != nil {
		t.Fatal(err)
	}
	res3, _ := p.Review(context.Background(), base, head)
	if len(res3.Findings) != 0 {
		t.Fatal("dismissed finding was re-reported")
	}
}

func TestUnreproducedCandidateNeverBecomesAFinding(t *testing.T) {
	root, base, head := gitRepo(t)
	st, _ := store.Open(":memory:")
	defer st.Close()
	flaky := &flakyExec{}
	p := newPipeline(t, root, flaky, st)
	res, err := p.Review(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("unreproduced candidate surfaced: %+v", res.Findings)
	}
	all, _ := st.List(context.Background(), finding.Query{})
	if len(all) != 0 {
		t.Fatal("unreproduced candidate was stored")
	}
	if len(res.Traces) != 1 || res.Traces[0].Stage == "proved" {
		t.Fatalf("traces = %+v", res.Traces)
	}
}

type flakyExec struct{}

func (flakyExec) Run(context.Context, sandbox.Request) (sandbox.Result, error) {
	return sandbox.Result{Outcome: sandbox.Failed, Reproducible: false}, nil
}

func TestSelfThrottleSuppressesNoisyType(t *testing.T) {
	root, base, head := gitRepo(t)
	st, _ := store.Open(":memory:")
	defer st.Close()
	p := newPipeline(t, root, &fakeExec{}, st)
	p.Throttled = func(context.Context, finding.Type) (bool, error) { return true, nil }
	res, err := p.Review(context.Background(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || len(res.Throttled) != 1 {
		t.Fatalf("findings=%d throttled=%d", len(res.Findings), len(res.Throttled))
	}
}
