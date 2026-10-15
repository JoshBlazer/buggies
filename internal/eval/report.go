package eval

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// WriteText renders a human-readable report: a per-fixture table, the
// aggregate precision/recall/F1, a by-type breakdown, and the blocking-only
// numbers (where precision matters most). The format is stable enough to diff
// across runs.
func WriteText(w io.Writer, rep Report) {
	fmt.Fprintf(w, "buggies eval — sandbox runtime: %s\n\n", rep.Runtime)

	fmt.Fprintf(w, "%-24s %5s %5s %5s %8s %10s %8s  %s\n",
		"FIXTURE", "TP", "FP", "FN", "ELAPSED", "MODEL$", "CALLS", "RESULT")
	for _, fr := range rep.Fixtures {
		result := "PASS"
		switch {
		case fr.Err != "" && fr.Skipped:
			result = "SKIP (" + fr.Err + ")"
		case fr.Err != "":
			result = "ERROR (" + fr.Err + ")"
		case !fr.Score.Pass():
			result = "FAIL"
		}
		fmt.Fprintf(w, "%-24s %5d %5d %5d %8s %10.4f %8d  %s\n",
			trunc(fr.Name, 24), fr.Score.TP, fr.Score.FP, fr.Score.FN,
			roundMs(fr.Elapsed), fr.CostUSD, fr.ModelCalls, result)
		for _, f := range fr.Score.Unexpected {
			fmt.Fprintf(w, "    + unexpected: %s %s %s:%d — %s\n",
				f.Type, f.Impact, f.Location.Path, f.Location.Line, trunc(f.Claim, 60))
		}
		for _, ei := range fr.Score.Missed {
			m := fr.Fixture.Expect[ei]
			fmt.Fprintf(w, "    - missed:     %s %s %s%s\n", m.Type, impactNote(m), m.Path, symbolNote(m))
		}
	}

	a := rep.Aggregate
	fmt.Fprintf(w, "\noverall   precision %s  recall %s  f1 %s   (tp=%d fp=%d fn=%d)\n",
		pct(a.Overall.Precision), pct(a.Overall.Recall), pct(a.Overall.F1), a.Overall.TP, a.Overall.FP, a.Overall.FN)
	fmt.Fprintf(w, "blocking  precision %s  recall %s  f1 %s   (tp=%d fp=%d fn=%d)\n",
		pct(a.Blocking.Precision), pct(a.Blocking.Recall), pct(a.Blocking.F1), a.Blocking.TP, a.Blocking.FP, a.Blocking.FN)
	if len(a.ByType) > 0 {
		fmt.Fprintln(w, "by type:")
		for _, t := range sortedTypes(a.ByType) {
			m := a.ByType[t]
			fmt.Fprintf(w, "  %-12s precision %s  recall %s  f1 %s   (tp=%d fp=%d fn=%d)\n",
				t, pct(m.Precision), pct(m.Recall), pct(m.F1), m.TP, m.FP, m.FN)
		}
	}
	if rep.Pass {
		fmt.Fprintln(w, "\nPASS")
	} else {
		fmt.Fprintln(w, "\nFAIL")
	}
}

func pct(f float64) string { return fmt.Sprintf("%5.1f%%", f*100) }

func roundMs(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return d.Round(time.Millisecond).String()
}

func impactNote(m Matcher) string {
	if m.MinImpact != "" {
		return m.MinImpact + " "
	}
	return ""
}

func symbolNote(m Matcher) string {
	if m.Symbol != "" {
		return " (" + m.Symbol + ")"
	}
	return ""
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
