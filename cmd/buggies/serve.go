package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joshblazer/kernel/finding"
	gsqlite "github.com/joshblazer/kernel/graph/sqlite"
	"github.com/joshblazer/kernel/sandbox"
	"github.com/joshblazer/kernel/sandbox/adapter"
	"github.com/joshblazer/kernel/sandbox/adapter/node"
	"github.com/joshblazer/kernel/sandbox/adapter/python"
	"github.com/joshblazer/kernel/sandbox/runtime"

	"github.com/joshblazer/buggies/internal/agents"
	"github.com/joshblazer/buggies/internal/agents/skills"
	"github.com/joshblazer/buggies/internal/api"
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
				return nil, classifyReviewError(err)
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
		// Data-deletion-on-uninstall: when a tenant removes Buggies from a repo,
		// GitHub stops authorizing it for that code, so Buggies erases what it
		// held — the per-repo state directory (graph, findings, store key,
		// model cache) and every queued/historical job for that repo.
		Uninstall: func(ctx context.Context, repos []string) error {
			for _, name := range repos {
				dir := filepath.Join(root, strings.ReplaceAll(name, "/", "__"))
				if err := os.RemoveAll(dir); err != nil {
					logf("uninstall: purge %s data: %v", name, err)
				}
				if n, err := q.PurgeRepo(ctx, name); err != nil {
					logf("uninstall: purge %s jobs: %v", name, err)
				} else {
					logf("uninstall: purged %s (%d job(s))", name, n)
				}
			}
			return nil
		},
	}

	server := unattended.NewServer([]byte(secret), r)
	// Readiness reflects the real blocker: can the isolation runtime be reached?
	// A review cannot run without it, so /readyz goes red when the sandbox is
	// down — an orchestrator should hold traffic rather than let the daemon post
	// "sandbox unavailable" checks. Liveness (/healthz) stays up regardless.
	server.Ready = func(ctx context.Context) error {
		ex := &runtime.Docker{Egress: &runtime.Egress{}, Log: io.Discard}
		if os.Getenv("BUGGIES_ALLOW_RUNC") == "1" {
			ex.Runtime, ex.AllowRunc = "runc", true
		}
		return ex.CheckRuntime(ctx)
	}
	go server.Run(ctx, *interval)

	// Admin dashboard (JSON API + static SPA) on a SEPARATE loopback listener,
	// never the public webhook address. Fail closed: no BUGGIES_ADMIN_TOKEN,
	// no admin surface at all.
	if adminTok := os.Getenv("BUGGIES_ADMIN_TOKEN"); adminTok != "" {
		adminAddr := envOr("BUGGIES_ADMIN_ADDR", "127.0.0.1:8081")
		webDir := envOr("BUGGIES_WEB_DIR", "web/dist")
		apiHandler := api.New(api.Deps{
			Jobs:     q,
			Ledger:   ledger,
			Catalog:  api.DirCatalog{Root: root},
			Settings: &api.FileSettings{Path: filepath.Join(root, "admin-settings.json"), Defaults: api.AdminSettings{BudgetCents: budgetCents}},
			Config: api.Config{
				Addr:             *addr,
				IntervalSeconds:  int((*interval).Seconds()),
				GitHubHost:       host,
				GitHubAppID:      appID,
				SkillsEnabled:    os.Getenv("BUGGIES_SKILLS") != "off",
				AllowRunc:        os.Getenv("BUGGIES_ALLOW_RUNC") != "",
				DataRoot:         root,
				HasWebhookSecret: secret != "",
				HasAppKey:        len(keyPEM) > 0,
				HasStoreKey:      os.Getenv("BUGGIES_STORE_KEY") != "",
			},
		}, adminTok)
		mux := http.NewServeMux()
		mux.Handle("/api/", apiHandler)
		mux.Handle("/", spaHandler(webDir))
		adminSrv := &http.Server{Addr: adminAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = adminSrv.Shutdown(sh)
		}()
		go func() {
			logf("admin dashboard on http://%s (serving %q)", adminAddr, webDir)
			if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logf("admin server: %v", err)
			}
		}()
	} else {
		logf("admin dashboard disabled (set BUGGIES_ADMIN_TOKEN to enable)")
	}

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

// spaHandler serves the built dashboard from dir, falling back to index.html
// for any path that is not an existing file (client-side routing). The
// filepath.Clean on a rooted path keeps requests from escaping dir.
func spaHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if st, err := os.Stat(filepath.Join(dir, filepath.Clean("/"+r.URL.Path))); err == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFile(w, r, index)
	})
}

// errSandboxUnavailable marks a failure to reach the isolation runtime (the
// sandbox's Docker/gVisor daemon). It is infrastructure, not a problem with the
// PR, so classifyReviewError turns it into a neutral skip note on the check run
// rather than a silent failed job.
var errSandboxUnavailable = errors.New("sandbox unavailable")

