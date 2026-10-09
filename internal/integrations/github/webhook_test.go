package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "it's a secret to everybody"

// recordedDelivery is a trimmed but real-shaped pull_request "synchronize"
// payload, as GitHub would send it.
const recordedDelivery = `{
  "action": "synchronize",
  "number": 42,
  "pull_request": {
    "head": { "sha": "9f28ce3", "repo": { "full_name": "tj/commander.js", "fork": false } },
    "base": { "sha": "ba6d13d" }
  },
  "repository": { "name": "commander.js", "owner": { "login": "tj" } },
  "installation": { "id": 7654321 }
}`

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func post(t *testing.T, h http.Handler, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "d-123")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWebhookSignedDeliveryYieldsJob(t *testing.T) {
	var got PRJob
	seen := 0
	h := &WebhookHandler{
		Secret: []byte(testSecret),
		OnPR: func(_ context.Context, job PRJob) error {
			got = job
			seen++
			return nil
		},
	}

	rec := post(t, h, recordedDelivery, sign(testSecret, recordedDelivery))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if seen != 1 {
		t.Fatalf("OnPR called %d times, want 1", seen)
	}
	want := PRJob{
		DeliveryID: "d-123", Action: "synchronize", Owner: "tj", Repo: "commander.js",
		Number: 42, HeadSHA: "9f28ce3", BaseSHA: "ba6d13d", InstallationID: 7654321, Fork: false,
	}
	if got != want {
		t.Fatalf("job = %+v\nwant %+v", got, want)
	}
}

func TestWebhookTamperedSignatureRejected(t *testing.T) {
	seen := 0
	h := &WebhookHandler{
		Secret: []byte(testSecret),
		OnPR:   func(context.Context, PRJob) error { seen++; return nil },
	}

	// Valid signature over the original, but the body was tampered in flight.
	tampered := strings.Replace(recordedDelivery, `"number": 42`, `"number": 99`, 1)
	rec := post(t, h, tampered, sign(testSecret, recordedDelivery))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if seen != 0 {
		t.Fatalf("OnPR ran %d times on a bad signature, want 0", seen)
	}
}

func TestWebhookNonReviewableActionAckedNotDispatched(t *testing.T) {
	seen := 0
	h := &WebhookHandler{
		Secret: []byte(testSecret),
		OnPR:   func(context.Context, PRJob) error { seen++; return nil },
	}
	body := strings.Replace(recordedDelivery, `"action": "synchronize"`, `"action": "labeled"`, 1)

	rec := post(t, h, body, sign(testSecret, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ack-and-drop)", rec.Code)
	}
	if seen != 0 {
		t.Fatalf("OnPR ran on a non-reviewable action %d times, want 0", seen)
	}
}

func TestVerifySignatureRejectsEmptySecretAndJunk(t *testing.T) {
	if VerifySignature(nil, []byte("x"), sign(testSecret, "x")) {
		t.Error("empty secret must never verify")
	}
	if VerifySignature([]byte(testSecret), []byte("x"), "not-a-sig") {
		t.Error("malformed header must not verify")
	}
}
