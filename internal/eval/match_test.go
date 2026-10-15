package eval

import (
	"testing"

	"github.com/joshblazer/kernel/finding"
)

func mustCompile(t *testing.T, m Matcher) Matcher {
	t.Helper()
	if err := m.compile("test"); err != nil {
		t.Fatalf("compile %+v: %v", m, err)
	}
	return m
}

func goldFinding() finding.Finding {
	return finding.Finding{
		Type:       finding.Correctness,
		Confidence: finding.Reproduced,
		Impact:     finding.DataLoss,
		Claim:      "gold tier no longer receives its discount",
		Location:   finding.Location{Repo: "acme/shop", Path: "src/discount.js", Symbol: "applyDiscount", Line: 2},
	}
}

func TestMatcherFields(t *testing.T) {
	f := goldFinding()
	cases := []struct {
		name string
		m    Matcher
		want bool
	}{
		{"type+path", Matcher{Type: "correctness", Path: "src/discount.js"}, true},
		{"wrong type", Matcher{Type: "security", Path: "src/discount.js"}, false},
		{"wrong path", Matcher{Type: "correctness", Path: "src/cart.js"}, false},
		{"symbol ok", Matcher{Type: "correctness", Path: "src/discount.js", Symbol: "applyDiscount"}, true},
		{"symbol wrong", Matcher{Type: "correctness", Path: "src/discount.js", Symbol: "total"}, false},
		{"minImpact met", Matcher{Type: "correctness", Path: "src/discount.js", MinImpact: "data-loss"}, true},
		{"minImpact too high", Matcher{Type: "correctness", Path: "src/discount.js", MinImpact: "exploitable"}, false},
		{"blocking ok", Matcher{Type: "correctness", Path: "src/discount.js", Blocking: true}, true},
		{"line in range", Matcher{Type: "correctness", Path: "src/discount.js", LineRange: []int{1, 5}}, true},
		{"line out of range", Matcher{Type: "correctness", Path: "src/discount.js", LineRange: []int{10, 20}}, false},
		{"claim regex ok", Matcher{Type: "correctness", Path: "src/discount.js", ClaimRegex: "(?i)gold"}, true},
		{"claim regex miss", Matcher{Type: "correctness", Path: "src/discount.js", ClaimRegex: "silver"}, false},
		{"backslash path normalizes", Matcher{Type: "correctness", Path: "src\\discount.js"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mustCompile(t, c.m)
			if got := m.matches(f); got != c.want {
				t.Fatalf("matches=%v want %v", got, c.want)
			}
		})
	}
}

func TestBlockingRequiresGateBar(t *testing.T) {
	f := goldFinding()
	f.Impact = finding.Degraded // reproduced but below data-loss
	m := mustCompile(t, Matcher{Type: "correctness", Path: "src/discount.js", Blocking: true})
	if m.matches(f) {
		t.Fatal("degraded finding should not satisfy a blocking matcher")
	}
}

func TestGradeTruePositive(t *testing.T) {
	fx := Fixture{Expect: []Matcher{mustCompile(t, Matcher{Type: "correctness", Path: "src/discount.js", Symbol: "applyDiscount", Blocking: true})}}
	s := grade(fx, []finding.Finding{goldFinding()})
	if s.TP != 1 || s.FP != 0 || s.FN != 0 || !s.Pass() {
		t.Fatalf("score=%+v", s)
	}
}

func TestGradeFalseNegative(t *testing.T) {
	fx := Fixture{Expect: []Matcher{mustCompile(t, Matcher{Type: "correctness", Path: "src/discount.js"})}}
	s := grade(fx, nil)
	if s.TP != 0 || s.FN != 1 || s.Pass() {
		t.Fatalf("score=%+v", s)
	}
	if len(s.Missed) != 1 {
		t.Fatalf("missed=%v", s.Missed)
	}
}

