package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joshblazer/kernel/finding"
	"github.com/joshblazer/kernel/sandbox"

	"github.com/joshblazer/buggies/internal/budget"
	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
	"github.com/joshblazer/buggies/internal/store"
)

const testToken = "s3cret-admin-token"

// ---- fakes ------------------------------------------------------------------

type fakeJobs struct {
	recs []queue.Record
	// requeueErr, keyed by id, lets a test force ErrJobRunning/ErrNotFound.
	requeueErr map[int64]error
	requeued   []int64
}

func (f *fakeJobs) Count(_ context.Context, st queue.State) (int, error) {
	n := 0
	for _, r := range f.recs {
		if r.State == st {
			n++
		}
	}
	return n, nil
}

func (f *fakeJobs) List(_ context.Context, flt queue.Filter) ([]queue.Record, int, error) {
	var out []queue.Record
	for _, r := range f.recs {
		if flt.State != "" && r.State != flt.State {
			continue
		}
		if flt.Repo != "" && r.Repo != flt.Repo {
			continue
		}
		out = append(out, r)
	}
	total := len(out)
	if flt.Offset > 0 {
		if flt.Offset >= len(out) {
			out = nil
		} else {
			out = out[flt.Offset:]
		}
	}
	if flt.Limit > 0 && len(out) > flt.Limit {
		out = out[:flt.Limit]
	}
	return out, total, nil
}

func (f *fakeJobs) GetByID(_ context.Context, id int64) (queue.Record, error) {
	for _, r := range f.recs {
		if r.ID == id {
			return r, nil
		}
	}
	return queue.Record{}, queue.ErrNotFound
}

func (f *fakeJobs) Requeue(_ context.Context, id int64) error {
	if err := f.requeueErr[id]; err != nil {
		return err
	}
	found := false
	for _, r := range f.recs {
		if r.ID == id {
			found = true
		}
	}
	if !found {
		return queue.ErrNotFound
	}
	f.requeued = append(f.requeued, id)
	return nil
}

func (f *fakeJobs) Repos(_ context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, r := range f.recs {
		if !seen[r.Repo] {
			seen[r.Repo] = true
			out = append(out, r.Repo)
		}
	}
	return out, nil
}

type fakeStore struct {
	findings    map[string]finding.Finding
	transitions map[string][]store.Transition
	transErr    error
}

func (s *fakeStore) List(_ context.Context, q finding.Query) ([]finding.Finding, error) {
	var out []finding.Finding
	for _, f := range s.findings {
		if q.Repo != "" && f.Location.Repo != q.Repo {
			continue
		}
		if q.Type != "" && f.Type != q.Type {
			continue
		}
		if q.State != "" && f.State != q.State {
			continue
		}
		if q.MinConfidence > 0 && f.Confidence < q.MinConfidence {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *fakeStore) Get(_ context.Context, id string) (finding.Finding, error) {
	f, ok := s.findings[id]
	if !ok {
		return finding.Finding{}, finding.ErrNotFound
	}
	return f, nil
}

func (s *fakeStore) Transition(_ context.Context, id string, to finding.State, reason string) error {
	if s.transErr != nil {
		return s.transErr
	}
	f, ok := s.findings[id]
	if !ok {
		return finding.ErrNotFound
	}
	from := f.State
	f.State = to
	f.UpdatedAt = time.Now()
	s.findings[id] = f
	if s.transitions == nil {
		s.transitions = map[string][]store.Transition{}
	}
	s.transitions[id] = append(s.transitions[id], store.Transition{From: from, To: to, Reason: reason, At: f.UpdatedAt})
	return nil
}

func (s *fakeStore) ListTransitions(_ context.Context, id string) ([]store.Transition, error) {
	return s.transitions[id], nil
}

type fakeCatalog struct {
	stores map[string]*fakeStore // repo -> store
}

func (c fakeCatalog) Repos() ([]string, error) {
	var out []string
	for r := range c.stores {
		out = append(out, r)
	}
	return out, nil
}

func (c fakeCatalog) Open(repo string) (FindingStore, func() error, error) {
	s, ok := c.stores[repo]
	if !ok {
		return nil, func() error { return nil }, errors.New("no such repo store")
	}
	return s, func() error { return nil }, nil
}

type fakeLedger struct {
	spent map[string]int
	cap   int
}

func (l fakeLedger) Spent(_ context.Context, repo string, _ time.Time) (int, error) {
	return l.spent[repo], nil
}

func (l fakeLedger) Allow(_ context.Context, repo string, p budget.Policy, _ time.Time) (bool, string, error) {
	if p.MonthlyLimitCents <= 0 {
		return true, "", nil
	}
	if l.spent[repo] >= p.MonthlyLimitCents {
		return false, "over budget", nil
	}
	return true, "", nil
}

type fakeSettings struct{ s AdminSettings }

func (f *fakeSettings) Get() (AdminSettings, error) { return f.s, nil }
func (f *fakeSettings) Set(s AdminSettings) error   { f.s = s; return nil }

// ---- fixtures ---------------------------------------------------------------

func jobRecord(id int64, repo string, pj github.PRJob, st queue.State) queue.Record {
	payload, _ := json.Marshal(pj)
	return queue.Record{
		Job:       queue.Job{ID: id, Key: repo + "@" + pj.HeadSHA, Repo: repo, HeadSHA: pj.HeadSHA, Payload: payload, State: st},
		CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 13, 9, 5, 0, 0, time.UTC),
	}
}

func reproducedFinding(id, repo string) finding.Finding {
	return finding.Finding{
		ID: id, Type: finding.Correctness, Confidence: finding.Reproduced, Impact: finding.DataLoss,
		State: finding.Verified, Claim: "gold tier pays full price",
		Location: finding.Location{Repo: repo, Path: "src/discount.ts", Symbol: "applyDiscount", Line: 4},
		Proof: &finding.Proof{
			Test: sandbox.Artifact{Path: "src/__buggies__/discount.test.ts", Content: []byte("expect(80)")},
			Result: sandbox.Result{Outcome: sandbox.Failed, Reproducible: true, Runs: []sandbox.Evidence{
				{StackTrace: "AssertionError: 80 !== 100", ExitCode: 1, Timing: 1500 * time.Millisecond, Image: "node:20-bookworm"},
			}},
			SuggestedFix: "--- a\n+++ b\n",
		},
		CreatedAt: time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC),
	}
}

