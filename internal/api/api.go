// Package api is the read+write JSON admin API for the `buggies serve` daemon.
// It is grounded in the real on-disk stores: the global queue.db (jobs) and
// budget.db (spend) at the data root, plus a per-repo findings.db under each
// owner__repo directory. Repos and installations are not a table of their own;
// they are derived from distinct jobs.repo and the InstallationID carried in
// each job's PRJob payload, joined with the on-disk per-repo dirs.
//
// Auth stance (localhost self-hosted admin): a single shared bearer token from
// BUGGIES_ADMIN_TOKEN. New fails closed — when the token is empty the caller
// must not mount the API at all, so the only exposed surface stays the public
// /webhook + /healthz. Every request is compared with crypto/subtle, the same
// constant-time pattern as webhook.VerifySignature. The integrator additionally
// binds this handler to loopback (127.0.0.1), separate from the public
// BUGGIES_ADDR webhook listener, so the token is a second factor, not the only
// wall. No secret (webhook secret, App private key, store key, installation
// tokens) is ever read back out.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/budget"
	"github.com/joshblazer/buggies/internal/queue"
	"github.com/joshblazer/buggies/internal/store"
)

// JobStore is the queue read/write surface the API needs. *queue.Queue
// satisfies it.
type JobStore interface {
	Count(ctx context.Context, st queue.State) (int, error)
	List(ctx context.Context, f queue.Filter) ([]queue.Record, int, error)
	GetByID(ctx context.Context, id int64) (queue.Record, error)
	Requeue(ctx context.Context, id int64) error
	Repos(ctx context.Context) ([]string, error)
}

// FindingStore is one repo's finding store. *store.Store satisfies it.
type FindingStore interface {
	List(ctx context.Context, q finding.Query) ([]finding.Finding, error)
	Get(ctx context.Context, id string) (finding.Finding, error)
	Transition(ctx context.Context, id string, to finding.State, reason string) error
	ListTransitions(ctx context.Context, id string) ([]store.Transition, error)
}

// Catalog enumerates and opens the per-repo finding stores. DirCatalog is the
// concrete filesystem implementation; tests use a fake.
type Catalog interface {
	// Repos lists repos (owner/repo) that have an on-disk finding store.
	Repos() ([]string, error)
	// Open opens the finding store for repo. The returned closer is always safe
	// to call and must be called by the caller when done.
	Open(repo string) (FindingStore, func() error, error)
}

// BudgetLedger is the spend surface. *budget.Ledger satisfies it.
type BudgetLedger interface {
	Spent(ctx context.Context, repo string, at time.Time) (int, error)
	Allow(ctx context.Context, repo string, p budget.Policy, now time.Time) (allow bool, note string, err error)
}

// AdminSettings is the mutable slice of daemon config this API owns. Only
// BudgetCents feeds live behaviour today (budget.Policy.MonthlyLimitCents, read
// per-call by the Reviewer.Budget closure); MinConfidence and PostAdvisory are
// persisted admin preferences the UI round-trips but serve.go does not yet
// consume. No secret is ever part of this.
type AdminSettings struct {
	BudgetCents   int
	MinConfidence finding.Confidence
	PostAdvisory  bool
}

// SettingsStore persists AdminSettings across restarts. FileSettings is the
// concrete implementation (a JSON file under the data root).
type SettingsStore interface {
	Get() (AdminSettings, error)
	Set(AdminSettings) error
}

// Config is the read-only daemon configuration surfaced on GET /api/settings.
// It holds no secret values — only booleans reporting whether each secret is
// configured.
type Config struct {
	Addr             string
	IntervalSeconds  int
	GitHubHost       string
	GitHubAppID      int64
	SkillsEnabled    bool
	AllowRunc        bool
	DataRoot         string
	KernelVersion    string
	HasWebhookSecret bool
	HasAppKey        bool
	HasStoreKey      bool
}

// Deps are the injected dependencies. All are interfaces so handlers are
// testable without SQLite, the filesystem, or GitHub.
type Deps struct {
	Jobs     JobStore
	Ledger   BudgetLedger
	Catalog  Catalog
	Settings SettingsStore
	Config   Config
	// Now defaults to time.Now when nil.
	Now func() time.Time
}

// compile-time assertions that the production types satisfy the API's
// injected interfaces, so a signature drift in queue/budget is caught here.
var (
	_ JobStore     = (*queue.Queue)(nil)
	_ BudgetLedger = (*budget.Ledger)(nil)
)

type api struct {
	d     Deps
	token string
}

// New builds the admin API handler, mounted under /api, guarded by a bearer
// token. The caller MUST NOT mount it when token is empty (fail closed): pass a
// non-empty BUGGIES_ADMIN_TOKEN or leave the whole admin surface unmounted.
func New(d Deps, token string) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	a := &api{d: d, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", a.handleOverview)
	mux.HandleFunc("GET /api/jobs", a.handleJobs)
	mux.HandleFunc("GET /api/jobs/{id}", a.handleJob)
	mux.HandleFunc("POST /api/jobs/{id}/requeue", a.handleRequeue)
	mux.HandleFunc("GET /api/installations", a.handleInstallations)
	mux.HandleFunc("GET /api/repos", a.handleRepos)
	mux.HandleFunc("GET /api/findings", a.handleFindings)
	mux.HandleFunc("GET /api/findings/{id}", a.handleFinding)
	mux.HandleFunc("GET /api/findings/{id}/history", a.handleHistory)
	mux.HandleFunc("POST /api/findings/{id}/transition", a.handleTransition)
	mux.HandleFunc("GET /api/budget", a.handleBudget)
	mux.HandleFunc("GET /api/settings", a.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", a.handlePutSettings)
	return a.auth(mux)
}

// auth enforces the bearer token on every request with a constant-time compare,
// the same pattern as webhook.VerifySignature. The token rides in a header, not
// a cookie, so there is no CSRF surface.
func (a *api) auth(next http.Handler) http.Handler {
	want := []byte(a.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			a.writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) now() time.Time { return a.d.Now().UTC() }

func (a *api) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (a *api) writeError(w http.ResponseWriter, code int, msg string) {
	a.writeJSON(w, code, map[string]string{"error": msg})
}

// allRepos is the union of repos the queue has seen and repos with an on-disk
// finding store, deduped and sorted. The dashboard shows both: a repo may have
// findings on disk from a prior run with no job row left, and a just-queued
// repo may have no findings yet.
func (a *api) allRepos(ctx context.Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	if a.d.Jobs != nil {
		if repos, err := a.d.Jobs.Repos(ctx); err == nil {
			for _, r := range repos {
				add(r)
			}
		}
	}
	if a.d.Catalog != nil {
		if repos, err := a.d.Catalog.Repos(); err == nil {
			for _, r := range repos {
				add(r)
			}
		}
	}
	sort.Strings(out)
	return out
}

// withStore opens repo's finding store, runs fn, and always closes it.
func (a *api) withStore(repo string, fn func(FindingStore) error) error {
	fs, closer, err := a.d.Catalog.Open(repo)
	if err != nil {
		return err
	}
	defer closer()
	return fn(fs)
}
