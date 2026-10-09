package unattended

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshblazer/kernel/finding"

	"github.com/joshblazer/buggies/internal/integrations/github"
)

// mockGitHub records the review and check-run requests Publish sends and serves
// plausible responses, standing in for api.github.com so the posting path runs
// end-to-end without a live App.
type mockGitHub struct {
	reviews    int
	reviewBody map[string]any
	checkGets  int
	checkPosts int
	checkBody  map[string]any
	unexpected []string
}

func (m *mockGitHub) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reviews"):
			m.reviews++
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &m.reviewBody)
			json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/tj/commander.js/pull/42#pullrequestreview-1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			m.checkGets++
			json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{}}) // none yet → Publish creates
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/check-runs"):
			m.checkPosts++
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &m.checkBody)
			json.NewEncoder(w).Encode(map[string]any{"id": 999})
		default:
			m.unexpected = append(m.unexpected, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func publishTo(t *testing.T, m *mockGitHub, findings []finding.Finding) {
	t.Helper()
	srv := m.server(t)
	defer srv.Close()
	cl := &github.Client{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	pr := github.PR{Owner: "tj", Repo: "commander.js", Number: 42}
	url, err := Publish(context.Background(), cl, pr, "9f28ce3", findings)
	if err != nil {
		t.Fatal(err)
	}
	if url == "" {
		t.Error("Publish returned an empty review URL")
	}
	if len(m.unexpected) > 0 {
		t.Fatalf("unexpected GitHub calls: %v", m.unexpected)
	}
}

// TestPublishBlockingFindingFailsTheGate proves the daemon's post path for a
// reproduced, exploitable finding: one review with a comment, and a check run
// whose conclusion is failure (the merge gate).
func TestPublishBlockingFindingFailsTheGate(t *testing.T) {
	var m mockGitHub
	publishTo(t, &m, []finding.Finding{{
		ID: "f_1", Type: "correctness", Confidence: finding.Reproduced, Impact: finding.Exploitable,
		Claim: "short flag -p is dropped", Location: finding.Location{Path: "lib/option.js", Line: 40},
	}})

	if m.reviews != 1 {
		t.Fatalf("reviews posted = %d, want 1", m.reviews)
	}
	if m.reviewBody["event"] != "COMMENT" {
		t.Errorf("review event = %v, want COMMENT", m.reviewBody["event"])
	}
	if cs, _ := m.reviewBody["comments"].([]any); len(cs) != 1 {
		t.Errorf("inline comments = %d, want 1", len(cs))
	}
	if m.checkPosts != 1 {
		t.Fatalf("check runs created = %d, want 1", m.checkPosts)
	}
	if m.checkBody["conclusion"] != "failure" {
		t.Errorf("check conclusion = %v, want failure (blocking finding)", m.checkBody["conclusion"])
	}
}

// TestPublishCleanPRPassesTheGate proves the no-findings case: a review is still
// posted (saying nothing reproduced) and the check run is success, so a clean
// PR is never blocked.
func TestPublishCleanPRPassesTheGate(t *testing.T) {
	var m mockGitHub
	publishTo(t, &m, nil)

	if m.reviews != 1 {
		t.Fatalf("reviews posted = %d, want 1", m.reviews)
	}
	if cs, _ := m.reviewBody["comments"].([]any); len(cs) != 0 {
		t.Errorf("inline comments = %d, want 0 on a clean PR", len(cs))
	}
	if m.checkPosts != 1 {
		t.Fatalf("check runs created = %d, want 1", m.checkPosts)
	}
	if m.checkBody["conclusion"] != "success" {
		t.Errorf("check conclusion = %v, want success (nothing reproduced)", m.checkBody["conclusion"])
	}
}

// TestPublishAdvisoryFindingDoesNotBlock proves a reproduced but low-impact
// finding posts a comment yet leaves the gate green — verification first, but
// only data-loss/exploitable blocks a merge.
func TestPublishAdvisoryFindingDoesNotBlock(t *testing.T) {
	var m mockGitHub
	publishTo(t, &m, []finding.Finding{{
		ID: "f_2", Type: "contract", Confidence: finding.Reproduced, Impact: finding.Degraded,
		Claim: "returns undefined instead of []", Location: finding.Location{Path: "lib/x.js", Line: 7},
	}})

	if cs, _ := m.reviewBody["comments"].([]any); len(cs) != 1 {
		t.Errorf("inline comments = %d, want 1", len(cs))
	}
	if m.checkBody["conclusion"] != "success" {
		t.Errorf("check conclusion = %v, want success (advisory, not blocking)", m.checkBody["conclusion"])
	}
}