func newTestAPI() (http.Handler, *fakeJobs, *fakeCatalog, *fakeSettings) {
	pj := github.PRJob{Owner: "acme", Repo: "shop", Number: 7, HeadSHA: "deadbeef", BaseSHA: "cafe", InstallationID: 42}
	jobs := &fakeJobs{
		recs: []queue.Record{
			jobRecord(3, "acme/shop", pj, queue.Done),
			jobRecord(2, "acme/shop", github.PRJob{Owner: "acme", Repo: "shop", Number: 6, HeadSHA: "feed", InstallationID: 42}, queue.Running),
			jobRecord(1, "acme/widgets", github.PRJob{Owner: "acme", Repo: "widgets", Number: 1, HeadSHA: "beef", InstallationID: 99}, queue.Failed),
		},
		requeueErr: map[int64]error{2: queue.ErrJobRunning},
	}
	cat := &fakeCatalog{stores: map[string]*fakeStore{
		"acme/shop": {findings: map[string]finding.Finding{
			"f_blocking": reproducedFinding("f_blocking", "acme/shop"),
		}},
		"acme/widgets": {findings: map[string]finding.Finding{}},
	}}
	settings := &fakeSettings{s: AdminSettings{BudgetCents: 5000, MinConfidence: finding.Reproduced, PostAdvisory: true}}
	led := fakeLedger{spent: map[string]int{"acme/shop": 1200}, cap: 5000}
	d := Deps{
		Jobs: jobs, Ledger: led, Catalog: cat, Settings: settings,
		Config: Config{GitHubHost: "github.com", IntervalSeconds: 15, SkillsEnabled: true, HasWebhookSecret: true},
		Now:    func() time.Time { return time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC) },
	}
	return New(d, testToken), jobs, cat, settings
}

func do(t *testing.T, h http.Handler, method, target string, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if auth {
		r.Header.Set("Authorization", "Bearer "+testToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v (body: %s)", v, err, w.Body.String())
	}
	return v
}

// ---- auth -------------------------------------------------------------------

func TestAuth(t *testing.T) {
	h, _, _, _ := newTestAPI()
	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"no bearer prefix", testToken, http.StatusUnauthorized},
		{"correct", "Bearer " + testToken, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/overview", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestEmptyTokenRejectsEverything(t *testing.T) {
	// New with an empty token must never authorize a request (fail closed); the
	// caller is responsible for not mounting it at all.
	h := New(Deps{}, "")
	w := do(t, h, "GET", "/api/overview", "", true)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty-token API authorized a request: status %d", w.Code)
	}
}

