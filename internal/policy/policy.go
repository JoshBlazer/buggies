// Package policy holds every product opinion the kernel refuses to have:
// which model at which cascade tier, budgets, and the self-throttle rule.
package policy

import "github.com/joshblazer/kernel/finding"

// Cascade is Buggies' routing policy: optimise for predictable per-seat
// margin. Cheap filters, mid confirms, frontier only on survivors.
type Cascade struct {
	Cheap    string
	Mid      string
	Frontier string
}

// DefaultCascade is Anthropic-only for v0 (self-hosted BYO-key overrides it).
var DefaultCascade = Cascade{
	Cheap:    "claude-haiku-4-5-20251001",
	Mid:      "claude-sonnet-5",
	Frontier: "claude-opus-5",
}

// Throttle is the self-throttle rule from USAGE.md, stated in code: if a
// repo's dismissal rate for a finding type exceeds Threshold over at least
// MinSample decisions, that type is suppressed for that repo and the
// dashboard's calibration state says so. Buggies would rather say less and
// stay trusted than say more and get muted.
type Throttle struct {
	Threshold float64 // dismissal rate in [0,1]
	MinSample int
}

// DefaultThrottle: quiet down once 60% of at least 10 decisions were dismissals.
var DefaultThrottle = Throttle{Threshold: 0.6, MinSample: 10}

// Suppress reports whether a type should be silenced for a repo given its
// dismissal history.
func (t Throttle) Suppress(typ finding.Type, dismissed, total int) bool {
	if total < t.MinSample || total == 0 {
		return false
	}
	return float64(dismissed)/float64(total) > t.Threshold
}
