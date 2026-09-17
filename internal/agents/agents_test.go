package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/buggies-dev/kernel/finding"
	"github.com/buggies-dev/kernel/router"
	"github.com/buggies-dev/kernel/sandbox"
	"github.com/buggies-dev/kernel/triad"
)

// scripted returns canned model output per purpose and records tiers.
type scripted struct {
	out   map[string]string
	tiers map[string]router.Tier
}

func (s *scripted) Call(_ context.Context, tier router.Tier, req router.Request) (router.Response, error) {
	if s.tiers == nil {
		s.tiers = map[string]router.Tier{}
	}
	s.tiers[req.Purpose] = tier
	return router.Response{Text: s.out[req.Purpose]}, nil
}

func newAgents(out map[string]string) (*Agents, *scripted) {
	r := &scripted{out: out}
	return &Agents{Router: r, Tiers: Tiers{Propose: router.Mid, Attack: router.Mid, Prove: router.Frontier}, MaxCandidates: 2}, r
}

func TestProposerDropsOutOfScopeTypesAndCaps(t *testing.T) {
	a, r := newAgents(map[string]string{PurposePropose: "Sure! ```json\n" + `{"candidates":[
	  {"type":"security","claim":"sql injection","path":"a.ts","line":1},
	  {"type":"correctness","impact":"data-loss","claim":"gold gets no discount","path":"src/discount.ts","symbol":"applyDiscount","line":4,"excerpt":"if (user.tier === 'Gold')","rationale":"tiers are lowercase"},
	  {"type":"contract","claim":"caller breaks","path":"src/cart.ts","line":9},
	  {"type":"correctness","claim":"third","path":"c.ts","line":1}
	]}` + "\n```"})
	cands, err := a.Proposer().Propose(context.Background(), triad.Scope{Repo: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 || cands[0].Type != finding.Correctness || cands[1].Type != finding.Contract {
		t.Fatalf("cands = %+v", cands)
	}
	if cands[0].Impact != finding.DataLoss || cands[1].Impact != finding.Cosmetic {
		t.Fatalf("impacts = %s, %s", cands[0].Impact, cands[1].Impact)
	}
	if cands[0].Location.Repo != "r" || cands[0].Location.Line != 4 {
		t.Fatalf("location = %+v", cands[0].Location)
	}
	if r.tiers[PurposePropose] != router.Mid {
		t.Fatalf("propose ran on tier %s", r.tiers[PurposePropose])
	}
}

func TestSkepticParsesVerdict(t *testing.T) {
	a, _ := newAgents(map[string]string{PurposeAttack: `{"survives":false,"reason":"tier is normalised upstream"}`})
	v, err := a.Skeptic().Attack(context.Background(), triad.Candidate{}, triad.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Survives || !strings.Contains(v.Reason, "upstream") {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestProverRequiresBuggiesDirAndFailingExpectation(t *testing.T) {
	a, r := newAgents(map[string]string{PurposeProve: `{"test_path":"src/__buggies__/discount.test.ts","test":"import {}","fix_diff":"--- a\n+++ b\n"}`})
	att, err := a.Prover().Prove(context.Background(), triad.Candidate{}, triad.Scope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if att.ExpectedOutcome != sandbox.Failed || att.Test.Path != "src/__buggies__/discount.test.ts" || att.SuggestedFix == "" {
		t.Fatalf("attempt = %+v", att)
	}
	if r.tiers[PurposeProve] != router.Frontier {
		t.Fatalf("prove ran on tier %s", r.tiers[PurposeProve])
	}

	a, _ = newAgents(map[string]string{PurposeProve: `{"test_path":"../../etc/x.test.ts","test":"x"}`})
	if _, err := a.Prover().Prove(context.Background(), triad.Candidate{}, triad.Scope{}, nil); err == nil {
		t.Fatal("path traversal accepted")
	}
	a, _ = newAgents(map[string]string{PurposeProve: `{"test_path":"src/__buggies__/x.test.ts","test":""}`})
	if _, err := a.Prover().Prove(context.Background(), triad.Candidate{}, triad.Scope{}, nil); err == nil {
		t.Fatal("empty test accepted")
	}
}

func TestGarbageOutputIsAnErrorNotAFinding(t *testing.T) {
	a, _ := newAgents(map[string]string{PurposePropose: "I think there might be a bug on line 4."})
	if _, err := a.Proposer().Propose(context.Background(), triad.Scope{}); err == nil {
		t.Fatal("prose accepted as candidates")
	}
}
