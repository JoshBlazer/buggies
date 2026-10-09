// Package security holds cross-cutting safety helpers for the unattended path.
//
// Fork isolation: a pull request from a fork contains code and text an outsider
// controls. Reviewing it must not expose Buggies' secrets (the GitHub
// installation token, the finding-store key, model keys), because a malicious
// fork could try to exfiltrate whatever the review process can read. For a fork
// PR the model subprocess and any tool run with a scrubbed environment.
package security

import "strings"

// secretExact are environment variables always stripped for fork PRs.
var secretExact = map[string]bool{
	"GITHUB_TOKEN":      true,
	"BUGGIES_STORE_KEY": true,
	"ANTHROPIC_API_KEY": true,
}

// secretSubstr marks any variable whose name contains one of these (case-
// insensitive) as a secret — a conservative catch-all.
var secretSubstr = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "API_KEY", "PRIVATE_KEY", "CREDENTIAL"}

// IsSecret reports whether an env var name (the part before '=') is a secret.
func IsSecret(name string) bool {
	if secretExact[name] {
		return true
	}
	up := strings.ToUpper(name)
	for _, s := range secretSubstr {
		if strings.Contains(up, s) {
			return true
		}
	}
	return false
}

// ForkSafeEnv returns env unchanged for a non-fork review, or with every secret
// variable removed for a fork. env is "KEY=VALUE" entries (as os.Environ()).
func ForkSafeEnv(env []string, isFork bool) []string {
	if !isFork {
		return env
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if IsSecret(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