func TestGradeFalsePositiveOnNegativeFixture(t *testing.T) {
	fx := Fixture{Expect: nil} // negative fixture: expect nothing
	s := grade(fx, []finding.Finding{goldFinding()})
	if s.FP != 1 || s.TP != 0 || s.Pass() {
		t.Fatalf("score=%+v", s)
	}
}

func TestGradeAllowDoesNotCountAsFP(t *testing.T) {
	fx := Fixture{
		Expect: nil,
		Allow:  []Matcher{mustCompile(t, Matcher{Type: "correctness", Path: "src/discount.js"})},
	}
	s := grade(fx, []finding.Finding{goldFinding()})
	if s.FP != 0 || len(s.Allowed) != 1 || !s.Pass() {
		t.Fatalf("score=%+v", s)
	}
}

func TestGradeOneToOneAssignment(t *testing.T) {
	// Two identical expectations, two identical findings: each expectation
	// claims a distinct finding, so TP=2, not TP=1 with a double-count.
	m := Matcher{Type: "correctness", Path: "src/discount.js"}
	fx := Fixture{Expect: []Matcher{mustCompile(t, m), mustCompile(t, m)}}
	s := grade(fx, []finding.Finding{goldFinding(), goldFinding()})
	if s.TP != 2 || s.FP != 0 || s.FN != 0 {
		t.Fatalf("score=%+v", s)
	}
	// One finding, two expectations: one matches, one is a miss.
	s = grade(fx, []finding.Finding{goldFinding()})
	if s.TP != 1 || s.FN != 1 {
		t.Fatalf("score=%+v", s)
	}
}

func TestAggregateMicroAverage(t *testing.T) {
	fxPos := Fixture{Expect: []Matcher{mustCompile(t, Matcher{Type: "correctness", Path: "src/discount.js", Blocking: true})}}
	fxNeg := Fixture{Expect: nil}
	noise := goldFinding()
	noise.Location.Path = "src/other.js"

	scores := []Score{
		grade(fxPos, []finding.Finding{goldFinding()}), // TP=1
		grade(fxNeg, []finding.Finding{noise}),         // FP=1
	}
	agg := aggregate([]Fixture{fxPos, fxNeg}, scores)
	if agg.Overall.TP != 1 || agg.Overall.FP != 1 || agg.Overall.FN != 0 {
		t.Fatalf("overall=%+v", agg.Overall)
	}
	if agg.Overall.Precision != 0.5 || agg.Overall.Recall != 1 {
		t.Fatalf("precision=%v recall=%v", agg.Overall.Precision, agg.Overall.Recall)
	}
	// The true positive was blocking; the noise was data-loss+reproduced so
	// it is blocking too → blocking precision 0.5.
	if agg.Blocking.TP != 1 || agg.Blocking.FP != 1 {
		t.Fatalf("blocking=%+v", agg.Blocking)
	}
	if agg.ByType[finding.Correctness].TP != 1 || agg.ByType[finding.Correctness].FP != 1 {
		t.Fatalf("byType=%+v", agg.ByType[finding.Correctness])
	}
}

func TestLoadFixtures(t *testing.T) {
	fxs, err := Load("../../testdata/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if len(fxs) == 0 {
		t.Fatal("no fixtures loaded")
	}
	byName := map[string]Fixture{}
	for _, fx := range fxs {
		byName[fx.Name] = fx
	}
	gold, ok := byName["gold-tier-discount"]
	if !ok {
		t.Fatalf("gold-tier-discount fixture missing; have %v", byName)
	}
	if gold.Repo != "acme/shop" || len(gold.Expect) != 1 {
		t.Fatalf("gold fixture = %+v", gold)
	}
	if gold.Expect[0].claimRe == nil {
		t.Fatal("claimRegex was not compiled on load")
	}
	neg, ok := byName["clean-refactor"]
	if !ok || len(neg.Expect) != 0 {
		t.Fatalf("clean-refactor negative fixture = %+v (ok=%v)", neg, ok)
	}
}