// ---- read endpoints ---------------------------------------------------------

func TestOverview(t *testing.T) {
	h, _, _, _ := newTestAPI()
	w := do(t, h, "GET", "/api/overview", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	ov := decode[overviewDTO](t, w)
	if ov.Repos != 2 {
		t.Errorf("repos = %d, want 2", ov.Repos)
	}
	if ov.Queue.Running != 1 || ov.Queue.Done != 1 || ov.Queue.Failed != 1 {
		t.Errorf("queue depth = %+v, want running/done/failed = 1/1/1", ov.Queue)
	}
	if ov.Findings.Total != 1 {
		t.Errorf("findings.total = %d, want 1", ov.Findings.Total)
	}
	if ov.Findings.Blocking != 1 {
		t.Errorf("findings.blocking = %d, want 1", ov.Findings.Blocking)
	}
	if ov.Findings.ByConfidence["reproduced"] != 1 {
		t.Errorf("findings.byConfidence[reproduced] = %d, want 1", ov.Findings.ByConfidence["reproduced"])
	}
	if ov.SpendCents != 1200 {
		t.Errorf("spendCents = %d, want 1200", ov.SpendCents)
	}
	if ov.BudgetCents != 5000 {
		t.Errorf("budgetCents = %d, want 5000", ov.BudgetCents)
	}
	if ov.Month != "2026-10" {
		t.Errorf("month = %q, want 2026-10", ov.Month)
	}
}

func TestJobsList(t *testing.T) {
	h, _, _, _ := newTestAPI()
	w := do(t, h, "GET", "/api/jobs", "", true)
	pg := decode[jobsPageDTO](t, w)
	if pg.Total != 3 || len(pg.Jobs) != 3 {
		t.Fatalf("got total=%d jobs=%d, want 3/3", pg.Total, len(pg.Jobs))
	}
	// The job state is the raw queue state (contract JobState), not a GUI alias.
	var widgets jobDTO
	for _, j := range pg.Jobs {
		if j.Repo == "acme/widgets" {
			widgets = j
		}
	}
	if widgets.State != "failed" {
		t.Errorf("failed job state = %q, want failed", widgets.State)
	}
	if widgets.ID != 1 {
		t.Errorf("job id = %d, want 1", widgets.ID)
	}

	// Filter by repo.
	w = do(t, h, "GET", "/api/jobs?repo=acme/widgets", "", true)
	pg = decode[jobsPageDTO](t, w)
	if pg.Total != 1 {
		t.Errorf("repo-filtered total = %d, want 1", pg.Total)
	}
}

func TestJobDetail(t *testing.T) {
	h, _, _, _ := newTestAPI()
	w := do(t, h, "GET", "/api/jobs/3", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	jd := decode[jobDetailDTO](t, w)
	if jd.Job.Number != 7 || jd.Job.HeadSHA != "deadbeef" || jd.Job.InstallationID != 42 {
		t.Errorf("unexpected job: %+v", jd.Job)
	}
	if len(jd.Findings) != 1 {
		t.Errorf("findings = %d, want 1", len(jd.Findings))
	}
	if jd.Check.Conclusion != "failure" {
		t.Errorf("check.conclusion = %q, want failure (a blocking finding)", jd.Check.Conclusion)
	}

	// Unknown id -> 404.
	w = do(t, h, "GET", "/api/jobs/999", "", true)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown job status = %d, want 404", w.Code)
	}
	// Non-numeric id -> 400.
	w = do(t, h, "GET", "/api/jobs/abc", "", true)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad job id status = %d, want 400", w.Code)
	}
}

func TestRequeue(t *testing.T) {
	h, jobs, _, _ := newTestAPI()

	// A done job requeues.
	w := do(t, h, "POST", "/api/jobs/3/requeue", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	rq := decode[requeueDTO](t, w)
	if rq.ID != 3 || rq.State != "queued" {
		t.Errorf("unexpected requeue response: %+v", rq)
	}
	if len(jobs.requeued) != 1 || jobs.requeued[0] != 3 {
		t.Errorf("requeue not recorded: %v", jobs.requeued)
	}

	// A running job -> 409.
	w = do(t, h, "POST", "/api/jobs/2/requeue", "", true)
	if w.Code != http.StatusConflict {
		t.Errorf("running job requeue status = %d, want 409", w.Code)
	}

	// Unknown job -> 404.
	w = do(t, h, "POST", "/api/jobs/999/requeue", "", true)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown job requeue status = %d, want 404", w.Code)
	}
}

