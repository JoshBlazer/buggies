package api

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
	"github.com/joshblazer/buggies/internal/store"
	"github.com/joshblazer/buggies/internal/surfaces/checkrun"
)

// The wire DTOs below match web/src/api/types.ts exactly: camelCase fields and
// the kernel's *string* enum spellings (finding.Confidence.String() etc.), not
// the PascalCase integer-enum default JSON of finding.Finding. Marshalling a
// DTO keeps the wire format self-describing and decoupled from Go internals.

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

type locationDTO struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Line   int    `json:"line"`
}

type sandboxRunDTO struct {
	Outcome    string `json:"outcome"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

type proofDTO struct {
	TestPath     string          `json:"testPath"`
	TestContent  string          `json:"testContent"`
	Output       string          `json:"output"`
	SuggestedFix string          `json:"suggestedFix"`
	Image        string          `json:"image"`
	Outcome      string          `json:"outcome"`
	Runs         []sandboxRunDTO `json:"runs"`
}

type findingDTO struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	Confidence    string      `json:"confidence"`
	Impact        string      `json:"impact"`
	State         string      `json:"state"`
	Blocking      bool        `json:"blocking"`
	Claim         string      `json:"claim"`
	Location      locationDTO `json:"location"`
	Proof         *proofDTO   `json:"proof"`
	CreatedAt     string      `json:"createdAt"`
	UpdatedAt     string      `json:"updatedAt"`
	KernelVersion string      `json:"kernelVersion"`
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func mapFinding(f finding.Finding) findingDTO {
	d := findingDTO{
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
		CreatedAt:     rfc3339(f.CreatedAt),
		UpdatedAt:     rfc3339(f.UpdatedAt),
		KernelVersion: f.KernelVersion,
	}
	// Proof travels only for a reproduced finding — the product promise is that
	// the developer never sees an unproven claim dressed as a fact.
	if f.Confidence == finding.Reproduced && f.Proof != nil {
		ev := f.Proof.Result.Evidence()
		output := ev.StackTrace
		if output == "" {
			output = ev.Stderr
		}
		runs := make([]sandboxRunDTO, 0, len(f.Proof.Result.Runs))
		for _, r := range f.Proof.Result.Runs {
			runs = append(runs, sandboxRunDTO{
				Outcome:    string(f.Proof.Result.Outcome),
				ExitCode:   r.ExitCode,
				DurationMs: r.Timing.Milliseconds(),
			})
		}
		d.Proof = &proofDTO{
			TestPath:     f.Proof.Test.Path,
			TestContent:  string(f.Proof.Test.Content),
			Output:       output,
			SuggestedFix: f.Proof.SuggestedFix,
			Image:        ev.Image,
			Outcome:      string(f.Proof.Result.Outcome),
			Runs:         runs,
		}
	}
	return d
}

type jobDTO struct {
	ID            string `json:"id"`
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	Number        int    `json:"number"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	HeadSha       string `json:"headSha"`
	BaseSha       string `json:"baseSha"`
	Fork          bool   `json:"fork"`
	Status        string `json:"status"`
	Conclusion    string `json:"conclusion"`
	SkipReason    string `json:"skipReason,omitempty"`
	FindingCount  int    `json:"findingCount"`
	BlockingCount int    `json:"blockingCount"`
	ReviewURL     string `json:"reviewUrl,omitempty"`
	ModelCalls    int    `json:"modelCalls"`
	CostCents     int    `json:"costCents"`
	QueuedAt      string `json:"queuedAt"`
	FinishedAt    string `json:"finishedAt,omitempty"`
}

// jobStatus maps a queue.State to the GUI's JobStatus vocabulary.
func jobStatus(st queue.State) string {
	switch st {
	case queue.Failed:
		return "error"
	default:
		return string(st) // queued | running | done
	}
}

// mapJob builds the job DTO. tally supplies the repo's finding counts and check
// conclusion (findings.db is per-repo, so every job of a repo shares them —
// the honest best the on-disk data offers without a per-head finding index).
func mapJob(rec queue.Record, tally repoTally) jobDTO {
	var pj github.PRJob
	_ = json.Unmarshal(rec.Payload, &pj)
	d := jobDTO{
		ID:            strconv.FormatInt(rec.ID, 10),
		Owner:         pj.Owner,
		Repo:          rec.Repo,
		Number:        pj.Number,
		Title:         prTitle(pj.Number),
		HeadSha:       rec.HeadSHA,
		BaseSha:       pj.BaseSHA,
		Fork:          pj.Fork,
		Status:        jobStatus(rec.State),
		Conclusion:    tally.conclusion,
		FindingCount:  tally.total,
		BlockingCount: tally.blocking,
		QueuedAt:      rfc3339(rec.CreatedAt),
	}
	if rec.State == queue.Done || rec.State == queue.Failed {
		d.FinishedAt = rfc3339(rec.UpdatedAt)
	}
	return d
}

func prTitle(number int) string {
	if number == 0 {
		return ""
	}
	return "PR #" + strconv.Itoa(number)
}

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

// transition DTOs (history endpoint, computed-spec shape).
type transitionDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

func mapTransition(t store.Transition) transitionDTO {
	return transitionDTO{From: string(t.From), To: string(t.To), Reason: t.Reason, At: rfc3339(t.At)}
}
