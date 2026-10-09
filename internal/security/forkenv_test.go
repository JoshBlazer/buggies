package security

import (
	"slices"
	"strings"
	"testing"
)

// TestForkSafeEnvStripsSecrets is the fork half of the M10 exit check: a fork
// PR review runs with no token/secret in its environment.
func TestForkSafeEnvStripsSecrets(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GITHUB_TOKEN=ghs_secret",
		"BUGGIES_STORE_KEY=deadbeef",
		"ANTHROPIC_API_KEY=sk-ant-xxx",
		"MY_SERVICE_TOKEN=abc",
		"DB_PASSWORD=hunter2",
		"NODE_ENV=test",
		"CI=true",
	}

	fork := ForkSafeEnv(env, true)
	for _, kv := range fork {
		name, _, _ := strings.Cut(kv, "=")
		if IsSecret(name) {
			t.Errorf("fork env still contains secret %q", name)
		}
	}
	// Benign vars survive.
	for _, want := range []string{"PATH=/usr/bin", "NODE_ENV=test", "CI=true"} {
		if !slices.Contains(fork, want) {
			t.Errorf("fork env dropped benign var %q", want)
		}
	}
	// Specifically: no GitHub token reaches the fork review.
	for _, kv := range fork {
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			t.Fatal("GITHUB_TOKEN leaked to fork review")
		}
	}

	// Non-fork review is untouched.
	if got := ForkSafeEnv(env, false); len(got) != len(env) {
		t.Errorf("non-fork env changed: got %d vars, want %d", len(got), len(env))
	}
}
