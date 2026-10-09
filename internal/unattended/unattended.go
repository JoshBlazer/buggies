// Package unattended wires the GitHub webhook to the review pipeline through
// the durable queue: the webhook enqueues (and acks fast), a worker drains and
// reviews. Idempotency lives in the queue key, so a redelivered webhook or a
// re-push to the same head is reviewed — and commented — exactly once.
package unattended

import (
	"context"
	"encoding/json"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
)

// Reviewer connects the queue to the pipeline. Review runs the pipeline for one
// PR; Post renders and posts the result. Budget (optional) gates a review
// before it runs; when it denies, PostSkip explains the skip on the PR instead
// of running the review. All are injected so the orchestration is testable
// without GitHub or models. Self-throttle is enforced inside Review (the
// pipeline), so throttled findings never reach Post.
type Reviewer struct {
	Q      *queue.Queue
	Review func(ctx context.Context, job github.PRJob) ([]finding.Finding, error)
	Post   func(ctx context.Context, job github.PRJob, findings []finding.Finding) error
	// Budget, if set, returns allow=false with a human note to skip the review
	// (e.g. the repo is over its monthly budget).
	Budget func(ctx context.Context, job github.PRJob) (allow bool, note string, err error)
	// PostSkip posts the skip note to the PR. Required if Budget can deny.
	PostSkip func(ctx context.Context, job github.PRJob, note string) error
	Logf     func(format string, args ...any)
}

func (r *Reviewer) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func repoName(job github.PRJob) string { return job.Owner + "/" + job.Repo }

// OnPR is the webhook callback: it enqueues the PR for review, idempotently.
// A duplicate (same head) is dropped, so GitHub's at-least-once delivery never
// produces a second review.
func (r *Reviewer) OnPR(ctx context.Context, job github.PRJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	key := queue.ReviewKey(repoName(job), job.HeadSHA)
	enqueued, err := r.Q.Enqueue(ctx, key, job.DeliveryID, repoName(job), job.HeadSHA, payload)
	if err != nil {
		return err
	}
	if !enqueued {
		r.logf("skip: %s already queued/reviewed (delivery %s)", key, job.DeliveryID)
	}
	return nil
}

// Work drains the queue once: for each job, review then post. It is safe to
// call on a timer or in a loop.
func (r *Reviewer) Work(ctx context.Context) error {
	return r.Q.Drain(ctx, func(ctx context.Context, j queue.Job) error {
		var job github.PRJob
		if err := json.Unmarshal(j.Payload, &job); err != nil {
			return err
		}
		if r.Budget != nil {
			allow, note, err := r.Budget(ctx, job)
			if err != nil {
				return err
			}
			if !allow {
				r.logf("budget: skipping %s #%d: %s", repoName(job), job.Number, note)
				if r.PostSkip != nil {
					return r.PostSkip(ctx, job, note)
				}
				return nil
			}
		}
		findings, err := r.Review(ctx, job)
		if err != nil {
			return err
		}
		return r.Post(ctx, job, findings)
	})
}
