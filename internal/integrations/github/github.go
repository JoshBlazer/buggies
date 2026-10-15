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
	"strings"
	"time"

	"github.com/joshblazer/buggies/internal/surfaces/prcomment"
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
	}{CommitID: headSHA, Body: summary, Event: "COMMENT", Comments: []ghComment{}}
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
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = b
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}

	// GitHub throttles with 429 (primary rate limit), 403 (secondary rate
	// limit), and the occasional 5xx. A single Do would turn any of those into a
	// permanently-failed job; retry a few times with backoff (honouring
	// Retry-After) so a transient throttle does not lose a review. The body is
	// kept as bytes so each attempt can send a fresh reader.
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var rb io.Reader
		if body != nil {
			rb = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, rb)
		if err != nil {
			return err
		}
		// With a PAT the token is set here; a GitHub App client leaves Token
		// empty and lets its installation-token transport inject Authorization.
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			if attempt == maxAttempts || !backoffSleep(ctx, attempt, 0) {
				return err
			}
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			if out != nil && len(raw) > 0 {
				return json.Unmarshal(raw, out)
			}
			return nil
		}
		lastErr = fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(raw))
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5 ||
			(resp.StatusCode == http.StatusForbidden && bytes.Contains(bytes.ToLower(raw), []byte("rate limit")))
		if !retryable || attempt == maxAttempts || !backoffSleep(ctx, attempt, retryAfter(resp.Header)) {
			return lastErr
		}
	}
	return lastErr
}

// retryAfter reads a Retry-After header expressed in seconds, if present.
func retryAfter(h http.Header) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// backoffSleep waits before the next attempt — the server's Retry-After when it
// gave one, otherwise exponential (0.5s, 1s, 2s…) capped at 8s. It returns
// false if the context is cancelled while waiting, so a shutdown is not stalled.
func backoffSleep(ctx context.Context, attempt int, retry time.Duration) bool {
	d := retry
	if d <= 0 {
		d = time.Duration(250*(1<<attempt)) * time.Millisecond
		if d > 8*time.Second {
			d = 8 * time.Second
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
