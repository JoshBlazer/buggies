package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/budget"
	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
	"github.com/joshblazer/buggies/internal/surfaces/checkrun"
)

// ---- overview ---------------------------------------------------------------

func (a *api) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := a.now()
	repos := a.allRepos(ctx)

	acc := repoTally{byType: map[string]int{}, byState: map[string]int{}, byConf: map[string]int{}}
	tallies := a.repoTallies(ctx, repos)
	spent := 0
	for _, repo := range repos {
		tallies[repo].mergeInto(&acc)
		if s, err := a.d.Ledger.Spent(ctx, repo, now); err == nil {
			spent += s
		}
	}

	// Queue depth by state across the whole queue.
	var qd queueDepthDTO
	if a.d.Jobs != nil {
		qd.Queued, _ = a.d.Jobs.Count(ctx, queue.Queued)
		qd.Running, _ = a.d.Jobs.Count(ctx, queue.Running)
		qd.Done, _ = a.d.Jobs.Count(ctx, queue.Done)
		qd.Failed, _ = a.d.Jobs.Count(ctx, queue.Failed)
	}

	a.writeJSON(w, http.StatusOK, overviewDTO{
		Queue: qd,
		Findings: overviewFindingsDTO{
			Total:        acc.total,
			Blocking:     acc.blocking,
			ByState:      acc.byState,
			ByConfidence: acc.byConf,
		},
		Repos:       len(repos),
		Month:       now.Format("2006-01"),
		SpendCents:  spent,
		BudgetCents: a.budgetCents(),
		GeneratedAt: rfc3339(now),
	})
}

// ---- jobs -------------------------------------------------------------------

func (a *api) handleJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := queue.Filter{
		Repo:   q.Get("repo"),
		Limit:  atoiDefault(q.Get("limit"), 0),
		Offset: atoiDefault(q.Get("offset"), 0),
	}
	if s := q.Get("state"); s != "" {
		f.State = queue.State(s)
	}
	recs, total, err := a.d.Jobs.List(ctx, f)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]jobDTO, 0, len(recs))
	for _, rec := range recs {
		items = append(items, mapJob(rec))
	}
	a.writeJSON(w, http.StatusOK, jobsPageDTO{Jobs: items, Total: total})
}

func (a *api) handleJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	rec, err := a.d.Jobs.GetByID(ctx, id)
	if errors.Is(err, queue.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// findings.db is per-repo, not per-head, so a job's "findings" are the
	// repo's findings (the honest best the on-disk data offers). The check
	// conclusion/title are recomputed from them the same way the posted check is.
	var fs []finding.Finding
	_ = a.withStore(rec.Repo, func(s FindingStore) error {
		list, err := s.List(ctx, finding.Query{Repo: rec.Repo})
		if err != nil {
			return err
		}
		fs = list
		return nil
	})
	sortFindings(fs)
	summaries := make([]findingSummaryDTO, 0, len(fs))
	for _, f := range fs {
		summaries = append(summaries, mapFindingSummary(f))
	}
	cr := checkrun.Render(fs)
	a.writeJSON(w, http.StatusOK, jobDetailDTO{
		Job:      mapJob(rec),
		Findings: summaries,
		Check:    checkDTO{Conclusion: cr.Conclusion, Title: cr.Title},
	})
}

type requeueDTO struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

func (a *api) handleRequeue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	switch err := a.d.Jobs.Requeue(ctx, id); {
	case errors.Is(err, queue.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "job not found")
		return
	case errors.Is(err, queue.ErrJobRunning):
		a.writeError(w, http.StatusConflict, "job is currently running")
		return
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, requeueDTO{ID: id, State: string(queue.Queued)})
}

// ---- installations / repos --------------------------------------------------

type repoDTO struct {
	Name             string `json:"name"`
	DefaultBranch    string `json:"defaultBranch"`
	Private          bool   `json:"private"`
	Indexed          bool   `json:"indexed"`
	LastReviewedAt   string `json:"lastReviewedAt,omitempty"`
	OpenFindings     int    `json:"openFindings"`
	BlockingFindings int    `json:"blockingFindings"`
}

