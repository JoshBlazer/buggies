package policy

import (
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/buggies-dev/kernel/router"
	"github.com/buggies-dev/kernel/router/filecache"
	anthropicprov "github.com/buggies-dev/kernel/router/providers/anthropic"

	"github.com/buggies-dev/buggies/internal/agents"
)

// TriadTiers is where each triad purpose runs. Proposing and attacking are
// mid-tier work over a diff; only writing the proof — the step whose output
// a developer actually runs — gets the frontier model.
var TriadTiers = agents.Tiers{
	Propose: router.Mid,
	Attack:  router.Mid,
	Prove:   router.Frontier,
}

// Limits for the triad loop. Conservative: few candidates, one retry.
const (
	MaxCandidates = 3
	ProveRetries  = 1
)

// ModelPolicy implements router.Policy over a single provider.
type ModelPolicy struct {
	Provider router.Provider
	Cascade  Cascade
}

// Model implements router.Policy.
func (p ModelPolicy) Model(tier router.Tier, _ string) (router.Provider, string, error) {
	switch tier {
	case router.Cheap:
		return p.Provider, p.Cascade.Cheap, nil
	case router.Mid:
		return p.Provider, p.Cascade.Mid, nil
	case router.Frontier:
		return p.Provider, p.Cascade.Frontier, nil
	}
	return nil, "", fmt.Errorf("%w: %s", router.ErrNoModel, tier)
}

// NewRouter builds the production router: Anthropic provider (API key from
// ANTHROPIC_API_KEY or an `ant auth login` profile; BUGGIES_MODEL_BASE_URL
// points a self-hosted deployment at its own endpoint) with a persistent
// fingerprint cache under cacheDir.
func NewRouter(cacheDir string) *router.Router {
	var opts []anthropicprov.Option
	if u := os.Getenv("BUGGIES_MODEL_BASE_URL"); u != "" {
		opts = append(opts, option.WithBaseURL(u))
	}
	return &router.Router{
		Policy: ModelPolicy{Provider: anthropicprov.New(opts...), Cascade: DefaultCascade},
		Cache:  &filecache.Cache{Dir: cacheDir},
	}
}
