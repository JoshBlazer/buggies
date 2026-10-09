package claudecli

import (
	"context"
	"testing"

	"github.com/joshblazer/kernel/router"
)

func TestModelAlias(t *testing.T) {
	cases := map[string]string{
		"claude-haiku-4-5-20251001": "haiku",
		"claude-sonnet-5":           "sonnet",
		"claude-sonnet-5-5":         "sonnet", // snapshot suffix still maps to family
		"claude-opus-5":             "opus",
		"HAIKU":                     "haiku",
		"gpt-4o":                    "gpt-4o", // unknown family passes through
	}
	for in, want := range cases {
		if got := modelAlias(in); got != want {
			t.Errorf("modelAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompleteRejectsBadRequests(t *testing.T) {
	p := New()
	ctx := context.Background()

	if _, err := p.Complete(ctx, "sonnet", router.Request{}); err == nil {
		t.Error("empty request: want error, got nil")
	}

	onlySystem := router.Request{Messages: []router.Message{{Role: "system", Content: "x"}}}
	if _, err := p.Complete(ctx, "sonnet", onlySystem); err == nil {
		t.Error("system-only request: want error (no user/assistant turn), got nil")
	}

	badRole := router.Request{Messages: []router.Message{{Role: "wizard", Content: "x"}}}
	if _, err := p.Complete(ctx, "sonnet", badRole); err == nil {
		t.Error("unknown role: want error, got nil")
	}
}

func TestNewHonoursBinOverride(t *testing.T) {
	t.Setenv("BUGGIES_CLAUDE_BIN", "claude.exe")
	if got := New().Bin; got != "claude.exe" {
		t.Errorf("Bin = %q, want claude.exe", got)
	}
}
