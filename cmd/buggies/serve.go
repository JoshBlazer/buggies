package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/joshblazer/kernel/finding"
	gsqlite "github.com/joshblazer/kernel/graph/sqlite"
	"github.com/joshblazer/kernel/sandbox"
	"github.com/joshblazer/kernel/sandbox/adapter"
	"github.com/joshblazer/kernel/sandbox/adapter/node"
	"github.com/joshblazer/kernel/sandbox/runtime"

	"github.com/joshblazer/buggies/internal/agents"
	"github.com/joshblazer/buggies/internal/agents/skills"
	"github.com/joshblazer/buggies/internal/budget"
	"github.com/joshblazer/buggies/internal/checkout"
	"github.com/joshblazer/buggies/internal/claudecli"
	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/pipeline"
	"github.com/joshblazer/buggies/internal/policy"
	"github.com/joshblazer/buggies/internal/queue"
	"github.com/joshblazer/buggies/internal/security"
	"github.com/joshblazer/buggies/internal/store"
	"github.com/joshblazer/buggies/internal/surfaces/checkrun"
	"github.com/joshblazer/buggies/internal/unattended"
)

// serveCmd runs Buggies unattended: a GitHub App webhook receiver feeds a
// durable queue; a worker drains it, reviews each PR head once (idempotent),
// and posts a review plus a CI check run. Fork PRs are reviewed with secrets
// stripped from the model subprocess. Per-repo spend is capped by budget.
func serveCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", envOr("BUGGIES_ADDR", ":8080"), "listen address")
	interval := fs.Duration("interval", 15*time.Second, "worker poll interval")
	if err := fs.Parse(args); err != nil {
		return err
	}

	secret := os.Getenv("BUGGIES_WEBHOOK_SECRET")
	if secret == "" {
		return fmt.Errorf("BUGGIES_WEBHOOK_SECRET is required")
	}
	appID, err := strconv.ParseInt(os.Getenv("BUGGIES_GITHUB_APP_ID"), 10, 64)
	if err != nil || appID == 0 {
		return fmt.Errorf("BUGGIES_GITHUB_APP_ID must be the App's numeric id")
	}
	keyPath := os.Getenv("BUGGIES_GITHUB_APP_KEY")
	if keyPath == "" {
		return fmt.Errorf("BUGGIES_GITHUB_APP_KEY (path to the App private key PEM) is required")
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read app key: %w", err)
	}
	host := envOr("BUGGIES_GITHUB_HOST", "github.com")
	apiBase := ""
	if host != "github.com" {
		apiBase = "https://" + host + "/api/v3" // GitHub Enterprise
	}
	var budgetCents int
	if v := os.Getenv("BUGGIES_BUDGET_CENTS"); v != "" {
		if budgetCents, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("BUGGIES_BUDGET_CENTS: %w", err)
		}
	}

	root, err := dataRoot()
	if err != nil {
		return err
	}
	q, err := queue.Open(filepath.Join(root, "queue.db"))
	if err != nil {
		return err
	}
	defer q.Close()
	ledger, err := budget.Open(filepath.Join(root, "budget.db"))
	if err != nil {
		return err
	}
	defer ledger.Close()

	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "buggies serve: "+format+"\n", a...) }
	appCfg := func(installID int64) github.AppConfig {
		return github.AppConfig{AppID: appID, InstallationID: installID, PrivateKeyPEM: keyPEM}
	}

	r := &unattended.Reviewer{
		Q:    q,
		Logf: logf,
		Budget: func(ctx context.Context, job github.PRJob) (bool, string, error) {
			return ledger.Allow(ctx, job.Owner+"/"+job.Repo, budget.Policy{MonthlyLimitCents: budgetCents}, time.Now())
		},
		Review: func(ctx context.Context, job github.PRJob) ([]finding.Finding, error) {
			name := job.Owner + "/" + job.Repo
			dd, err := dataDir(name)
			if err != nil {
				return nil, err
			}
			tok, err := appCfg(job.InstallationID).InstallationToken(ctx)
			if err != nil {
				return nil, fmt.Errorf("installation token: %w", err)
			}
			repoURL := fmt.Sprintf("https://x-access-token:%s@%s/%s.git", tok, host, name)
			wsDir := filepath.Join(dd, "checkout", job.HeadSHA)
			treeRoot, err := checkout.Materialize(ctx, checkout.Spec{
				RepoURL: repoURL,
				Ref:     fmt.Sprintf("refs/pull/%d/head", job.Number),
				HeadSHA: job.HeadSHA,
				BaseSHA: job.BaseSHA,
				Dir:     wsDir,
			})
			if err != nil {
				return nil, err
			}
			// A fork PR is outsider-controlled code: the model subprocess runs
			// with Buggies' secrets stripped from its environment.
			env := security.ForkSafeEnv(os.Environ(), job.Fork)

			callsBefore, costBefore := claudecli.Usage()
			fs, err := serveReview(ctx, name, treeRoot, dd, job.BaseSHA, job.HeadSHA, env)
			if err != nil {
				return nil, err
			}
			if _, costAfter := claudecli.Usage(); costAfter > costBefore {
				cents := int(math.Round((costAfter - costBefore) * 100))
				if err := ledger.Add(ctx, name, cents, time.Now()); err != nil {
					logf("warn: record spend for %s: %v", name, err)
				}
			}
			calls, _ := claudecli.Usage()
			logf("reviewed %s #%d @ %s: %d finding(s), %d model call(s) total", name, job.Number, short(job.HeadSHA), len(fs), calls-callsBefore)
			return fs, nil
		},
		Post: func(ctx context.Context, job github.PRJob, fs []finding.Finding) error {
			cl, err := github.NewInstallationClient(appCfg(job.InstallationID))
			if err != nil {
				return err
			}
			cl.BaseURL = apiBase
			pr := github.PR{Owner: job.Owner, Repo: job.Repo, Number: job.Number}
			url, err := unattended.Publish(ctx, cl, pr, job.HeadSHA, fs)
			if err != nil {
				return err
			}
			logf("posted %s #%d: %s", pr.Owner+"/"+pr.Repo, pr.Number, url)
			return nil
		},
		PostSkip: func(ctx context.Context, job github.PRJob, note string) error {
			cl, err := github.NewInstallationClient(appCfg(job.InstallationID))
			if err != nil {
				return err
			}
			cl.BaseURL = apiBase
			pr := github.PR{Owner: job.Owner, Repo: job.Repo, Number: job.Number}
			// Neutral conclusion: the gate is not failed, the review simply did
			// not run. The reason shows on the check run so it is never silent.
			_, err = cl.UpsertCheckRun(ctx, pr, github.CheckRun{
				Name: checkrun.CheckName, HeadSHA: job.HeadSHA, Conclusion: "neutral",
				Title: "skipped", Summary: "Buggies skipped this review: " + note,
			})
			return err
		},
	}

	server := unattended.NewServer([]byte(secret), r)
	go server.Run(ctx, *interval)

	srv := &http.Server{Addr: *addr, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()

	logf("listening on %s (app %d, host %s, budget %d¢/mo)", *addr, appID, host, budgetCents)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// serveReview builds the full pipeline over a materialized checkout and reviews
// base..head. env is the (possibly secret-scrubbed) environment for the model
// subprocess.
func serveReview(ctx context.Context, name, root, dd, base, head string, env []string) ([]finding.Finding, error) {
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

	ag := &agents.Agents{Router: policy.NewRouterEnv(filepath.Join(dd, "model-cache"), env), Tiers: policy.TriadTiers, MaxCandidates: policy.MaxCandidates}
	if os.Getenv("BUGGIES_SKILLS") != "off" {
		sk, err := skills.Load()
		if err != nil {
			return nil, err
		}
		ag.Skills = sk
	}
	p := &pipeline.Pipeline{
		Repo: name, Root: root, Graph: g, Executor: ex, Adapter: node.Adapter{},
		Agents: ag, Store: st,
		Throttled: func(ctx context.Context, typ finding.Type) (bool, error) {
			d, n, err := st.DismissalRate(ctx, name, typ)
			return policy.DefaultThrottle.Suppress(typ, d, n), err
		},
		Limits: pipeline.DefaultLimits(), Log: os.Stderr,
	}
	res, err := p.Review(ctx, base, head)
	if err != nil {
		return nil, err
	}
	return res.Findings, nil
}

// short abbreviates a commit SHA to 12 characters for log lines.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
