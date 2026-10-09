package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/sandbox"
)

// TestEveryToolResponds is the M12 exit check: start the server and call every
// tool, asserting each returns a valid (non-error) response.
func TestEveryToolResponds(t *testing.T) {
	dismissed := ""
	deps := Deps{
		BlastRadius: func(_ context.Context, _, _, _ string) ([]string, error) {
			return []string{"Option.is", "Command._findOption"}, nil
		},
		Review: func(_ context.Context, _, _, _ string) ([]finding.Finding, error) {
			return []finding.Finding{{ID: "f_1", Claim: "off by one", Confidence: finding.Reproduced}}, nil
		},
		GetFinding: func(_ context.Context, id string) (finding.Finding, error) {
			return finding.Finding{ID: id, Claim: "off by one",
				Proof: &finding.Proof{Test: sandbox.Artifact{Path: "x.test.js", Content: []byte("test contents")}}}, nil
		},
		Dismiss: func(_ context.Context, id, _ string) error { dismissed = id; return nil },
	}

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	server := NewServer(deps)
	go func() { _ = server.Run(ctx, serverT) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Every tool is advertised.
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"blast_radius": false, "review": false, "get_finding": false, "dismiss": false}
	for _, tool := range lt.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q not advertised", name)
		}
	}

	call := func(name string, args any) {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: transport error: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool reported error: %+v", name, res.Content)
		}
		if res.StructuredContent == nil {
			t.Fatalf("%s: no structured result", name)
		}
	}

	call("blast_radius", diffArgs{Repo: "r", Base: "a", Head: "b"})
	call("review", diffArgs{Repo: "r", Base: "a", Head: "b"})
	call("get_finding", idArgs{ID: "f_1"})
	call("dismiss", idArgs{ID: "f_1", Reason: "not real"})

	if dismissed != "f_1" {
		t.Errorf("dismiss did not reach the backend; got %q", dismissed)
	}
}
