package unattended

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/joshblazer/buggies/internal/integrations/github"
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
	s.mux.Handle("/webhook", &github.WebhookHandler{Secret: secret, OnPR: onPR})
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	return s
}

func (s *Server) nudge() {
	select {
	case s.kick <- struct{}{}:
	default: // a nudge is already pending; one drain handles all queued jobs
	}
}

// Handler is the HTTP handler: POST /webhook and GET /healthz.
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
