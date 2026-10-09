package policy

import (
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/joshblazer/kernel/router"
	"github.com/joshblazer/kernel/router/filecache"
	anthropicprov "github.com/joshblazer/kernel/router/providers/anthropic"

	"github.com/joshblazer/buggies/internal/agents"
	"github.com/joshblazer/buggies/internal/claudecli"
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

// NewRouter builds the production router with a persistent fingerprint cache
// under cacheDir. The model backend is selected by BUGGIES_MODEL_BACKEND:
//
//   - "claude-cli" (default): the on-device Claude Code CLI. No API key, no
//     API billing — uses whatever auth the local `claude` binary has.
//   - "anthropic": the Anthropic API (key from ANTHROPIC_API_KEY or an
//     `ant auth login` profile; BUGGIES_MODEL_BASE_URL points a self-hosted
//     deployment at its own endpoint).
//
// The default is on-device so a run needs no secret; set the env var to
// "anthropic" to use the API once a key is provisioned.
func NewRouter(cacheDir string) *router.Router {
	return NewRouterEnv(cacheDir, nil)
}

// NewRouterEnv is NewRouter with an explicit environment for the on-device
// model subprocess. The unattended worker passes a secret-scrubbed environment
// (internal/security.ForkSafeEnv) for fork PRs, so untrusted fork code reviewed
// through the CLI backend can never read Buggies' tokens. env is ignored by the
// "anthropic" backend (it talks to the API in-process, not a subprocess); a
// fork review on that backend must instead run with secrets already absent from
// the process. A nil env inherits the parent's (the trusted, same-repo path).
func NewRouterEnv(cacheDir string, env []string) *router.Router {
	var provider router.Provider
	switch os.Getenv("BUGGIES_MODEL_BACKEND") {
	case "anthropic":
		var opts []anthropicprov.Option
		if u := os.Getenv("BUGGIES_MODEL_BASE_URL"); u != "" {
			opts = append(opts, option.WithBaseURL(u))
		}
		provider = anthropicprov.New(opts...)
	default: // "claude-cli" or unset
		p := claudecli.New()
		p.Env = env
		provider = p
	}
	return &router.Router{
		Policy: ModelPolicy{Provider: provider, Cascade: DefaultCascade},
		Cache:  &filecache.Cache{Dir: cacheDir},
	}
}
