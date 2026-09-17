package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/buggies-dev/buggies/internal/surfaces/prcomment"
)

// Client posts reviews with a personal access token (v0). The GitHub App
// flow replaces the token with an installation token; the request shape is
// identical.
type Client struct {
	Token   string
	BaseURL string // default https://api.github.com
	HTTP    *http.Client
}

// FromEnv reads GITHUB_TOKEN.
func FromEnv() (*Client, error) {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN is not set")
	}
	return &Client{Token: tok}, nil
}

// PR identifies a pull request.
type PR struct {
	Owner, Repo string
	Number      int
}

var prRef = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)#(\d+)$`)

// ParsePR parses "owner/repo#123".
func ParsePR(s string) (PR, error) {
	m := prRef.FindStringSubmatch(s)
	if m == nil {
		return PR{}, fmt.Errorf("expected owner/repo#number, got %q", s)
	}
	n, _ := strconv.Atoi(m[3])
	return PR{Owner: m[1], Repo: m[2], Number: n}, nil
}

// Head returns the PR's head SHA and base ref, so the CLI can check out
// exactly what will be reviewed.
func (c *Client) Head(ctx context.Context, pr PR) (headSHA, baseSHA string, err error) {
	var out struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number), nil, &out); err != nil {
		return "", "", err
	}
	return out.Head.SHA, out.Base.SHA, nil
}

// PostReview submits one review with inline comments on the head commit.
func (c *Client) PostReview(ctx context.Context, pr PR, headSHA, summary string, comments []prcomment.Comment) (string, error) {
	type ghComment struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	}
	body := struct {
		CommitID string      `json:"commit_id"`
		Body     string      `json:"body"`
		Event    string      `json:"event"`
		Comments []ghComment `json:"comments"`
	}{CommitID: headSHA, Body: summary, Event: "COMMENT"}
	for _, cm := range comments {
		body.Comments = append(body.Comments, ghComment{Path: cm.Path, Line: cm.Line, Side: "RIGHT", Body: cm.Body})
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", pr.Owner, pr.Repo, pr.Number), body, &out)
	return out.HTMLURL, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	var rb io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rb = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rb)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(raw))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
