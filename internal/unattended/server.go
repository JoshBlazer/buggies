package unattended

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/joshblazer/buggies/internal/integrations/github"
	"github.com/joshblazer/buggies/internal/queue"
)

// Server wires a Reviewer to a GitHub webhook handler and a background worker.
// It owns no network port: the caller serves Handler() on whatever listener it
// chooses (a real port in production, an httptest server in tests) and cancels
// the Run context to stop the worker. Keeping the port out makes the whole
// intake→queue→worker→post path testable end-to-end over loopback.
type Server struct {
	r    *Reviewer
	kick chan struct{}
	mux  *http.ServeMux
	// Ready, if set, is the readiness check behind GET /readyz: it returns nil
	// when the daemon can actually do a review (the isolation runtime is
	// reachable), or an error naming what is wrong. Liveness (/healthz) only
	// says the process is up; readiness says it can work. When nil, /readyz is
	// readiness-equals-liveness.
	Ready func(ctx context.Context) error
}

// NewServer builds the handler. Every reviewable webhook enqueues (idempotently,
// via Reviewer.OnPR) and nudges the worker so a PR is reviewed promptly rather
// than only on the next poll tick.
func NewServer(secret []byte, r *Reviewer) *Server {
	s := &Server{r: r, kick: make(chan struct{}, 1), mux: http.NewServeMux()}
	onPR := func(ctx context.Context, job github.PRJob) error {
		err := r.OnPR(ctx, job)
		s.nudge()
		return err
	}
	s.mux.Handle("/webhook", &github.WebhookHandler{Secret: secret, OnPR: onPR, OnUninstall: r.Uninstall})
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	s.mux.HandleFunc("/readyz", s.handleReady)
	s.mux.HandleFunc("/metrics", s.handleMetrics)
	return s
}

// handleReady is the readiness probe: 200 when the daemon can do a review,
// 503 (naming the cause) when it cannot — e.g. the sandbox runtime is down.
// An orchestrator uses this to hold traffic off an instance that would only
// post "sandbox unavailable" checks.
func (s *Server) handleReady(w http.ResponseWriter, req *http.Request) {
	if s.Ready != nil {
		if err := s.Ready(req.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "not ready: %v\n", err)
			return
		}
	}
	fmt.Fprintln(w, "ready")
}

// handleMetrics exposes queue depth by state plus a couple of process gauges in
// Prometheus text format — no client library, so the single-binary stance holds.
// queued/running show backlog and in-flight; done is cumulative throughput;
// failed is the dead-letter count (a rising failed gauge is the alert signal).
func (s *Server) handleMetrics(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	var b strings.Builder
	b.WriteString("# HELP buggies_up 1 if the daemon is serving.\n# TYPE buggies_up gauge\nbuggies_up 1\n")
	b.WriteString("# HELP buggies_queue_jobs Number of jobs in the queue by state.\n# TYPE buggies_queue_jobs gauge\n")
	for _, st := range []queue.State{queue.Queued, queue.Running, queue.Done, queue.Failed} {
		n, err := s.r.Q.Count(ctx, st)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "buggies_queue_jobs{state=%q} %d\n", string(st), n)
	}
	b.WriteString("# HELP buggies_goroutines Current number of goroutines.\n# TYPE buggies_goroutines gauge\n")
	fmt.Fprintf(&b, "buggies_goroutines %d\n", runtime.NumGoroutine())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	io.WriteString(w, b.String())
}

func (s *Server) nudge() {
	select {
	case s.kick <- struct{}{}:
	default: // a nudge is already pending; one drain handles all queued jobs
	}
}

// Handler is the HTTP handler: POST /webhook, GET /healthz, GET /readyz, and
// GET /metrics.
func (s *Server) Handler() http.Handler { return s.mux }

// Run drains the queue whenever a webhook arrives, and on a timer so a job left
// behind by a crash still runs. It blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-t.C:
		}
		if err := s.r.Work(ctx); err != nil {
			s.r.logf("work: %v", err)
		}
	}
}
