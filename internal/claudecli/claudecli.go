// Package claudecli is a router.Provider that runs model calls through the
// on-device Claude Code CLI (`claude -p`) instead of the Anthropic API. It
// uses whatever auth the local `claude` binary already has (a Claude
// subscription or `ant auth login` profile), so it needs no ANTHROPIC_API_KEY
// and bills no API usage. One Complete call spawns one `claude -p` subprocess.
//
// This provider lives in Buggies, not the kernel: it implements the kernel's
// router.Provider interface from the product side, so nothing product-specific
// (or on-device-specific) leaks across the wall into the shared engine.
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/joshblazer/kernel/router"
)

// Provider runs completions via the local `claude` CLI in print mode.
type Provider struct {
	// Bin is the claude executable; empty means "claude" on PATH (override
	// with BUGGIES_CLAUDE_BIN).
	Bin string
}

var _ router.Provider = (*Provider)(nil)

// New builds a provider using BUGGIES_CLAUDE_BIN or "claude" from PATH.
func New() *Provider {
	bin := os.Getenv("BUGGIES_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	return &Provider{Bin: bin}
}

// result is the subset of `claude -p --output-format json` we consume.
type result struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
	APIErr  string `json:"api_error_status"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete implements router.Provider by shelling out to `claude -p`.
//
// System messages become --system-prompt (replacing the default coding-agent
// prompt with the caller's, so the model does pure reasoning). Remaining turns
// are flattened onto stdin. All tools are disabled via a bogus allowlist, so
// the call is a deterministic text completion with no file or shell access.
func (p *Provider) Complete(ctx context.Context, model string, req router.Request) (router.Response, error) {
	if len(req.Messages) == 0 {
		return router.Response{}, errors.New("claudecli: empty request")
	}

	var system []string
	var turns []string
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = append(system, m.Content)
		case "user":
			turns = append(turns, m.Content)
		case "assistant":
			turns = append(turns, "[assistant]\n"+m.Content)
		default:
			return router.Response{}, fmt.Errorf("claudecli: unknown role %q", m.Role)
		}
	}
	if len(turns) == 0 {
		return router.Response{}, errors.New("claudecli: no user/assistant content")
	}

	args := []string{
		"-p",
		"--output-format", "json",
		"--model", modelAlias(model),
		"--allowedTools", "__none__", // allowlist that matches no real tool => tools off
	}
	if len(system) > 0 {
		args = append(args, "--system-prompt", strings.Join(system, "\n\n"))
	}

	cmd := exec.CommandContext(ctx, p.Bin, args...)
	cmd.Stdin = strings.NewReader(strings.Join(turns, "\n\n"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return router.Response{}, fmt.Errorf("claudecli: %s failed: %w: %s", p.Bin, err, strings.TrimSpace(stderr.String()))
	}

	var r result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return router.Response{}, fmt.Errorf("claudecli: cannot parse CLI output: %w", err)
	}
	if r.IsError || (r.Subtype != "" && r.Subtype != "success") {
		msg := r.Result
		if r.APIErr != "" {
			msg = r.APIErr + ": " + msg
		}
		return router.Response{}, fmt.Errorf("claudecli: run failed: %s", strings.TrimSpace(msg))
	}

	return router.Response{
		Text:         r.Result,
		Model:        model,
		InputTokens:  r.Usage.InputTokens,
		OutputTokens: r.Usage.OutputTokens,
	}, nil
}

// modelAlias maps a cascade model ID to a `claude --model` value. The CLI
// understands the family aliases and resolves each to its current best
// snapshot (haiku -> claude-haiku-5-5, etc.), so a cascade ID like
// "claude-opus-5" need not be a literal CLI model string.
func modelAlias(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "haiku"):
		return "haiku"
	case strings.Contains(m, "opus"):
		return "opus"
	case strings.Contains(m, "sonnet"):
		return "sonnet"
	default:
		return model // pass through an explicit snapshot ID unchanged
	}
}