type installationDTO struct {
	ID          int64     `json:"id"`
	Account     string    `json:"account"`
	AccountType string    `json:"accountType"`
	Host        string    `json:"host"`
	Suspended   bool      `json:"suspended"`
	RepoCount   int       `json:"repoCount"`
	Repos       []repoDTO `json:"repos"`
}

func (a *api) handleInstallations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	repos := a.allRepos(ctx)
	tallies := a.repoTallies(ctx, repos)
	instIDs := a.installationsByRepo(ctx, repos)
	lastReviewed := a.lastReviewedByRepo(ctx, repos)

	// Group repos by installation id.
	byInst := map[int64][]repoDTO{}
	for _, name := range repos {
		t := tallies[name]
		owner := ownerOf(name)
		rd := repoDTO{
			Name:             name,
			Indexed:          t.total > 0,
			OpenFindings:     t.open,
			BlockingFindings: t.blocking,
			LastReviewedAt:   rfc3339(lastReviewed[name]),
		}
		id := instIDs[name]
		byInst[id] = append(byInst[id], rd)
		_ = owner
	}

	var items []installationDTO
	var ids []int64
	for id := range byInst {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rs := byInst[id]
		account := ""
		if len(rs) > 0 {
			account = ownerOf(rs[0].Name)
		}
		items = append(items, installationDTO{
			ID:          id,
			Account:     account,
			AccountType: "User",
			Host:        a.d.Config.GitHubHost,
			RepoCount:   len(rs),
			Repos:       rs,
		})
	}
	a.writeJSON(w, http.StatusOK, page(items, len(items)))
}

// handleRepos is the computed-spec /api/repos flat view: one row per repo with
// job counts, last review, findings, and spend. Kept alongside installations so
// the full agreed endpoint set exists even though the GUI groups by install.
func (a *api) handleRepos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := a.now()
	repos := a.allRepos(ctx)
	tallies := a.repoTallies(ctx, repos)
	instIDs := a.installationsByRepo(ctx, repos)
	lastReviewed := a.lastReviewedByRepo(ctx, repos)

	type repoRow struct {
		Repo           string         `json:"repo"`
		InstallationID int64          `json:"installationID"`
		Jobs           map[string]int `json:"jobs"`
		LastHeadSHA    string         `json:"lastHeadSHA"`
		LastReviewedAt string         `json:"lastReviewedAt,omitempty"`
		Findings       int            `json:"findings"`
		Blocking       int            `json:"blocking"`
		SpendCents     int            `json:"spendCents"`
	}
	var rows []repoRow
	for _, name := range repos {
		counts := map[string]int{"queued": 0, "running": 0, "done": 0, "failed": 0}
		lastHead := ""
		if a.d.Jobs != nil {
			if recs, _, err := a.d.Jobs.List(ctx, queue.Filter{Repo: name}); err == nil {
				for i, rec := range recs {
					counts[string(rec.State)]++
					if i == 0 {
						lastHead = rec.HeadSHA // newest first
					}
				}
			}
		}
		spend, _ := a.d.Ledger.Spent(ctx, name, now)
		t := tallies[name]
		rows = append(rows, repoRow{
			Repo:           name,
			InstallationID: instIDs[name],
			Jobs:           counts,
			LastHeadSHA:    lastHead,
			LastReviewedAt: rfc3339(lastReviewed[name]),
			Findings:       t.total,
			Blocking:       t.blocking,
			SpendCents:     spend,
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"repos": rows})
}

// ---- findings ---------------------------------------------------------------

