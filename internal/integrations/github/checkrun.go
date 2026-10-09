package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// CheckRun is the subset of a GitHub check run Buggies writes.
type CheckRun struct {
	Name       string
	HeadSHA    string
	Conclusion string // "success" | "failure" | ...
	Title      string
	Summary    string
}

// UpsertCheckRun creates the check run for HeadSHA, or updates the existing one
// of the same name in place, so a re-run never adds a second check. Returns the
// check run id touched.
func (c *Client) UpsertCheckRun(ctx context.Context, pr PR, cr CheckRun) (int64, error) {
	id, err := c.findCheckRun(ctx, pr, cr.HeadSHA, cr.Name)
	if err != nil {
		return 0, err
	}
	body := map[string]any{
		"name":       cr.Name,
		"head_sha":   cr.HeadSHA,
		"status":     "completed",
		"conclusion": cr.Conclusion,
		"output":     map[string]any{"title": cr.Title, "summary": cr.Summary},
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if id == 0 {
		err = c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/check-runs", pr.Owner, pr.Repo), body, &out)
		return out.ID, err
	}
	err = c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/check-runs/%d", pr.Owner, pr.Repo, id), body, &out)
	if out.ID == 0 {
		out.ID = id
	}
	return out.ID, err
}

// findCheckRun returns the id of an existing check run of the given name on
// headSHA, or 0 if none exists.
func (c *Client) findCheckRun(ctx context.Context, pr PR, headSHA, name string) (int64, error) {
	var out struct {
		CheckRuns []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?check_name=%s",
		pr.Owner, pr.Repo, headSHA, url.QueryEscape(name))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return 0, err
	}
	for _, r := range out.CheckRuns {
		if r.Name == name {
			return r.ID, nil
		}
	}
	return 0, nil
}