func TestInstallations(t *testing.T) {
	h, _, _, _ := newTestAPI()
	w := do(t, h, "GET", "/api/installations", "", true)
	pg := decode[pageDTO[installationDTO]](t, w)
	if pg.Total != 2 {
		t.Fatalf("installations = %d, want 2", pg.Total)
	}
	for _, inst := range pg.Items {
		if inst.Host != "github.com" {
			t.Errorf("host = %q, want github.com", inst.Host)
		}
		if inst.ID == 42 {
			if inst.Account != "acme" || inst.RepoCount != 1 {
				t.Errorf("install 42: account=%q repoCount=%d", inst.Account, inst.RepoCount)
			}
		}
	}
}

func TestFindingsList(t *testing.T) {
	h, _, _, _ := newTestAPI()

	// Repo-scoped.
	w := do(t, h, "GET", "/api/findings?repo=acme/shop", "", true)
	resp := decode[findingsResponseDTO](t, w)
	if resp.Repo != "acme/shop" || len(resp.Findings) != 1 {
		t.Fatalf("repo findings resp = %+v, want repo acme/shop with 1 finding", resp)
	}
	f := resp.Findings[0]
	if f.Confidence != "reproduced" || f.Impact != "data-loss" || !f.Blocking || !f.Reproduced {
		t.Errorf("unexpected finding summary: %+v", f)
	}
	if f.Path != "src/discount.ts" || f.Symbol != "applyDiscount" {
		t.Errorf("flat location not mapped: %+v", f)
	}

	// Aggregate across all repos (no repo param).
	w = do(t, h, "GET", "/api/findings", "", true)
	resp = decode[findingsResponseDTO](t, w)
	if len(resp.Findings) != 1 {
		t.Errorf("aggregate findings = %d, want 1", len(resp.Findings))
	}

	// minConfidence filter above the finding's confidence yields none... but the
	// finding IS reproduced (max), so filtering at reproduced keeps it.
	w = do(t, h, "GET", "/api/findings?repo=acme/shop&minConfidence=reproduced", "", true)
	resp = decode[findingsResponseDTO](t, w)
	if len(resp.Findings) != 1 {
		t.Errorf("minConfidence=reproduced findings = %d, want 1", len(resp.Findings))
	}

	// Bad confidence -> 400.
	w = do(t, h, "GET", "/api/findings?repo=acme/shop&confidence=bogus", "", true)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad confidence status = %d, want 400", w.Code)
	}
}

func TestFindingDetailAndResolveWithoutRepo(t *testing.T) {
	h, _, _, _ := newTestAPI()
	// No repo param: the API scans stores to locate the id.
	w := do(t, h, "GET", "/api/findings/f_blocking", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	f := decode[findingDetailDTO](t, w)
	if f.ID != "f_blocking" || f.Location.Repo != "acme/shop" {
		t.Errorf("unexpected finding: %+v", f)
	}
	if f.Proof == nil || len(f.Proof.Result.Runs) != 1 {
		t.Fatalf("proof not mapped: %+v", f.Proof)
	}
	if f.Proof.Result.Runs[0].Image != "node:20-bookworm" || f.Proof.Result.Runs[0].TimingMs != 1500 {
		t.Errorf("proof run not mapped: %+v", f.Proof.Result.Runs[0])
	}
	if !f.Proof.Result.Reproducible || f.Proof.Test.Path != "src/__buggies__/discount.test.ts" {
		t.Errorf("proof result/test not mapped: %+v", f.Proof)
	}
	if f.RenderedMarkdown == "" {
		t.Errorf("renderedMarkdown is empty")
	}

	// Unknown id -> 404.
	w = do(t, h, "GET", "/api/findings/f_nope", "", true)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown finding status = %d, want 404", w.Code)
	}
}