func (a *api) handleFindings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	query := finding.Query{
		Path:  q.Get("path"),
		Type:  finding.Type(q.Get("type")),
		State: finding.State(q.Get("state")),
		Limit: atoiDefault(q.Get("limit"), 0),
	}
	if c := q.Get("confidence"); c != "" {
		conf, ok := parseConfidence(c)
		if !ok {
			a.writeError(w, http.StatusBadRequest, "invalid confidence")
			return
		}
		query.MinConfidence = conf
	}
	if c := q.Get("minConfidence"); c != "" {
		conf, ok := parseConfidence(c)
		if !ok {
			a.writeError(w, http.StatusBadRequest, "invalid minConfidence")
			return
		}
		query.MinConfidence = conf
	}

	repoParam := q.Get("repo")
	var repos []string
	if repoParam != "" {
		repos = []string{repoParam}
	} else {
		repos = a.allRepos(ctx)
	}

	var all []finding.Finding
	for _, repo := range repos {
		rq := query
		rq.Repo = repo
		err := a.withStore(repo, func(fs FindingStore) error {
			list, err := fs.List(ctx, rq)
			if err != nil {
				return err
			}
			all = append(all, list...)
			return nil
		})
		if err != nil {
			// A single repo's store failing to open (e.g. missing key) must not
			// blank the whole dashboard; skip it.
			continue
		}
	}
	sortFindings(all)
	if query.Limit > 0 && len(all) > query.Limit {
		all = all[:query.Limit]
	}
	items := make([]findingSummaryDTO, 0, len(all))
	for _, f := range all {
		items = append(items, mapFindingSummary(f))
	}
	a.writeJSON(w, http.StatusOK, findingsResponseDTO{Repo: repoParam, Findings: items})
}

type findingsResponseDTO struct {
	Repo     string              `json:"repo"`
	Findings []findingSummaryDTO `json:"findings"`
}

func (a *api) handleFinding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	_, f, err := a.resolveFinding(ctx, r.URL.Query().Get("repo"), id)
	if errors.Is(err, finding.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, mapFindingDetail(f))
}

func (a *api) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	repo, _, err := a.resolveFinding(ctx, r.URL.Query().Get("repo"), id)
	if errors.Is(err, finding.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var out []transitionDTO
	err = a.withStore(repo, func(fs FindingStore) error {
		ts, err := fs.ListTransitions(ctx, id)
		if err != nil {
			return err
		}
		for _, t := range ts {
			out = append(out, mapTransition(t))
		}
		return nil
	})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []transitionDTO{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"id": id, "transitions": out})
}

