package unattended

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
)

// postDelivery sends a signed pull_request webhook to a running server over
// real HTTP (not httptest.NewRequest), so the whole listener path is exercised.
func postDelivery(t *testing.T, base, deliveryID string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/webhook", strings.NewReader(delivery))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", sig(delivery))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestServeEndToEndReviewsOnce stands up the real HTTP handler and background
// worker, posts the same webhook twice over loopback, and asserts the daemon
// reviews and posts exactly once — the whole unattended path, port and all,
// minus only GitHub itself (Review/Post are stubs).
func TestServeEndToEndReviewsOnce(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var reviews, posts atomic.Int64
	done := make(chan struct{}, 4)
	r := &Reviewer{
		Q: q,
		Review: func(context.Context, github.PRJob) ([]finding.Finding, error) {
			reviews.Add(1)
			return []finding.Finding{{ID: "f_1"}}, nil
		},
		Post: func(context.Context, github.PRJob, []finding.Finding) error {
			posts.Add(1)
			done <- struct{}{}
			return nil
		},
	}

	server := NewServer([]byte(secret), r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Run(ctx, 20*time.Millisecond)

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	if code := postDelivery(t, ts.URL, "delivery-A"); code != http.StatusOK {
		t.Fatalf("first delivery status = %d", code)
	}
	// Wait for the worker to post (the nudge should make this near-instant).
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not post within 3s")
	}

	// Redeliver the same push; the queue dedups so no second review/post.
	if code := postDelivery(t, ts.URL, "delivery-B"); code != http.StatusOK {
		t.Fatalf("redelivery status = %d", code)
	}
	// Give the worker ample time to (not) do a second post.
	select {
	case <-done:
		t.Fatal("redelivery produced a second post")
	case <-time.After(300 * time.Millisecond):
	}

	if got := reviews.Load(); got != 1 {
		t.Errorf("reviews = %d, want 1", got)
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("posts = %d, want 1", got)
	}
	if n, _ := q.Count(context.Background(), queue.Done); n != 1 {
		t.Errorf("done jobs = %d, want 1", n)
	}
}

// TestHealthz proves the liveness endpoint the daemon exposes for orchestrators.
func TestHealthz(t *testing.T) {
	r := &Reviewer{}
	ts := httptest.NewServer(NewServer([]byte(secret), r).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", resp.StatusCode)
	}
}
