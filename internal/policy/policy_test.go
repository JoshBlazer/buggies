package policy

import (
	"testing"

	"github.com/joshblazer/kernel/finding"
)

func TestThrottleNeedsSampleBeforeSuppressing(t *testing.T) {
	th := DefaultThrottle
	if th.Suppress(finding.Correctness, 5, 5) {
		t.Fatal("suppressed on too small a sample")
	}
	if !th.Suppress(finding.Correctness, 8, 10) {
		t.Fatal("did not suppress at 80% dismissal over 10")
	}
	if th.Suppress(finding.Correctness, 5, 10) {
		t.Fatal("suppressed at 50%, below threshold")
	}
}
