package eval

import (
	"sort"

	"github.com/joshblazer/kernel/finding"
)

// Score is the confusion-matrix result of comparing one fixture's expected
// findings against the findings a review actually produced.
type Score struct {
	// TP: expected findings that were reproduced. FN: expected but missed.
	// FP: produced findings that no expectation (and no allow matcher) covers.
	TP, FP, FN int

	// Matched pairs an expectation index to the finding that satisfied it, for
	// the per-finding report. Missed holds expectation indices with no match.
	Matched map[int]finding.Finding `json:"-"`
	Missed  []int                   `json:"-"`
	// Unexpected are the findings counted as false positives (noise).
	Unexpected []finding.Finding `json:"-"`
	// Allowed are findings matched by an Allow matcher: real, but not scored.
	Allowed []finding.Finding `json:"-"`
}

// Pass reports whether the fixture scored perfectly: every expected finding
// reproduced and zero noise.
func (s Score) Pass() bool { return s.FN == 0 && s.FP == 0 }

// grade compares a fixture's expectations against the findings a review
// produced. The matching is a greedy one-to-one assignment: each expectation
// claims the first not-yet-claimed finding it matches. It is pure — no
// sandbox, no I/O — which is what makes the scoring unit-testable.
func grade(fx Fixture, found []finding.Finding) Score {
	s := Score{Matched: map[int]finding.Finding{}}
	claimed := make([]bool, len(found))

	for ei := range fx.Expect {
		hit := -1
		for fi := range found {
			if claimed[fi] {
				continue
			}
			if fx.Expect[ei].matches(found[fi]) {
				hit = fi
				break
			}
		}
		if hit >= 0 {
			claimed[hit] = true
			s.TP++
			s.Matched[ei] = found[hit]
		} else {
			s.FN++
			s.Missed = append(s.Missed, ei)
		}
	}

	for fi := range found {
		if claimed[fi] {
			continue
		}
		if allowedBy(fx.Allow, found[fi]) {
			s.Allowed = append(s.Allowed, found[fi])
			continue
		}
		s.FP++
		s.Unexpected = append(s.Unexpected, found[fi])
	}
	return s
}

func allowedBy(allow []Matcher, f finding.Finding) bool {
	for i := range allow {
		if allow[i].matches(f) {
			return true
		}
	}
	return false
}

// Metrics are precision/recall/F1 over a set of scores.
type Metrics struct {
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	FN        int     `json:"fn"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

func (m *Metrics) finish() {
	m.Precision = ratio(m.TP, m.TP+m.FP)
	m.Recall = ratio(m.TP, m.TP+m.FN)
	if m.Precision+m.Recall > 0 {
		m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
	}
}

// ratio is TP/(TP+x); a denominator of zero yields 1.0 (vacuously perfect: no
// findings expected and none wrongly produced, or nothing to recall).
func ratio(num, den int) float64 {
	if den == 0 {
		return 1
	}
	return float64(num) / float64(den)
}

// Aggregate is the micro-averaged result over every fixture, plus a per-type
// breakdown and the blocking-only slice (findings at the merge-gate bar, where
// precision matters most).
type Aggregate struct {
	Overall  Metrics                   `json:"overall"`
	Blocking Metrics                   `json:"blocking"`
	ByType   map[finding.Type]*Metrics `json:"byType"`
}

// aggregate micro-averages the per-fixture scores. The by-type and
// blocking slices re-grade from the matched/missed/unexpected detail so a
// type's or the gate's numbers are independently meaningful.
func aggregate(fxs []Fixture, scores []Score) Aggregate {
	agg := Aggregate{ByType: map[finding.Type]*Metrics{}}
	typeOf := func(m Matcher) finding.Type { t, _ := parseType(m.Type); return t }
	bump := func(by finding.Type, tp, fp, fn int) {
		mt := agg.ByType[by]
		if mt == nil {
			mt = &Metrics{}
			agg.ByType[by] = mt
		}
		mt.TP += tp
		mt.FP += fp
		mt.FN += fn
	}

	for i, s := range scores {
		agg.Overall.TP += s.TP
		agg.Overall.FP += s.FP
		agg.Overall.FN += s.FN

		fx := fxs[i]
		// True positives, keyed by the expectation's type.
		for ei, f := range s.Matched {
			bump(typeOf(fx.Expect[ei]), 1, 0, 0)
			if f.Blocking() {
				agg.Blocking.TP++
			}
		}
		// False negatives, keyed by the expectation's type; blocking FN when
		// the expectation demanded the gate bar.
		for _, ei := range s.Missed {
			bump(typeOf(fx.Expect[ei]), 0, 0, 1)
			if fx.Expect[ei].Blocking {
				agg.Blocking.FN++
			}
		}
		// False positives, keyed by the finding's own type.
		for _, f := range s.Unexpected {
			bump(f.Type, 0, 1, 0)
			if f.Blocking() {
				agg.Blocking.FP++
			}
		}
	}

	agg.Overall.finish()
	agg.Blocking.finish()
	for _, mt := range agg.ByType {
		mt.finish()
	}
	return agg
}

// sortedTypes returns the by-type keys in a stable order for reporting.
func sortedTypes(m map[finding.Type]*Metrics) []finding.Type {
	out := make([]finding.Type, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
