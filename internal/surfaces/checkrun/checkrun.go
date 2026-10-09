// Package checkrun renders the CI gate: a GitHub check run that answers one
// question — does this PR contain a reproduced, blocking-level finding? That
// is what makes Buggies part of the merge decision rather than advisory noise
// (USAGE.md Surface 3). Verification ranks above severity everywhere, and the
// gate encodes exactly VISION.md's blocking rule.
package checkrun

import (
	"fmt"
	"strings"

	"github.com/joshblazer/kernel/finding"
)

// CheckName is the stable name GitHub keys the check run by, so a re-run
// updates in place instead of adding a second check.
const CheckName = "buggies"

// Blocking reports whether a finding is merge-gating: reproduced AND
// (data-loss OR exploitable). Everything else is advisory. This is the policy
// dial's default (VISION.md levels).
func Blocking(f finding.Finding) bool {
	return f.Confidence == finding.Reproduced &&
		(f.Impact == finding.DataLoss || f.Impact == finding.Exploitable)
}

// Result is a renderable check run.
type Result struct {
	Name       string
	Conclusion string // "success" | "failure"
	Title      string
	Summary    string
}

// Render builds the check run from a review's findings. The conclusion is
// failure iff at least one finding blocks; advisory findings never fail the
// gate — they ride along as comments.
func Render(findings []finding.Finding) Result {
	var blocking []finding.Finding
	reproduced := 0
	for _, f := range findings {
		if f.Confidence == finding.Reproduced {
			reproduced++
		}
		if Blocking(f) {
			blocking = append(blocking, f)
		}
	}

	r := Result{Name: CheckName}
	if len(blocking) > 0 {
		r.Conclusion = "failure"
		r.Title = fmt.Sprintf("%d blocking finding(s)", len(blocking))
	} else {
		r.Conclusion = "success"
		switch {
		case reproduced > 0:
			r.Title = fmt.Sprintf("%d reproduced, none blocking", reproduced)
		case len(findings) > 0:
			r.Title = fmt.Sprintf("%d advisory finding(s)", len(findings))
		default:
			r.Title = "no findings"
		}
	}

	var b strings.Builder
	if len(findings) == 0 {
		b.WriteString("Buggies found nothing it could reproduce.\n")
	}
	for _, f := range findings {
		mark := "advisory"
		if Blocking(f) {
			mark = "**blocking**"
		}
		fmt.Fprintf(&b, "- %s · %s × %s · %s — `%s:%d`\n",
			mark, f.Confidence, f.Impact, f.Type, f.Location.Path, f.Location.Line)
	}
	r.Summary = b.String()
	return r
}
