package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshblazer/buggies/internal/surfaces/prcomment"
)

// TestPostReviewEmptyCommentsIsArray guards the GitHub review API's contract:
// the "comments" field must be a JSON array, never null. A nil Go slice
// marshals to null, which GitHub rejects with 422 "nil is not an array"; this
// is the zero-finding case the daemon hits on most PRs.
func TestPostReviewEmptyCommentsIsArray(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}
		w.Write([]byte(`{"html_url":"https://example/review"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	if _, err := c.PostReview(context.Background(), pr, "deadbeef", "no findings", nil); err != nil {
		t.Fatalf("PostReview: %v", err)
	}

	v, ok := got["comments"]
	if !ok {
		t.Fatal(`request omitted "comments"`)
	}
	if _, isArray := v.([]any); !isArray {
		t.Fatalf(`"comments" must be a JSON array, got %T (%v)`, v, v)
	}
}

// TestPostReviewSendsComments confirms inline comments are carried through with
// the fields GitHub expects.
func TestPostReviewSendsComments(t *testing.T) {
	var got struct {
		CommitID string `json:"commit_id"`
		Event    string `json:"event"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Side string `json:"side"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("bad JSON: %v", err)
		}
		w.Write([]byte(`{"html_url":"https://example/review"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	in := []prcomment.Comment{{Path: "a.js", Line: 10, Body: "boom"}}
	if _, err := c.PostReview(context.Background(), pr, "sha", "summary", in); err != nil {
		t.Fatalf("PostReview: %v", err)
	}
	if got.CommitID != "sha" || got.Event != "COMMENT" {
		t.Fatalf("unexpected header: commit=%q event=%q", got.CommitID, got.Event)
	}
	if len(got.Comments) != 1 {
		t.Fatalf("want 1 comment, got %d", len(got.Comments))
	}
	if c0 := got.Comments[0]; c0.Path != "a.js" || c0.Line != 10 || c0.Side != "RIGHT" || c0.Body != "boom" {
		t.Fatalf("comment wire shape wrong: %+v", c0)
	}
}
