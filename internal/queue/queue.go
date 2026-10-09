// Package queue is a durable, embedded job queue on SQLite. It sits between
// the GitHub webhook (which must ack fast) and the review pipeline (which is
// slow), so a crash mid-review loses nothing: the job stays claimable. It is
// SQLite, not Redis/SQS, because Buggies must self-host as a single binary
// (STACK.md) — no broker only the vendor can run.
package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// State is a job's lifecycle position.
type State string

const (
	Queued  State = "queued"
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"
)

// Job is one unit of durable work.
type Job struct {
	ID         int64
	Key        string // idempotency key; unique across the queue
	DeliveryID string
	Repo       string
	HeadSHA    string
	Payload    []byte
	State      State
	Attempts   int
}

// Queue is the SQLite-backed store.
type Queue struct{ db *sql.DB }

const schema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  key TEXT NOT NULL UNIQUE,
  delivery_id TEXT NOT NULL,
  repo TEXT NOT NULL,
  head_sha TEXT NOT NULL,
  payload BLOB,
  state TEXT NOT NULL DEFAULT 'queued',
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS jobs_state ON jobs(state, id);
`

// Open opens (creating if needed) the queue database at dsn.
func Open(dsn string) (*Queue, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialize; claim is a read-then-write
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("queue: schema: %w", err)
	}
	return &Queue{db: db}, nil
}

// Close closes the database.
func (q *Queue) Close() error { return q.db.Close() }

// ReviewKey is the idempotency key for reviewing one PR head. A redelivered
// webhook or a second push to the same head produces the same key, so the
// review runs once. A new push (new head) is a different key and re-reviews.
func ReviewKey(repo, headSHA string) string { return repo + "@" + headSHA }

// Enqueue inserts a job under key. enqueued is false (and no row is added) when
// a job with that key already exists in any state — that is the idempotency
// guarantee the webhook relies on.
func (q *Queue) Enqueue(ctx context.Context, key, deliveryID, repo, headSHA string, payload []byte) (enqueued bool, err error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := q.db.ExecContext(ctx, `
INSERT INTO jobs(key, delivery_id, repo, head_sha, payload, state, attempts, created_at, updated_at)
VALUES (?,?,?,?,?,?,0,?,?)
ON CONFLICT(key) DO NOTHING`,
		key, deliveryID, repo, headSHA, payload, string(Queued), now, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Claim atomically takes the oldest queued job and marks it running. ok is
// false when nothing is queued.
func (q *Queue) Claim(ctx context.Context) (Job, bool, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	var j Job
	var state string
	err = tx.QueryRowContext(ctx, `
SELECT id, key, delivery_id, repo, head_sha, payload, state, attempts
FROM jobs WHERE state = ? ORDER BY id LIMIT 1`, string(Queued)).
		Scan(&j.ID, &j.Key, &j.DeliveryID, &j.Repo, &j.HeadSHA, &j.Payload, &state, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, attempts=attempts+1, updated_at=? WHERE id=?`,
		string(Running), now, j.ID); err != nil {
		return Job{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	j.State, j.Attempts = Running, j.Attempts+1
	return j, true, nil
}

func (q *Queue) setState(ctx context.Context, id int64, st State) error {
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET state=?, updated_at=? WHERE id=?`,
		string(st), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// Complete marks a claimed job done.
func (q *Queue) Complete(ctx context.Context, id int64) error { return q.setState(ctx, id, Done) }

// Fail marks a job failed, or requeues it for another attempt.
func (q *Queue) Fail(ctx context.Context, id int64, requeue bool) error {
	if requeue {
		return q.setState(ctx, id, Queued)
	}
	return q.setState(ctx, id, Failed)
}

// Drain claims and runs jobs until the queue is empty. A handler error fails
// that job (no requeue) and is returned; remaining jobs are left queued.
func (q *Queue) Drain(ctx context.Context, handler func(context.Context, Job) error) error {
	for {
		j, ok, err := q.Claim(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := handler(ctx, j); err != nil {
			_ = q.Fail(ctx, j.ID, false)
			return fmt.Errorf("queue: job %d (%s): %w", j.ID, j.Key, err)
		}
		if err := q.Complete(ctx, j.ID); err != nil {
			return err
		}
	}
}

// Count returns how many jobs are in a given state (for tests and metrics).
func (q *Queue) Count(ctx context.Context, st State) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state = ?`, string(st)).Scan(&n)
	return n, err
}