func TestTransition(t *testing.T) {
	h, _, cat, _ := newTestAPI()
	tests := []struct {
		name string
		body string
		want int
	}{
		{"valid dismiss", `{"to":"dismissed","reason":"false positive"}`, http.StatusOK},
		{"valid with repo", `{"repo":"acme/shop","to":"fixed","reason":"patched"}`, http.StatusOK},
		{"invalid state", `{"to":"banana","reason":"x"}`, http.StatusBadRequest},
		{"malformed body", `{`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// reset the store finding each case
			cat.stores["acme/shop"].findings["f_blocking"] = reproducedFinding("f_blocking", "acme/shop")
			w := do(t, h, "POST", "/api/findings/f_blocking/transition", tc.body, true)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				r := decode[transitionResultDTO](t, w)
				if r.ID != "f_blocking" || r.State == "verified" || r.State == "" {
					t.Errorf("state not updated: %+v", r)
				}
			}
		})
	}

	// Unknown finding -> 404.
	w := do(t, h, "POST", "/api/findings/f_nope/transition", `{"to":"dismissed","reason":"x"}`, true)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown finding transition status = %d, want 404", w.Code)
	}
}

func TestHistory(t *testing.T) {
	h, _, cat, _ := newTestAPI()
	// Seed a transition.
	_ = cat.stores["acme/shop"].Transition(context.Background(), "f_blocking", finding.Dismissed, "nope")
	w := do(t, h, "GET", "/api/findings/f_blocking/history?repo=acme/shop", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		ID          string          `json:"id"`
		Transitions []transitionDTO `json:"transitions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Transitions) != 1 || resp.Transitions[0].To != "dismissed" {
		t.Errorf("unexpected history: %+v", resp.Transitions)
	}
}

func TestBudget(t *testing.T) {
	h, _, _, _ := newTestAPI()
	w := do(t, h, "GET", "/api/budget", "", true)
	bs := decode[budgetDTO](t, w)
	if bs.Month != "2026-10" {
		t.Errorf("month = %q, want 2026-10", bs.Month)
	}
	if bs.BudgetCents != 5000 {
		t.Errorf("budgetCents = %d, want 5000", bs.BudgetCents)
	}
	var shop budgetRepoDTO
	found := false
	for _, r := range bs.Repos {
		if r.Repo == "acme/shop" {
			shop = r
			found = true
		}
	}
	if !found {
		t.Fatalf("acme/shop not in budget rows: %+v", bs.Repos)
	}
	if shop.SpentCents != 1200 || !shop.Allowed {
		t.Errorf("acme/shop budget row: %+v (want spent 1200, allowed true)", shop)
	}
}

func TestSettingsGetAndPut(t *testing.T) {
	h, _, _, settings := newTestAPI()

	w := do(t, h, "GET", "/api/settings", "", true)
	s := decode[settingsDTO](t, w)
	if s.Editable.BudgetCents != 5000 {
		t.Errorf("editable.budgetCents = %d, want 5000", s.Editable.BudgetCents)
	}
	if s.ReadOnly.GithubHost != "github.com" || s.ReadOnly.IntervalSeconds != 15 || !s.ReadOnly.SkillsEnabled {
		t.Errorf("read-only config not reflected: %+v", s.ReadOnly)
	}
	if !s.HasWebhookSecret {
		t.Errorf("hasWebhookSecret = false, want true")
	}

	// Valid PUT updates the one editable field; extra keys are ignored.
	w = do(t, h, "PUT", "/api/settings", `{"budgetCents":8000,"githubHost":"ignored.example"}`, true)
	if w.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", w.Code, w.Body.String())
	}
	upd := decode[budgetUpdateDTO](t, w)
	if upd.BudgetCents != 8000 {
		t.Errorf("put response budgetCents = %d, want 8000", upd.BudgetCents)
	}
	if settings.s.BudgetCents != 8000 {
		t.Errorf("budget not persisted: %d", settings.s.BudgetCents)
	}
	// Read-only config is untouched by the PUT.
	w = do(t, h, "GET", "/api/settings", "", true)
	s = decode[settingsDTO](t, w)
	if s.ReadOnly.GithubHost != "github.com" || s.Editable.BudgetCents != 8000 {
		t.Errorf("post-put settings wrong: %+v", s)
	}

	// Negative budget -> 400.
	w = do(t, h, "PUT", "/api/settings", `{"budgetCents":-1}`, true)
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative budget status = %d, want 400", w.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, _, _, _ := newTestAPI()
	// DELETE on a GET-only route: ServeMux returns 405.
	w := do(t, h, "DELETE", "/api/overview", "", true)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestErrorShapeIsJSON(t *testing.T) {
	h, _, _, _ := newTestAPI()
	w := do(t, h, "GET", "/api/jobs/999", "", true)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body not JSON: %s", w.Body.String())
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("error body missing 'error' key: %v", body)
	}
}
