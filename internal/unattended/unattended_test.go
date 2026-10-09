package unattended

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
)

const secret = "webhook-secret"

const delivery = `{
  "action": "synchronize",
  "number": 42,
  "pull_request": { "head": { "sha": "9f28ce3", "repo": { "full_name": "tj/commander.js" } }, "base": { "sha": "ba6d13d" } },
  "repository": { "name": "commander.js", "owner": { "login": "tj" } },
  "installation": { "id": 7654321 }
}`

func sig(body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func deliver(t *testing.T, h http.Handler, deliveryID string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(delivery))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", sig(delivery))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestReplayedWebhookReviewsAndCommentsOnce is the M7 exit check: the same
// recorded webhook delivered twice must yield exactly one review and one
// posted comment.
func TestReplayedWebhookReviewsAndCommentsOnce(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var reviews, posts atomic.Int64
	r := &Reviewer{
		Q: q,
		Review: func(_ context.Context, _ github.PRJob) ([]finding.Finding, error) {
			reviews.Add(1)
			return []finding.Finding{{ID: "f_1"}}, nil
		},
		Post: func(_ context.Context, _ github.PRJob, fs []finding.Finding) error {
			posts.Add(1)
			return nil
		},
	}
	h := &github.WebhookHandler{Secret: []byte(secret), OnPR: r.OnPR}

	// Two deliveries of the SAME push (GitHub's at-least-once redelivery):
	// different delivery IDs, same repo+head.
	if code := deliver(t, h, "delivery-A"); code != http.StatusOK {
		t.Fatalf("first delivery status = %d", code)
	}
	if code := deliver(t, h, "delivery-B"); code != http.StatusOK {
		t.Fatalf("redelivery status = %d", code)
	}

	// Drain twice to prove the second drain finds nothing new, either.
	if err := r.Work(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Work(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := reviews.Load(); got != 1 {
		t.Errorf("reviews = %d, want 1", got)
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("posted comments = %d, want 1", got)
	}
	if n, _ := q.Count(context.Background(), queue.Queued); n != 0 {
		t.Errorf("queued jobs left = %d, want 0", n)
	}
	if n, _ := q.Count(context.Background(), queue.Done); n != 1 {
		t.Errorf("done jobs = %d, want 1", n)
	}
}

// TestOverBudgetSkipsAndPostsNote is the M9 exit check: a repo past budget is
// not reviewed; a skip note is posted instead.
func TestOverBudgetSkipsAndPostsNote(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var reviews, skips atomic.Int64
	var gotNote string
	r := &Reviewer{
		Q:      q,
		Review: func(context.Context, github.PRJob) ([]finding.Finding, error) { reviews.Add(1); return nil, nil },
		Post:   func(context.Context, github.PRJob, []finding.Finding) error { return nil },
		Budget: func(context.Context, github.PRJob) (bool, string, error) {
			return false, "monthly budget reached ($5.00 of $5.00 for 2026-10)", nil
		},
		PostSkip: func(_ context.Context, _ github.PRJob, note string) error {
			skips.Add(1)
			gotNote = note
			return nil
		},
	}
	ctx := context.Background()
	if err := r.OnPR(ctx, github.PRJob{Owner: "tj", Repo: "commander.js", HeadSHA: "ccc", DeliveryID: "d"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Work(ctx); err != nil {
		t.Fatal(err)
	}

	if reviews.Load() != 0 {
		t.Errorf("reviews = %d, want 0 (over budget)", reviews.Load())
	}
	if skips.Load() != 1 {
		t.Errorf("skip notes = %d, want 1", skips.Load())
	}
	if gotNote == "" {
		t.Error("skip note empty; the PR must say why it was skipped")
	}
	// The job is consumed (completed), not left to retry forever.
	if n, _ := q.Count(ctx, queue.Done); n != 1 {
		t.Errorf("done = %d, want 1", n)
	}
}

// TestNewHeadReReviews confirms a genuinely new push (new head) is a new job.
func TestNewHeadReReviews(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var reviews atomic.Int64
	r := &Reviewer{
		Q:      q,
		Review: func(context.Context, github.PRJob) ([]finding.Finding, error) { reviews.Add(1); return nil, nil },
		Post:   func(context.Context, github.PRJob, []finding.Finding) error { return nil },
	}
	ctx := context.Background()
	if err := r.OnPR(ctx, github.PRJob{Owner: "tj", Repo: "commander.js", HeadSHA: "aaa", DeliveryID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.OnPR(ctx, github.PRJob{Owner: "tj", Repo: "commander.js", HeadSHA: "bbb", DeliveryID: "d2"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Work(ctx); err != nil {
		t.Fatal(err)
	}
	if got := reviews.Load(); got != 2 {
		t.Errorf("reviews = %d, want 2 (one per head)", got)
	}
}