// classifyReviewError decides how the worker should treat a serveReview error.
// A sandbox-unavailable or unsupported-ecosystem error is not a transient fault
// to retry and not the PR's fault to fail on: it becomes a neutral *SkipReview,
// which the worker posts to the PR as a non-blocking check-run note so the
// review is never silent. Any other error is returned unchanged, so the job
// fails and surfaces in the dashboard.
func classifyReviewError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errSandboxUnavailable) {
		return &unattended.SkipReview{Note: "sandbox unavailable — Buggies could not reach its isolation runtime, so the review did not run; re-run it once the sandbox is restored"}
	}
	var tooLarge *pipeline.ErrDiffTooLarge
	if errors.As(err, &tooLarge) {
		return &unattended.SkipReview{Note: fmt.Sprintf("diff too large to review (%d files changed, limit %d) — split the PR for a review", tooLarge.Files, tooLarge.Limit)}
	}
	var unsupported *pipeline.ErrUnsupportedProject
	if errors.As(err, &unsupported) {
		note := "no supported ecosystem (Node or Python) detected"
		if unsupported.Ecosystem != "" {
			note = "unsupported ecosystem (" + string(unsupported.Ecosystem) + " only)"
		}
		return &unattended.SkipReview{Note: note}
	}
	return err
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

	// Register an adapter for every supported ecosystem: runtime.Docker.validate
	// rejects a Request whose Ecosystem has no registered adapter, so whichever
	// one serveReview selects below must be present here.
	adapters := map[sandbox.Ecosystem]adapter.Adapter{}
	for _, a := range candidateAdapters() {
		adapters[a.Ecosystem()] = a
	}
	ex := &runtime.Docker{
		Adapters:   adapters,
		DepsCache:  filepath.Join(dd, "deps"),
		Workspaces: filepath.Join(dd, "ws"),
		Egress:     &runtime.Egress{},
		Log:        os.Stderr,
	}
	if os.Getenv("BUGGIES_ALLOW_RUNC") == "1" {
		ex.Runtime, ex.AllowRunc = "runc", true
	}
	if err := ex.CheckRuntime(ctx); err != nil {
		// The isolation runtime (the sandbox's Docker/gVisor daemon) is
		// unreachable. That is infrastructure, not a problem with this PR, so
		// mark it so the Review closure can turn it into a neutral "sandbox
		// unavailable" note on the check run instead of a silent failed job.
		return nil, fmt.Errorf("%w: %v", errSandboxUnavailable, err)
	}

	// Pick the ecosystem by detecting the checked-out tree: the first candidate
	// whose Detect matches wins (Node before Python for polyglot repos). When
	// none match, this is not a retryable failure — return ErrUnsupportedProject
	// with an empty Ecosystem so the Review closure neutral-skips the PR.
	selected, err := selectAdapter(ctx, candidateAdapters(), root, head)
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return nil, &pipeline.ErrUnsupportedProject{Root: root}
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
		Repo: name, Root: root, Graph: g, Executor: ex, Adapter: selected,
		Agents: ag, Store: st,
		Throttled: func(ctx context.Context, typ finding.Type) (bool, error) {
			d, n, err := st.DismissalRate(ctx, name, typ)
			return policy.DefaultThrottle.Suppress(typ, d, n), err
		},
		Limits:          pipeline.DefaultLimits(),
		Log:             os.Stderr,
		MaxChangedFiles: envIntOr("BUGGIES_MAX_CHANGED_FILES", 0),
	}
	res, err := p.Review(ctx, base, head)
	if err != nil {
		return nil, err
	}
	return res.Findings, nil
}

// candidateAdapters is the ordered set of ecosystem adapters serve considers,
// first match wins. Node precedes Python so a polyglot repo (both package.json
// and a Python marker) is reviewed as Node; ordering is the only knob here.
func candidateAdapters() []adapter.Adapter {
	return []adapter.Adapter{node.Adapter{}, python.Adapter{}}
}

// selectAdapter returns the first candidate whose Detect matches the tree at
// root@rev, or nil when none match (the caller then neutral-skips the PR). A
// Detect error is returned as-is: it means detection itself failed, which is
// not the same as "no ecosystem matched".
func selectAdapter(ctx context.Context, candidates []adapter.Adapter, root, rev string) (adapter.Adapter, error) {
	snap := sandbox.Snapshot{Root: root, Revision: rev}
	for _, a := range candidates {
		det, err := a.Detect(ctx, snap)
		if err != nil {
			return nil, err
		}
		if det.Matched {
			return a, nil
		}
	}
	return nil, nil
}

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

// envIntOr reads key as an int, falling back to def when unset or unparseable.
func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