type transitionReq struct {
	Repo   string `json:"repo"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

func (a *api) handleTransition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var req transitionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	to := finding.State(strings.TrimSpace(req.To))
	if !validState(to) {
		a.writeError(w, http.StatusBadRequest, "invalid target state")
		return
	}
	repo, _, err := a.resolveFinding(ctx, req.Repo, id)
	if errors.Is(err, finding.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var updated finding.Finding
	err = a.withStore(repo, func(fs FindingStore) error {
		if err := fs.Transition(ctx, id, to, req.Reason); err != nil {
			return err
		}
		f, err := fs.Get(ctx, id)
		if err != nil {
			return err
		}
		updated = f
		return nil
	})
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, transitionResultDTO{
		ID:        id,
		State:     string(updated.State),
		UpdatedAt: rfc3339(updated.UpdatedAt),
	})
}

// ---- budget -----------------------------------------------------------------

type budgetRepoDTO struct {
	Repo       string `json:"repo"`
	SpentCents int    `json:"spentCents"`
	Allowed    bool   `json:"allowed"`
	Note       string `json:"note"`
}

type budgetDTO struct {
	Month       string          `json:"month"`
	BudgetCents int             `json:"budgetCents"`
	Repos       []budgetRepoDTO `json:"repos"`
}

func (a *api) handleBudget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := a.now()
	capCents := a.budgetCents()
	pol := budget.Policy{MonthlyLimitCents: capCents}
	repos := a.allRepos(ctx)

	rows := make([]budgetRepoDTO, 0, len(repos))
	for _, repo := range repos {
		spent, _ := a.d.Ledger.Spent(ctx, repo, now)
		allow, note, _ := a.d.Ledger.Allow(ctx, repo, pol, now)
		rows = append(rows, budgetRepoDTO{
			Repo:       repo,
			SpentCents: spent,
			Allowed:    allow,
			Note:       note,
		})
	}
	a.writeJSON(w, http.StatusOK, budgetDTO{
		Month:       now.Format("2006-01"),
		BudgetCents: capCents,
		Repos:       rows,
	})
}

// ---- settings ---------------------------------------------------------------

type settingsEditableDTO struct {
	BudgetCents int `json:"budgetCents"`
}

type settingsReadOnlyDTO struct {
	Addr            string `json:"addr"`
	IntervalSeconds int    `json:"intervalSeconds"`
	GithubHost      string `json:"githubHost"`
	GithubAppID     int64  `json:"githubAppID"`
	SkillsEnabled   bool   `json:"skillsEnabled"`
	AllowRunc       bool   `json:"allowRunc"`
	DataRoot        string `json:"dataRoot"`
	KernelVersion   string `json:"kernelVersion"`
}

type settingsDTO struct {
	Editable         settingsEditableDTO `json:"editable"`
	ReadOnly         settingsReadOnlyDTO `json:"readOnly"`
	HasWebhookSecret bool                `json:"hasWebhookSecret"`
	HasAppKey        bool                `json:"hasAppKey"`
	HasStoreKey      bool                `json:"hasStoreKey"`
}

// budgetUpdateDTO is the PUT /api/settings body and response: the sole editable
// field is the per-repo monthly budget cap.
type budgetUpdateDTO struct {
	BudgetCents int `json:"budgetCents"`
}

func (a *api) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := a.d.Settings.Get()
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, a.settingsView(s))
}

func (a *api) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in budgetUpdateDTO
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.BudgetCents < 0 {
		a.writeError(w, http.StatusBadRequest, "budgetCents must not be negative")
		return
	}
	cur, err := a.d.Settings.Get()
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cur.BudgetCents = in.BudgetCents
	if err := a.d.Settings.Set(cur); err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, budgetUpdateDTO{BudgetCents: cur.BudgetCents})
}

func (a *api) settingsView(s AdminSettings) settingsDTO {
	return settingsDTO{
		Editable: settingsEditableDTO{BudgetCents: s.BudgetCents},
		ReadOnly: settingsReadOnlyDTO{
			Addr:            a.d.Config.Addr,
			IntervalSeconds: a.d.Config.IntervalSeconds,
			GithubHost:      a.d.Config.GitHubHost,
			GithubAppID:     a.d.Config.GitHubAppID,
			SkillsEnabled:   a.d.Config.SkillsEnabled,
			AllowRunc:       a.d.Config.AllowRunc,
			DataRoot:        a.d.Config.DataRoot,
			KernelVersion:   a.d.Config.KernelVersion,
		},
		HasWebhookSecret: a.d.Config.HasWebhookSecret,
		HasAppKey:        a.d.Config.HasAppKey,
		HasStoreKey:      a.d.Config.HasStoreKey,
	}
}

// ---- shared helpers ---------------------------------------------------------

func (a *api) budgetCents() int {
	if a.d.Settings != nil {
		if s, err := a.d.Settings.Get(); err == nil {
			return s.BudgetCents
		}
	}
	return 0
}

// repoTallies opens each repo's finding store once and rolls up its findings.
// A store that fails to open (missing key, absent db) yields an empty tally
// rather than failing the whole request.
func (a *api) repoTallies(ctx context.Context, repos []string) map[string]repoTally {
	out := make(map[string]repoTally, len(repos))
	for _, repo := range repos {
		if _, done := out[repo]; done {
			continue
		}
		t := repoTally{byType: map[string]int{}, byState: map[string]int{}, byConf: map[string]int{}, conclusion: "neutral"}
		if a.d.Catalog != nil {
			_ = a.withStore(repo, func(fs FindingStore) error {
				list, err := fs.List(ctx, finding.Query{Repo: repo})
				if err != nil {
					return err
				}
				t = tallyFindings(list)
				return nil
			})
		}
		out[repo] = t
	}
	return out
}

// installationsByRepo maps each repo to the installation id from its newest job
// payload (0 when the repo has no job carrying an id).
func (a *api) installationsByRepo(ctx context.Context, repos []string) map[string]int64 {
	out := make(map[string]int64, len(repos))
	if a.d.Jobs == nil {
		return out
	}
	for _, repo := range repos {
		recs, _, err := a.d.Jobs.List(ctx, queue.Filter{Repo: repo})
		if err != nil {
			continue
		}
		for _, rec := range recs { // newest first
			if id := installationID(rec); id != 0 {
				out[repo] = id
				break
			}
		}
	}
	return out
}

// lastReviewedByRepo maps each repo to the updated_at of its newest done job.
func (a *api) lastReviewedByRepo(ctx context.Context, repos []string) map[string]time.Time {
	out := map[string]time.Time{}
	if a.d.Jobs == nil {
		return out
	}
	for _, repo := range repos {
		recs, _, err := a.d.Jobs.List(ctx, queue.Filter{Repo: repo, State: queue.Done, Limit: 1})
		if err != nil || len(recs) == 0 {
			continue
		}
		out[repo] = recs[0].UpdatedAt
	}
	return out
}

// resolveFinding locates a finding by id. When repo is given it looks there;
// otherwise it scans every repo's store (ids are repo-salted fingerprints, so a
// cross-repo collision is not possible). Returns the owning repo and finding.
func (a *api) resolveFinding(ctx context.Context, repo, id string) (string, finding.Finding, error) {
	try := func(repo string) (finding.Finding, bool) {
		var f finding.Finding
		err := a.withStore(repo, func(fs FindingStore) error {
			got, err := fs.Get(ctx, id)
			if err != nil {
				return err
			}
			f = got
			return nil
		})
		return f, err == nil
	}
	if repo != "" {
		if f, ok := try(repo); ok {
			return repo, f, nil
		}
		return "", finding.Finding{}, finding.ErrNotFound
	}
	for _, repo := range a.allRepos(ctx) {
		if f, ok := try(repo); ok {
			return repo, f, nil
		}
	}
	return "", finding.Finding{}, finding.ErrNotFound
}

func installationID(rec queue.Record) int64 {
	var pj github.PRJob
	if err := json.Unmarshal(rec.Payload, &pj); err != nil {
		return 0
	}
	return pj.InstallationID
}

func distinctRepos(recs []queue.Record) []string {
	seen := map[string]bool{}
	var out []string
	for _, rec := range recs {
		if !seen[rec.Repo] {
			seen[rec.Repo] = true
			out = append(out, rec.Repo)
		}
	}
	return out
}

func ownerOf(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return name
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseConfidence(s string) (finding.Confidence, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "unverified", "0":
		return finding.Unverified, true
	case "statically-proven", "statically_proven", "1":
		return finding.StaticallyProven, true
	case "reproduced", "2":
		return finding.Reproduced, true
	}
	return 0, false
}

var lifecycleStates = map[finding.State]bool{
	finding.Predicted:     true,
	finding.Verified:      true,
	finding.ShippedAnyway: true,
	finding.Manifested:    true,
	finding.Attributed:    true,
	finding.Fixed:         true,
	finding.Closed:        true,
	finding.Dismissed:     true,
}

func validState(s finding.State) bool { return lifecycleStates[s] }

func sortFindings(fs []finding.Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Confidence != fs[j].Confidence {
			return fs[i].Confidence > fs[j].Confidence
		}
		if fs[i].Impact != fs[j].Impact {
			return fs[i].Impact > fs[j].Impact
		}
		return fs[i].UpdatedAt.After(fs[j].UpdatedAt)
	})
}
