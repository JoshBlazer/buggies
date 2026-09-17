// Command buggies is the CLI. It is deliberately thin: every subcommand is a
// call into internal/pipeline with flags parsed here.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/buggies-dev/kernel/finding"
	gsqlite "github.com/buggies-dev/kernel/graph/sqlite"
	"github.com/buggies-dev/kernel/sandbox"
	"github.com/buggies-dev/kernel/sandbox/adapter"
	"github.com/buggies-dev/kernel/sandbox/adapter/node"
	"github.com/buggies-dev/kernel/sandbox/runtime"

	"github.com/buggies-dev/buggies/internal/agents"
	"github.com/buggies-dev/buggies/internal/integrations/github"
	"github.com/buggies-dev/buggies/internal/pipeline"
	"github.com/buggies-dev/buggies/internal/policy"
	"github.com/buggies-dev/buggies/internal/store"
	"github.com/buggies-dev/buggies/internal/surfaces/prcomment"
)

const usage = `buggies — proof, not opinion

usage:
  buggies review --repo <path> --name owner/repo --base <rev> --head <rev> [--post owner/repo#N] [--json]
  buggies index  --repo <path> --name owner/repo
  buggies version

Only reproduced findings are shown as findings. Everything else is discarded.

environment:
  ANTHROPIC_API_KEY        model access (or an 'ant auth login' profile)
  GITHUB_TOKEN             required for --post
  BUGGIES_DATA             data directory (default ~/.buggies)
  BUGGIES_ALLOW_RUNC=1     development only: run the sandbox on runc when gVisor is absent
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "version":
		fmt.Println("buggies v0.0.0-dev (kernel " + pipeline.KernelVersion + ")")
	case "review":
		err = review(ctx, os.Args[2:])
	case "index":
		err = index(ctx, os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "buggies:", err)
		os.Exit(1)
	}
}

func dataDir(name string) (string, error) {
	d := os.Getenv("BUGGIES_DATA")
	if d == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(home, ".buggies")
	}
	d = filepath.Join(d, strings.ReplaceAll(name, "/", "__"))
	return d, os.MkdirAll(d, 0o700)
}

func index(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
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
	g, err := gsqlite.Open(filepath.Join(dd, "graph.db"), *name)
	if err != nil {
		return err
	}
	defer g.Close()
	if err := g.Index(ctx, root, nil); err != nil {
		return err
	}
	fmt.Println("indexed", root, "→", filepath.Join(dd, "graph.db"))
	return nil
}

func review(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	repo := fs.String("repo", ".", "path to the checkout (must have --head checked out)")
	name := fs.String("name", "", "repository name, owner/repo")
	base := fs.String("base", "", "base revision")
	head := fs.String("head", "HEAD", "head revision")
	post := fs.String("post", "", "post the review to this PR, owner/repo#N (needs GITHUB_TOKEN)")
	asJSON := fs.Bool("json", false, "print findings as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *base == "" {
		return fmt.Errorf("--name and --base are required")
	}
	root, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	dd, err := dataDir(*name)
	if err != nil {
		return err
	}

	g, err := gsqlite.Open(filepath.Join(dd, "graph.db"), *name)
	if err != nil {
		return err
	}
	defer g.Close()
	st, err := store.Open(filepath.Join(dd, "findings.db"))
	if err != nil {
		return err
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
		return err
	}

	rt := policy.NewRouter(filepath.Join(dd, "model-cache"))
	p := &pipeline.Pipeline{
		Repo:     *name,
		Root:     root,
		Graph:    g,
		Executor: ex,
		Adapter:  node.Adapter{},
		Agents:   &agents.Agents{Router: rt, Tiers: policy.TriadTiers, MaxCandidates: policy.MaxCandidates},
		Store:    st,
		Throttled: func(ctx context.Context, typ finding.Type) (bool, error) {
			d, n, err := st.DismissalRate(ctx, *name, typ)
			return policy.DefaultThrottle.Suppress(typ, d, n), err
		},
		Limits: pipeline.DefaultLimits(),
		Log:    os.Stderr,
	}

	res, err := p.Review(ctx, *base, *head)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "review finished in %s: %d reproduced finding(s), %d throttled, %d candidate(s) discarded\n",
		res.Elapsed.Round(1e9), len(res.Findings), len(res.Throttled), len(res.Traces)-len(res.Findings)-len(res.Throttled))

	var comments []prcomment.Comment
	for _, f := range res.Findings {
		comments = append(comments, prcomment.Render(f))
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res.Findings); err != nil {
			return err
		}
	} else {
		fmt.Println(prcomment.Summary(res.Findings))
		for _, c := range comments {
			fmt.Printf("\n--- %s:%d ---\n%s\n", c.Path, c.Line, c.Body)
		}
	}

	if *post != "" {
		pr, err := github.ParsePR(*post)
		if err != nil {
			return err
		}
		gh, err := github.FromEnv()
		if err != nil {
			return err
		}
		headSHA, _, err := gh.Head(ctx, pr)
		if err != nil {
			return err
		}
		url, err := gh.PostReview(ctx, pr, headSHA, prcomment.Summary(res.Findings), comments)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "posted:", url)
	}
	return nil
}
