package checkrun

import (
	"testing"

	"github.com/joshblazer/kernel/finding"
)

func f(c finding.Confidence, i finding.Impact) finding.Finding {
	return finding.Finding{Confidence: c, Impact: i, Type: finding.Correctness}
}

func TestConclusionBlockingRule(t *testing.T) {
	cases := []struct {
		name     string
		findings []finding.Finding
		want     string
	}{
		{"reproduced data-loss blocks", []finding.Finding{f(finding.Reproduced, finding.DataLoss)}, "failure"},
		{"reproduced exploitable blocks", []finding.Finding{f(finding.Reproduced, finding.Exploitable)}, "failure"},
		{"reproduced degraded is advisory", []finding.Finding{f(finding.Reproduced, finding.Degraded)}, "success"},
		{"unverified exploitable does not block", []finding.Finding{f(finding.Unverified, finding.Exploitable)}, "success"},
		{"static-only data-loss does not block", []finding.Finding{f(finding.StaticallyProven, finding.DataLoss)}, "success"},
		{"no findings", nil, "success"},
		{"one blocks among many", []finding.Finding{
			f(finding.Reproduced, finding.Degraded),
			f(finding.Reproduced, finding.Exploitable),
		}, "failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Render(tc.findings).Conclusion; got != tc.want {
				t.Errorf("Conclusion = %q, want %q", got, tc.want)
			}
		})
	}
}
