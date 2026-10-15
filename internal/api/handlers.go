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
)

// ---- overview ---------------------------------------------------------------

type overviewDTO struct {
	ReposActive         int            `json:"reposActive"`
	Installations       int            `json:"installations"`
	PrsReviewed         int            `json:"prsReviewed"`
	OpenFindings        int            `json:"openFindings"`
	BlockingFindings    int            `json:"blockingFindings"`
	ReproducedFindings  int            `json:"reproducedFindings"`
	AttributedIncidents int            `json:"attributedIncidents"`
	DismissalRate       float64        `json:"dismissalRate"`
	SpentCents          int            `json:"spentCents"`
	LimitCents          int            `json:"limitCents"`
	FindingsByType      map[string]int `json:"findingsByType"`
	RecentJobs          []jobDTO       `json:"recentJobs"`
}

func (a *api) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := a.now()
	repos := a.allRepos(ctx)

	var acc repoTally
	acc.byType = map[string]int{}
	acc.byState = map[string]int{}
	acc.byConf = map[string]int{}
	tallies := a.repoTallies(ctx, repos)
	installs := map[int64]bool{}
	spent := 0
	for _, repo := range repos {
		tallies[repo].mergeInto(&acc)
		if s, err := a.d.Ledger.Spent(ctx, repo, now); err == nil {
			spent += s
		}
	}

	prsReviewed := 0
	if a.d.Jobs != nil {
		if n, err := a.d.Jobs.Count(ctx, queue.Done); err == nil {
			prsReviewed = n
		}
	}

	var recent []jobDTO
	if a.d.Jobs != nil {
		recs, _, err := a.d.Jobs.List(ctx, queue.Filter{Limit: 10})
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, rec := range recs {
			recent = append(recent, mapJob(rec, tallies[rec.Repo]))
			if id := installationID(rec); id != 0 {
				installs[id] = true
			}
		}
	}
	// Count installations across every repo, not only the recent ten.
	for _, id := range a.installationsByRepo(ctx, repos) {
		if id != 0 {
			installs[id] = true
		}
	}

	dismissalRate := 0.0
	if acc.decided > 0 {
		dismissalRate = float64(acc.dismissed) / float64(acc.decided)
	}
	if acc.byType == nil {
		acc.byType = map[string]int{}
	}

	a.writeJSON(w, http.StatusOK, overviewDTO{
		ReposActive:         len(repos),
		Installations:       len(installs),
		PrsReviewed:         prsReviewed,
		OpenFindings:        acc.open,
		BlockingFindings:    acc.blocking,
		ReproducedFindings:  acc.reproduced,
		AttributedIncidents: acc.attributed,
		DismissalRate:       dismissalRate,
		SpentCents:          spent,
		LimitCents:          a.budgetCents(),
		FindingsByType:      acc.byType,
		RecentJobs:          recent,
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
	// Tally once per distinct repo in this page.
	repos := distinctRepos(recs)
	tallies := a.repoTallies(ctx, repos)
	items := make([]jobDTO, 0, len(recs))
	for _, rec := range recs {
		items = append(items, mapJob(rec, tallies[rec.Repo]))
	}
	a.writeJSON(w, http.StatusOK, page(items, total))
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
	tally := a.repoTallies(ctx, []string{rec.Repo})[rec.Repo]
	a.writeJSON(w, http.StatusOK, mapJob(rec, tally))
}

type requeueDTO struct {
	ID    string `json:"id"`
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
	a.writeJSON(w, http.StatusOK, requeueDTO{ID: strconv.FormatInt(id, 10), State: string(queue.Queued)})
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

	var repos []string
	if repo := q.Get("repo"); repo != "" {
		repos = []string{repo}
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
	total := len(all)
	if query.Limit > 0 && len(all) > query.Limit {
		all = all[:query.Limit]
	}
	items := make([]findingDTO, 0, len(all))
	for _, f := range all {
		items = append(items, mapFinding(f))
	}
	a.writeJSON(w, http.StatusOK, page(items, total))
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
	a.writeJSON(w, http.StatusOK, mapFinding(f))
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
	a.writeJSON(w, http.StatusOK, mapFinding(updated))
}

// ---- budget -----------------------------------------------------------------

type repoSpendDTO struct {
	Repo       string `json:"repo"`
	Period     string `json:"period"`
	SpentCents int    `json:"spentCents"`
	LimitCents int    `json:"limitCents"`
	Reviews    int    `json:"reviews"`
	ModelCalls int    `json:"modelCalls"`
	OverBudget bool   `json:"overBudget"`
}

type budgetSummaryDTO struct {
	Period          string         `json:"period"`
	TotalSpentCents int            `json:"totalSpentCents"`
	TotalLimitCents int            `json:"totalLimitCents"`
	Repos           []repoSpendDTO `json:"repos"`
}

func (a *api) handleBudget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := a.now()
	period := budget.Policy{}.Period(now)
	capCents := a.budgetCents()
	pol := budget.Policy{MonthlyLimitCents: capCents}
	repos := a.allRepos(ctx)

	var rows []repoSpendDTO
	totalSpent := 0
	for _, repo := range repos {
		spent, _ := a.d.Ledger.Spent(ctx, repo, now)
		allow, _, _ := a.d.Ledger.Allow(ctx, repo, pol, now)
		reviews := 0
		if a.d.Jobs != nil {
			if _, n, err := a.d.Jobs.List(ctx, queue.Filter{Repo: repo, State: queue.Done}); err == nil {
				reviews = n
			}
		}
		totalSpent += spent
		rows = append(rows, repoSpendDTO{
			Repo:       repo,
			Period:     period,
			SpentCents: spent,
			LimitCents: capCents,
			Reviews:    reviews,
			OverBudget: !allow,
		})
	}
	if rows == nil {
		rows = []repoSpendDTO{}
	}
	a.writeJSON(w, http.StatusOK, budgetSummaryDTO{
		Period:          period,
		TotalSpentCents: totalSpent,
		TotalLimitCents: capCents * len(repos),
		Repos:           rows,
	})
}

// ---- settings ---------------------------------------------------------------

type settingsDTO struct {
	BudgetCents           int    `json:"budgetCents"`
	MinConfidence         string `json:"minConfidence"`
	PostAdvisory          bool   `json:"postAdvisory"`
	SkillsEnabled         bool   `json:"skillsEnabled"`
	GithubHost            string `json:"githubHost"`
	WorkerIntervalSeconds int    `json:"workerIntervalSeconds"`
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
	dec := json.NewDecoder(r.Body)
	var in settingsDTO
	if err := dec.Decode(&in); err != nil {
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
	cur.PostAdvisory = in.PostAdvisory
	if in.MinConfidence != "" {
		conf, ok := parseConfidence(in.MinConfidence)
		if !ok {
			a.writeError(w, http.StatusBadRequest, "invalid minConfidence")
			return
		}
		cur.MinConfidence = conf
	}
	// SkillsEnabled, GithubHost, WorkerIntervalSeconds are read-only daemon
	// config: accepted in the payload (the GUI PUTs the whole object) but never
	// applied — the response always reflects the live config values.
	if err := a.d.Settings.Set(cur); err != nil {
		a.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, a.settingsView(cur))
}

func (a *api) settingsView(s AdminSettings) settingsDTO {
	return settingsDTO{
		BudgetCents:           s.BudgetCents,
		MinConfidence:         s.MinConfidence.String(),
		PostAdvisory:          s.PostAdvisory,
		SkillsEnabled:         a.d.Config.SkillsEnabled,
		GithubHost:            a.d.Config.GitHubHost,
		WorkerIntervalSeconds: a.d.Config.IntervalSeconds,
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
