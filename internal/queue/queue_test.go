package queue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTmp(t *testing.T) *Queue {
	t.Helper()
	q, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

func enqueueOne(t *testing.T, q *Queue, key string) {
	t.Helper()
	ok, err := q.Enqueue(context.Background(), key, "d-"+key, "o/r", key, []byte("{}"))
	if err != nil || !ok {
		t.Fatalf("enqueue %s: ok=%v err=%v", key, ok, err)
	}
}

func count(t *testing.T, q *Queue, st State) int {
	t.Helper()
	n, err := q.Count(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A handler error retries the job (back to queued, with a future backoff) until
// the attempt budget is spent, then dead-letters it in Failed with the cause.
func TestDrainRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	q := openTmp(t)
	q.SetMaxAttempts(1) // one attempt, then straight to the dead-letter
	enqueueOne(t, q, "h1")

	boom := errors.New("boom: upstream 503")
	err := q.Drain(ctx, func(context.Context, Job) error { return boom })
	if err == nil {
		t.Fatal("Drain should surface the handler error")
	}
	if got := count(t, q, Failed); got != 1 {
		t.Fatalf("Failed = %d, want 1 (dead-lettered after the last attempt)", got)
	}
	if got := count(t, q, Queued); got != 0 {
		t.Fatalf("Queued = %d, want 0", got)
	}
	// The cause is recorded on the dead-lettered job for inspection.
	var id int64
	if err := q.db.QueryRow(`SELECT id FROM jobs WHERE state=?`, string(Failed)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	msg, err := q.LastError(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if msg == "" {
		t.Fatal("dead-lettered job should record last_error")
	}
}

// A retried job is NOT reclaimed on the same (or an immediate next) drain — its
// backoff has not elapsed — so a flapping job cannot spin the worker.
func TestDrainBackoffHoldsRetry(t *testing.T) {
	ctx := context.Background()
	q := openTmp(t)
	q.SetMaxAttempts(5)
	enqueueOne(t, q, "h1")

	calls := 0
	handler := func(context.Context, Job) error { calls++; return errors.New("transient") }

	_ = q.Drain(ctx, handler) // first attempt fails -> requeued with backoff
	if calls != 1 {
		t.Fatalf("first drain: calls = %d, want 1", calls)
	}
	if got := count(t, q, Queued); got != 1 {
		t.Fatalf("Queued = %d, want 1 (requeued, not dead-lettered)", got)
	}
	_ = q.Drain(ctx, handler) // backoff not elapsed -> nothing claimable
	if calls != 1 {
		t.Fatalf("second drain reclaimed too early: calls = %d, want 1", calls)
	}

	// Once the backoff has elapsed (simulated), the job is claimable again.
	if _, err := q.db.Exec(`UPDATE jobs SET next_attempt_at=? WHERE state=?`,
		time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), string(Queued)); err != nil {
		t.Fatal(err)
	}
	_ = q.Drain(ctx, handler)
	if calls != 2 {
		t.Fatalf("after backoff: calls = %d, want 2", calls)
	}
}

// A successful handler completes the job.
func TestDrainCompletes(t *testing.T) {
	ctx := context.Background()
	q := openTmp(t)
	enqueueOne(t, q, "h1")
	if err := q.Drain(ctx, func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := count(t, q, Done); got != 1 {
		t.Fatalf("Done = %d, want 1", got)
	}
}

// A database created before the retry columns existed is migrated in place, and
// its legacy rows (empty next_attempt_at) stay immediately claimable.
func TestMigrateLegacyDB(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE jobs (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  key TEXT NOT NULL UNIQUE, delivery_id TEXT NOT NULL, repo TEXT NOT NULL,
	  head_sha TEXT NOT NULL, payload BLOB, state TEXT NOT NULL DEFAULT 'queued',
	  attempts INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
	INSERT INTO jobs(key,delivery_id,repo,head_sha,payload,state,attempts,created_at,updated_at)
	VALUES ('legacy','d','o/r','sha','{}','queued',0,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()

	q, err := Open(dsn) // runs migrate()
	if err != nil {
		t.Fatalf("Open/migrate legacy db: %v", err)
	}
	defer q.Close()

	j, ok, err := q.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("legacy row not claimable after migrate: ok=%v err=%v", ok, err)
	}
	if j.Key != "legacy" {
		t.Fatalf("claimed %q, want legacy", j.Key)
	}
}
