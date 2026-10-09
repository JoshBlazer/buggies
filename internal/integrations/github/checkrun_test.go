package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCheckRunUpsertUpdatesInPlace is the M8 exit check: posting a check run
// and then re-running on the same head produces one check run, updated not
// duplicated.
func TestCheckRunUpsertUpdatesInPlace(t *testing.T) {
	var creates, updates int
	exists := false
	const id = 555

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			if exists {
				json.NewEncoder(w).Encode(map[string]any{
					"check_runs": []map[string]any{{"id": id, "name": "buggies"}},
				})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{}})
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/check-runs"):
			creates++
			exists = true
			json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/check-runs/"):
			updates++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["conclusion"] != "failure" {
				t.Errorf("update conclusion = %v, want failure", body["conclusion"])
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &Client{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	pr := PR{Owner: "tj", Repo: "commander.js", Number: 42}
	cr := CheckRun{Name: "buggies", HeadSHA: "9f28ce3", Conclusion: "failure", Title: "1 blocking", Summary: "x"}

	// First run: create.
	if _, err := c.UpsertCheckRun(context.Background(), pr, cr); err != nil {
		t.Fatal(err)
	}
	// Re-run on the same head: update in place.
	if _, err := c.UpsertCheckRun(context.Background(), pr, cr); err != nil {
		t.Fatal(err)
	}

	if creates != 1 {
		t.Errorf("creates = %d, want 1", creates)
	}
	if updates != 1 {
		t.Errorf("updates = %d, want 1 (re-run must update, not duplicate)", updates)
	}
}
