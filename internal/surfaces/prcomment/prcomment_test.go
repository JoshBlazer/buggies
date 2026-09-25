package prcomment

import (
	"strings"
	"testing"

	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/sandbox"
)

func TestRenderReproducedFindingCarriesProof(t *testing.T) {
	f := finding.Finding{
		ID: "f_abc", Type: finding.Correctness, Confidence: finding.Reproduced, Impact: finding.DataLoss,
		Claim:    "Gold tier no longer receives its discount",
		Location: finding.Location{Path: "src/discount.ts", Line: 4},
		Proof: &finding.Proof{
			Test:         sandbox.Artifact{Path: "src/__buggies__/discount.test.ts", Content: []byte("expect(applyDiscount(u, 100)).toBe(80)")},
			Result:       sandbox.Result{Outcome: sandbox.Failed, Reproducible: true, Runs: []sandbox.Evidence{{StackTrace: "AssertionError: expected 100 to be 80", Image: "buggies/sandbox-node:22"}, {}}},
			SuggestedFix: "-  if (user.tier === 'Gold')\n+  if (user.tier === 'gold')",
		},
	}
	c := Render(f)
	for _, want := range []string{
		"**Gold tier no longer receives its discount**",
		"`Correctness · Reproduced × Data-loss` · **blocking**",
		"<details>", "Failing test — reproduced 2/2 runs",
		"expect(applyDiscount(u, 100)).toBe(80)",
		"AssertionError", "Suggested fix", "```diff",
		"_Reproduced in sandbox",
		"f_abc",
	} {
		if !strings.Contains(c.Body, want) {
			t.Errorf("missing %q in:\n%s", want, c.Body)
		}
	}
	if c.Path != "src/discount.ts" || c.Line != 4 {
		t.Errorf("anchor = %s:%d", c.Path, c.Line)
	}
}

func TestRenderUnreproducedIsMarkedHonestly(t *testing.T) {
	c := Render(finding.Finding{Confidence: finding.StaticallyProven, Impact: finding.Exploitable, Claim: "x"})
	if !strings.Contains(c.Body, "Static only") || strings.Contains(c.Body, "blocking") || strings.Contains(c.Body, "<details>") {
		t.Fatalf("unreproduced rendering wrong:\n%s", c.Body)
	}
}
