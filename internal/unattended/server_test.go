package unattended

import (
	"context"
	"errors"
	"io"
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

// TestReadyz proves readiness is 200 when the Ready check passes (or is unset)
// and 503 — naming the cause — when it fails, so an orchestrator holds traffic
// off an instance whose sandbox is down.
func TestReadyz(t *testing.T) {
	// Unset Ready: readiness equals liveness.
	s := NewServer([]byte(secret), &Reviewer{})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz (no check) = %d, want 200", resp.StatusCode)
	}

	// Failing Ready: 503 with the reason in the body.
	s.Ready = func(context.Context) error { return errors.New("sandbox daemon unreachable") }
	resp, err = http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz (failing) = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "sandbox daemon unreachable") {
		t.Fatalf("readyz body = %q, want the cause", body)
	}
}

// TestMetrics proves the Prometheus endpoint reports queue depth by state.
func TestMetrics(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if _, err := q.Enqueue(context.Background(), "o/r@h", "d", "o/r", "h", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewServer([]byte(secret), &Reviewer{Q: q}).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"buggies_up 1", `buggies_queue_jobs{state="queued"} 1`, "buggies_goroutines"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("metrics body missing %q; got:\n%s", want, body)
		}
	}
}
