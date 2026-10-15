package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// PRJob is the unit of work a pull_request webhook produces: everything the
// pipeline needs to review one PR, with no further GitHub lookup required to
// start. Fork is true when the head repository differs from the base — those
// PRs are reviewed without secrets (enforced in M10).
type PRJob struct {
	DeliveryID     string
	Action         string
	Owner          string
	Repo           string
	Number         int
	HeadSHA        string
	BaseSHA        string
	InstallationID int64
	Fork           bool
}

// reviewableActions are the pull_request actions that warrant a (re-)review.
// Everything else (labeled, assigned, closed, ...) is ignored.
var reviewableActions = map[string]bool{
	"opened":           true,
	"reopened":         true,
	"synchronize":      true, // a new push to the PR head
	"ready_for_review": true,
}

// VerifySignature reports whether sigHeader (the X-Hub-Signature-256 value,
// "sha256=<hex>") is a valid HMAC-SHA256 of body under secret. The comparison
// is constant-time. An empty secret or malformed header is never valid.
func VerifySignature(secret, body []byte, sigHeader string) bool {
	if len(secret) == 0 {
		return false
	}
	want, ok := strings.CutPrefix(sigHeader, "sha256=")
	if !ok {
		return false
	}
	wantMAC, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(wantMAC, mac.Sum(nil))
}

// prEvent is the subset of the pull_request webhook payload Buggies consumes.
type prEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Head struct {
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
				Fork     bool   `json:"fork"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// ParsePullRequest turns a pull_request payload into a PRJob. ok is false when
// the action is not one Buggies reviews (the caller should 200 and move on).
func ParsePullRequest(body []byte) (job PRJob, ok bool, err error) {
	var e prEvent
	if err := json.Unmarshal(body, &e); err != nil {
		return PRJob{}, false, fmt.Errorf("github: malformed pull_request payload: %w", err)
	}
	if !reviewableActions[e.Action] {
		return PRJob{}, false, nil
	}
	if e.Repository.Owner.Login == "" || e.Repository.Name == "" || e.Number == 0 {
		return PRJob{}, false, fmt.Errorf("github: pull_request payload missing repo/number")
	}
	// A PR is untrusted ("fork") when its head is a different repository than
	// the base — the case where a stranger's code reaches us. We do NOT use
	// Head.Repo.Fork: that flag is true whenever the base repo is itself a
	// GitHub fork of some upstream, which says nothing about whether THIS PR
	// crosses a repo boundary, and would wrongly flag the owner's own
	// branch->master PRs as hostile. Fail closed when the head repo is absent
	// (GitHub sends head.repo = null for a deleted fork): if we cannot confirm
	// head and base are the same repo, treat it as untrusted.
	base := e.Repository.Owner.Login + "/" + e.Repository.Name
	fork := e.PullRequest.Head.Repo.FullName == "" ||
		e.PullRequest.Head.Repo.FullName != base
	return PRJob{
		Action:         e.Action,
		Owner:          e.Repository.Owner.Login,
		Repo:           e.Repository.Name,
		Number:         e.Number,
		HeadSHA:        e.PullRequest.Head.SHA,
		BaseSHA:        e.PullRequest.Base.SHA,
		InstallationID: e.Installation.ID,
		Fork:           fork,
	}, true, nil
}

// ParseUninstall extracts the repositories a tenant has removed from Buggies,
// from an `installation` (action "deleted" — the whole app uninstalled) or an
// `installation_repositories` (action "removed" — specific repos dropped)
// event. ok is false for any other event or action. These are the signals that
// a tenant's data should be purged: GitHub no longer authorizes Buggies for
// that code, so neither should Buggies keep its findings.
func ParseUninstall(eventType string, body []byte) (repos []string, ok bool, err error) {
	var e struct {
		Action       string `json:"action"`
		Repositories []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
		RepositoriesRemoved []struct {
			FullName string `json:"full_name"`
		} `json:"repositories_removed"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, false, fmt.Errorf("github: malformed %s payload: %w", eventType, err)
	}
	var src []struct {
		FullName string `json:"full_name"`
	}
	switch {
	case eventType == "installation" && e.Action == "deleted":
		src = e.Repositories
	case eventType == "installation_repositories" && e.Action == "removed":
		src = e.RepositoriesRemoved
	default:
		return nil, false, nil
	}
	for _, r := range src {
		if r.FullName != "" {
			repos = append(repos, r.FullName)
		}
	}
	return repos, true, nil
}

// WebhookHandler verifies and dispatches GitHub webhooks. Secret is the App's
// webhook secret. OnPR receives every reviewable PR; OnUninstall (optional)
// receives the repositories a tenant removed, so their data can be purged.
// Returning an error from a handler yields 500 so GitHub redelivers. Other
// events and non-reviewable actions are acknowledged with 200 and dropped.
type WebhookHandler struct {
	Secret      []byte
	OnPR        func(ctx context.Context, job PRJob) error
	OnUninstall func(ctx context.Context, repos []string) error
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	if !VerifySignature(h.Secret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	ev := r.Header.Get("X-GitHub-Event")
	switch ev {
	case "pull_request":
		job, ok, err := ParsePullRequest(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusOK) // reviewable? no — ack and drop
			return
		}
		job.DeliveryID = r.Header.Get("X-GitHub-Delivery")
		if h.OnPR != nil {
			if err := h.OnPR(r.Context(), job); err != nil {
				http.Error(w, "handler error", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	case "installation", "installation_repositories":
		repos, ok, err := ParseUninstall(ev, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if ok && len(repos) > 0 && h.OnUninstall != nil {
			if err := h.OnUninstall(r.Context(), repos); err != nil {
				http.Error(w, "handler error", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK) // ack-and-ignore other events
	}
}
