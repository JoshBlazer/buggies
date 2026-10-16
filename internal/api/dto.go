package api

import (
	"encoding/json"
	"time"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
	"github.com/joshblazer/buggies/internal/store"
	"github.com/joshblazer/buggies/internal/surfaces/checkrun"
	"github.com/joshblazer/buggies/internal/surfaces/prcomment"
)

// The wire DTOs below match web/src/api/types.ts exactly: camelCase fields and
// the kernel's *string* enum spellings (finding.Confidence.String() etc.), not
// the PascalCase integer-enum default JSON of finding.Finding. Marshalling a
// DTO keeps the wire format self-describing and decoupled from Go internals.

// pageDTO is the generic {items,total} envelope used by endpoints that are not
// part of the dashboard's typed contract (e.g. /api/installations).
type pageDTO[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

func page[T any](items []T, total int) pageDTO[T] {
	if items == nil {
		items = []T{}
	}
	return pageDTO[T]{Items: items, Total: total}
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ---- overview (types.ts Overview) -------------------------------------------

type queueDepthDTO struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

type overviewFindingsDTO struct {
	Total        int            `json:"total"`
	Blocking     int            `json:"blocking"`
	ByState      map[string]int `json:"byState"`
	ByConfidence map[string]int `json:"byConfidence"`
}

type overviewDTO struct {
	Queue       queueDepthDTO       `json:"queue"`
	Findings    overviewFindingsDTO `json:"findings"`
	Repos       int                 `json:"repos"`
	Month       string              `json:"month"`
	SpendCents  int                 `json:"spendCents"`
	BudgetCents int                 `json:"budgetCents"`
	GeneratedAt string              `json:"generatedAt"`
}

// ---- jobs (types.ts Job / JobsPage / JobDetail) -----------------------------

type jobDTO struct {
	ID             int64  `json:"id"`
	Key            string `json:"key"`
	Repo           string `json:"repo"`
	Number         int    `json:"number"`
	HeadSHA        string `json:"headSHA"`
	BaseSHA        string `json:"baseSHA"`
	Fork           bool   `json:"fork"`
	InstallationID int64  `json:"installationID"`
	Action         string `json:"action"`
	DeliveryID     string `json:"deliveryID"`
	State          string `json:"state"`
	Attempts       int    `json:"attempts"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// mapJob builds the job DTO from a queue record and its decoded PR payload.
func mapJob(rec queue.Record) jobDTO {
	var pj github.PRJob
	_ = json.Unmarshal(rec.Payload, &pj)
	return jobDTO{
		ID:             rec.ID,
		Key:            rec.Key,
		Repo:           rec.Repo,
		Number:         pj.Number,
		HeadSHA:        rec.HeadSHA,
		BaseSHA:        pj.BaseSHA,
		Fork:           pj.Fork,
		InstallationID: pj.InstallationID,
		Action:         pj.Action,
		DeliveryID:     rec.DeliveryID,
		State:          string(rec.State),
		Attempts:       rec.Attempts,
		CreatedAt:      rfc3339(rec.CreatedAt),
		UpdatedAt:      rfc3339(rec.UpdatedAt),
	}
}

type jobsPageDTO struct {
	Jobs  []jobDTO `json:"jobs"`
	Total int      `json:"total"`
}

type checkDTO struct {
	Conclusion string `json:"conclusion"`
	Title      string `json:"title"`
}

type jobDetailDTO struct {
	Job      jobDTO              `json:"job"`
	Findings []findingSummaryDTO `json:"findings"`
	Check    checkDTO            `json:"check"`
}

// ---- findings (types.ts FindingSummary / FindingDetail) ---------------------

type locationDTO struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Line   int    `json:"line"`
}

// findingSummaryDTO is the list-row shape: flat path/symbol/line, no proof.
type findingSummaryDTO struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Confidence string `json:"confidence"`
	Impact     string `json:"impact"`
	State      string `json:"state"`
	Claim      string `json:"claim"`
	Blocking   bool   `json:"blocking"`
	Path       string `json:"path"`
	Symbol     string `json:"symbol"`
	Line       int    `json:"line"`
	Reproduced bool   `json:"reproduced"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

func mapFindingSummary(f finding.Finding) findingSummaryDTO {
	return findingSummaryDTO{
		ID:         f.ID,
		Type:       string(f.Type),
		Confidence: f.Confidence.String(),
		Impact:     f.Impact.String(),
		State:      string(f.State),
		Claim:      f.Claim,
		Blocking:   f.Blocking(),
		Path:       f.Location.Path,
		Symbol:     f.Location.Symbol,
		Line:       f.Location.Line,
		Reproduced: f.Confidence == finding.Reproduced,
		CreatedAt:  rfc3339(f.CreatedAt),
		UpdatedAt:  rfc3339(f.UpdatedAt),
	}
}

type sandboxRunDTO struct {
	Stdout       string  `json:"stdout"`
	Stderr       string  `json:"stderr"`
	StackTrace   string  `json:"stackTrace"`
	ExitCode     int     `json:"exitCode"`
	TimingMs     int64   `json:"timingMs"`
	Image        string  `json:"image"`
	PeakMemoryMB int64   `json:"peakMemoryMB"`
	CPUSeconds   float64 `json:"cpuSeconds"`
}

type proofTestDTO struct {
	Path    string   `json:"path"`
	Content string   `json:"content"`
	Cmd     []string `json:"cmd"`
}

type proofResultDTO struct {
	Outcome      string          `json:"outcome"`
	Reproducible bool            `json:"reproducible"`
	Runs         []sandboxRunDTO `json:"runs"`
}

type proofDTO struct {
	Test         proofTestDTO   `json:"test"`
	Result       proofResultDTO `json:"result"`
	SuggestedFix string         `json:"suggestedFix"`
}

type findingDetailDTO struct {
	ID               string      `json:"id"`
	Type             string      `json:"type"`
	Confidence       string      `json:"confidence"`
	Impact           string      `json:"impact"`
	State            string      `json:"state"`
	Blocking         bool        `json:"blocking"`
	Claim            string      `json:"claim"`
	Location         locationDTO `json:"location"`
	KernelVersion    string      `json:"kernelVersion"`
	CreatedAt        string      `json:"createdAt"`
	UpdatedAt        string      `json:"updatedAt"`
	Proof            *proofDTO   `json:"proof"`
	RenderedMarkdown string      `json:"renderedMarkdown"`
}

func mapFindingDetail(f finding.Finding) findingDetailDTO {
	d := findingDetailDTO{
		ID:         f.ID,
		Type:       string(f.Type),
		Confidence: f.Confidence.String(),
		Impact:     f.Impact.String(),
		State:      string(f.State),
		Blocking:   f.Blocking(),
		Claim:      f.Claim,
		Location: locationDTO{
			Repo:   f.Location.Repo,
			Path:   f.Location.Path,
			Symbol: f.Location.Symbol,
			Line:   f.Location.Line,
		},
		KernelVersion:    f.KernelVersion,
		CreatedAt:        rfc3339(f.CreatedAt),
		UpdatedAt:        rfc3339(f.UpdatedAt),
		RenderedMarkdown: prcomment.Render(f).Body,
	}
	// Proof travels only for a reproduced finding — the product promise is that
	// the developer never sees an unproven claim dressed as a fact.
	if f.Confidence == finding.Reproduced && f.Proof != nil {
		runs := make([]sandboxRunDTO, 0, len(f.Proof.Result.Runs))
		for _, r := range f.Proof.Result.Runs {
			runs = append(runs, sandboxRunDTO{
				Stdout:       r.Stdout,
				Stderr:       r.Stderr,
				StackTrace:   r.StackTrace,
				ExitCode:     r.ExitCode,
				TimingMs:     r.Timing.Milliseconds(),
				Image:        r.Image,
				PeakMemoryMB: r.Resource.PeakMemoryMB,
				CPUSeconds:   r.Resource.CPUSeconds,
			})
		}
		cmd := f.Proof.Test.Cmd
		if cmd == nil {
			cmd = []string{}
		}
		d.Proof = &proofDTO{
			Test:         proofTestDTO{Path: f.Proof.Test.Path, Content: string(f.Proof.Test.Content), Cmd: cmd},
			Result:       proofResultDTO{Outcome: string(f.Proof.Result.Outcome), Reproducible: f.Proof.Result.Reproducible, Runs: runs},
			SuggestedFix: f.Proof.SuggestedFix,
		}
	}
	return d
}

// ---- transitions (types.ts Transition / TransitionResult) -------------------

type transitionDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

func mapTransition(t store.Transition) transitionDTO {
	return transitionDTO{From: string(t.From), To: string(t.To), Reason: t.Reason, At: rfc3339(t.At)}
}

type transitionResultDTO struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	UpdatedAt string `json:"updatedAt"`
}

// ---- repo rollup (shared by jobs, repos, overview) --------------------------

// repoTally is the per-repo finding rollup shared by jobs, repos, and overview.
type repoTally struct {
	total      int
	blocking   int
	open       int // not in a terminal state (fixed/closed/dismissed)
	reproduced int
	attributed int
	conclusion string // "success" | "failure" | "neutral"
	byType     map[string]int
	byState    map[string]int
	byConf     map[string]int
	dismissed  int
	decided    int
}

func tallyFindings(fs []finding.Finding) repoTally {
	t := repoTally{byType: map[string]int{}, byState: map[string]int{}, byConf: map[string]int{}}
	t.total = len(fs)
	for _, f := range fs {
		if f.Blocking() {
			t.blocking++
		}
		switch f.State {
		case finding.Fixed, finding.Closed, finding.Dismissed:
		default:
			t.open++
		}
		if f.Confidence == finding.Reproduced {
			t.reproduced++
		}
		if f.State == finding.Attributed {
			t.attributed++
		}
		switch f.State {
		case finding.Dismissed:
			t.dismissed++
			t.decided++
		case finding.Fixed, finding.Closed, finding.ShippedAnyway:
			t.decided++
		}
		t.byType[string(f.Type)]++
		t.byState[string(f.State)]++
		t.byConf[f.Confidence.String()]++
	}
	t.conclusion = checkrun.Render(fs).Conclusion
	if t.total == 0 {
		t.conclusion = "neutral"
	}
	return t
}

func (t repoTally) mergeInto(acc *repoTally) {
	acc.total += t.total
	acc.blocking += t.blocking
	acc.open += t.open
	acc.reproduced += t.reproduced
	acc.attributed += t.attributed
	acc.dismissed += t.dismissed
	acc.decided += t.decided
	for k, v := range t.byType {
		acc.byType[k] += v
	}
	for k, v := range t.byState {
		acc.byState[k] += v
	}
	for k, v := range t.byConf {
		acc.byConf[k] += v
	}
}
