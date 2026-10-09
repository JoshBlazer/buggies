package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	gsqlite "github.com/joshblazer/kernel/graph/sqlite"
	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/sandbox"
	"github.com/joshblazer/kernel/sandbox/adapter"
	"github.com/joshblazer/kernel/sandbox/adapter/node"
	"github.com/joshblazer/kernel/sandbox/runtime"

	"github.com/joshblazer/buggies/internal/agents"
	"github.com/joshblazer/buggies/internal/agents/skills"
	"github.com/joshblazer/buggies/internal/mcp"
	"github.com/joshblazer/buggies/internal/pipeline"
	"github.com/joshblazer/buggies/internal/policy"
	"github.com/joshblazer/buggies/internal/store"
)

// mcpCmd runs the MCP server over stdio for one repository checkout, exposing
// Buggies' tools (blast radius, review, get finding, dismiss) to agents.
func mcpCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	repo := fs.String("repo", ".", "path to the checkout")
	name := fs.String("name", "", "repository name, owner/repo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	root, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	dd, err := dataDir(*name)
	if err != nil {
		return err
	}

	deps := mcp.Deps{
		BlastRadius: func(ctx context.Context, _, base, head string) ([]string, error) {
			g, err := gsqlite.Open(filepath.Join(dd, "graph.db"), *name)
			if err != nil {
				return nil, err
			}
			defer g.Close()
			br, err := (&pipeline.Pipeline{Repo: *name, Root: root, Graph: g, RadiusDepth: 2}).Scope(ctx, base, head)
			if err != nil {
				return nil, err
			}
			var out []string
			for _, s := range br.Changed {
				out = append(out, s.Name+" ("+s.Path+")")
			}
			for _, a := range br.Affected {
				out = append(out, a.Symbol.Name+" ("+a.Symbol.Path+")")
			}
			return out, nil
		},
		Review: func(ctx context.Context, _, base, head string) ([]finding.Finding, error) {
			return runReview(ctx, *name, root, dd, base, head)
		},
		GetFinding: func(ctx context.Context, id string) (finding.Finding, error) {
			st, err := store.Open(filepath.Join(dd, "findings.db"))
			if err != nil {
				return finding.Finding{}, err
			}
			defer st.Close()
			return st.Get(ctx, id)
		},
		Dismiss: func(ctx context.Context, id, reason string) error {
			st, err := store.Open(filepath.Join(dd, "findings.db"))
			if err != nil {
				return err
			}
			defer st.Close()
			if reason == "" {
				reason = "dismissed via mcp"
			}
			return st.Transition(ctx, id, finding.Dismissed, reason)
		},
	}

	fmt.Fprintln(os.Stderr, "buggies mcp: serving on stdio for", *name)
	return mcp.Serve(ctx, deps)
}

// runReview builds the full pipeline and reviews one diff. Shared shape with
// the `review` command.
func runReview(ctx context.Context, name, root, dd, base, head string) ([]finding.Finding, error) {
	g, err := gsqlite.Open(filepath.Join(dd, "graph.db"), name)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	st, err := store.Open(filepath.Join(dd, "findings.db"))
	if err != nil {
		return nil, err
	}
	defer st.Close()

	ex := &runtime.Docker{
		Adapters:   map[sandbox.Ecosystem]adapter.Adapter{sandbox.Node: node.Adapter{}},
		DepsCache:  filepath.Join(dd, "deps"),
		Workspaces: filepath.Join(dd, "ws"),
		Egress:     &runtime.Egress{},
		Log:        os.Stderr,
	}
	if os.Getenv("BUGGIES_ALLOW_RUNC") == "1" {
		ex.Runtime, ex.AllowRunc = "runc", true
	}
	if err := ex.CheckRuntime(ctx); err != nil {
		return nil, err
	}

	ag := &agents.Agents{Router: policy.NewRouter(filepath.Join(dd, "model-cache")), Tiers: policy.TriadTiers, MaxCandidates: policy.MaxCandidates}
	if os.Getenv("BUGGIES_SKILLS") != "off" {
		sk, err := skills.Load()
		if err != nil {
			return nil, err
		}
		ag.Skills = sk
	}
	p := &pipeline.Pipeline{
		Repo: name, Root: root, Graph: g, Executor: ex, Adapter: node.Adapter{},
		Agents: ag, Store: st, Limits: pipeline.DefaultLimits(), Log: os.Stderr,
	}
	res, err := p.Review(ctx, base, head)
	if err != nil {
		return nil, err
	}
	return res.Findings, nil
}
