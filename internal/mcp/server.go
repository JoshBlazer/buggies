// Package mcp exposes Buggies as tools over the Model Context Protocol, so
// Claude Code and other agents can call it on the current branch before opening
// a pull request. The pipeline wiring is injected (Deps), so the protocol layer
// is testable without a repo, graph or models.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/joshblazer/kernel/finding"
)

// Deps are the Buggies operations the tools expose. The CLI wires these to the
// real graph/store/pipeline; tests supply stubs.
type Deps struct {
	BlastRadius func(ctx context.Context, repo, base, head string) (affected []string, err error)
	Review      func(ctx context.Context, repo, base, head string) ([]finding.Finding, error)
	GetFinding  func(ctx context.Context, id string) (finding.Finding, error)
	Dismiss     func(ctx context.Context, id, reason string) error
}

// ---- tool argument/result shapes ----

type diffArgs struct {
	Repo string `json:"repo" jsonschema:"path or name of the repository checkout"`
	Base string `json:"base" jsonschema:"base revision"`
	Head string `json:"head" jsonschema:"head revision"`
}

type blastRadiusResult struct {
	Affected []string `json:"affected"`
}

// findingDTO is a flat, string-friendly view of a finding for agents — no
// []byte fields, so the generated output schema matches the JSON.
type findingDTO struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Confidence   string `json:"confidence"`
	Impact       string `json:"impact"`
	State        string `json:"state"`
	Claim        string `json:"claim"`
	Path         string `json:"path"`
	Line         int    `json:"line"`
	Reproduction string `json:"reproduction,omitempty"` // the failing test
	SuggestedFix string `json:"suggested_fix,omitempty"`
}

func toDTO(f finding.Finding) findingDTO {
	d := findingDTO{
		ID: f.ID, Type: string(f.Type), Confidence: f.Confidence.String(),
		Impact: f.Impact.String(), State: string(f.State), Claim: f.Claim,
		Path: f.Location.Path, Line: f.Location.Line,
	}
	if f.Proof != nil {
		d.Reproduction = string(f.Proof.Test.Content)
		d.SuggestedFix = f.Proof.SuggestedFix
	}
	return d
}

type reviewResult struct {
	Findings []findingDTO `json:"findings"`
	Count    int          `json:"count"`
}

type idArgs struct {
	ID     string `json:"id" jsonschema:"the stable finding id"`
	Reason string `json:"reason,omitempty" jsonschema:"why it is dismissed (dismiss only)"`
}

type getFindingResult struct {
	Finding findingDTO `json:"finding"`
}

type dismissResult struct {
	ID        string `json:"id"`
	Dismissed bool   `json:"dismissed"`
}

// NewServer builds the MCP server with Buggies' four tools registered.
func NewServer(d Deps) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "buggies", Version: "0.1.0"}, nil)

	sdk.AddTool(s, &sdk.Tool{
		Name:        "blast_radius",
		Description: "Return the code a diff can affect (callers and dependents), so a hunt is scoped to the relevant subgraph.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in diffArgs) (*sdk.CallToolResult, blastRadiusResult, error) {
		aff, err := d.BlastRadius(ctx, in.Repo, in.Base, in.Head)
		if err != nil {
			return nil, blastRadiusResult{}, err
		}
		return nil, blastRadiusResult{Affected: aff}, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "review",
		Description: "Review a diff and return only findings reproduced in the sandbox (true by construction).",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in diffArgs) (*sdk.CallToolResult, reviewResult, error) {
		fs, err := d.Review(ctx, in.Repo, in.Base, in.Head)
		if err != nil {
			return nil, reviewResult{}, err
		}
		dtos := make([]findingDTO, 0, len(fs))
		for _, f := range fs {
			dtos = append(dtos, toDTO(f))
		}
		return nil, reviewResult{Findings: dtos, Count: len(dtos)}, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "get_finding",
		Description: "Fetch a finding and its reproduction (the failing test) by stable id.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in idArgs) (*sdk.CallToolResult, getFindingResult, error) {
		if in.ID == "" {
			return nil, getFindingResult{}, fmt.Errorf("id is required")
		}
		f, err := d.GetFinding(ctx, in.ID)
		if err != nil {
			return nil, getFindingResult{}, err
		}
		return nil, getFindingResult{Finding: toDTO(f)}, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "dismiss",
		Description: "Dismiss a finding by stable id; it stops being reported and feeds calibration.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in idArgs) (*sdk.CallToolResult, dismissResult, error) {
		if in.ID == "" {
			return nil, dismissResult{}, fmt.Errorf("id is required")
		}
		if err := d.Dismiss(ctx, in.ID, in.Reason); err != nil {
			return nil, dismissResult{ID: in.ID}, err
		}
		return nil, dismissResult{ID: in.ID, Dismissed: true}, nil
	})

	return s
}

// Serve runs the server over stdio until the context is cancelled.
func Serve(ctx context.Context, d Deps) error {
	return NewServer(d).Run(ctx, &sdk.StdioTransport{})
}
