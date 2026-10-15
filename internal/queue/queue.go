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

// DefaultMaxAttempts is how many times a job is tried before it is
// dead-lettered (left in Failed with its last error recorded). A transient
// fault — GitHub API blip, model timeout, a briefly-down sandbox — is retried
// with exponential backoff; a job that fails this many times is almost
// certainly a real defect, not a blip, and parking it stops a poison job from
// burning budget forever.
const DefaultMaxAttempts = 5

// Queue is the SQLite-backed store.
type Queue struct {
	db          *sql.DB
	maxAttempts int
}

// SetMaxAttempts overrides the dead-letter threshold (tests use a small value).
func (q *Queue) SetMaxAttempts(n int) {
	if n > 0 {
		q.maxAttempts = n
	}
}

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
  next_attempt_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("queue: migrate: %w", err)
	}
	return &Queue{db: db, maxAttempts: DefaultMaxAttempts}, nil
}

// migrate adds columns introduced after the first release to an already-created
// database. SQLite has no ADD COLUMN IF NOT EXISTS, so the catalog is consulted
// first. Existing rows get the column default (” for both), and an empty
// next_attempt_at sorts before any RFC3339 timestamp, so legacy jobs stay
// immediately claimable — the upgrade needs no backfill.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(jobs)`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, c := range []struct{ name, ddl string }{
		{"next_attempt_at", `next_attempt_at TEXT NOT NULL DEFAULT ''`},
		{"last_error", `last_error TEXT NOT NULL DEFAULT ''`},
	} {
		if have[c.name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN ` + c.ddl); err != nil {
			return err
		}
	}
	// The claim index references next_attempt_at, so it is created only after
	// the column is guaranteed to exist (fresh DB or migrated legacy one).
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(state, next_attempt_at, id)`)
	return err
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
INSERT INTO jobs(key, delivery_id, repo, head_sha, payload, state, attempts, next_attempt_at, last_error, created_at, updated_at)
VALUES (?,?,?,?,?,?,0,?,'',?,?)
ON CONFLICT(key) DO NOTHING`,
		key, deliveryID, repo, headSHA, payload, string(Queued), now, now, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Claim atomically takes the oldest queued job whose backoff has elapsed and
// marks it running. ok is false when nothing is claimable (the queue is empty,
// or every queued job is still waiting out its retry backoff).
func (q *Queue) Claim(ctx context.Context) (Job, bool, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	var j Job
	var state string
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err = tx.QueryRowContext(ctx, `
SELECT id, key, delivery_id, repo, head_sha, payload, state, attempts
FROM jobs WHERE state = ? AND next_attempt_at <= ? ORDER BY id LIMIT 1`, string(Queued), now).
		Scan(&j.ID, &j.Key, &j.DeliveryID, &j.Repo, &j.HeadSHA, &j.Payload, &state, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
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

// backoff is the delay before a job's next attempt, given how many attempts it
// has already had (>=1): 1, 2, 4, 8, 16 min, capped at an hour. Enough to ride
// out a transient outage without hammering a flapping dependency.
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts >= 7 {
		return time.Hour
	}
	d := time.Minute << (attempts - 1)
	if d > time.Hour {
		return time.Hour
	}
	return d
}

// retryOrFail reschedules a job that errored. While it has attempts left it
// goes back to Queued with next_attempt_at in the future (exponential backoff)
// and its error recorded; once it has used maxAttempts it is dead-lettered
// (left in Failed with last_error) so a poison job stops consuming budget.
func (q *Queue) retryOrFail(ctx context.Context, j Job, cause error) (requeued bool, err error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	if j.Attempts < q.maxAttempts {
		next := now.Add(backoff(j.Attempts)).Format(time.RFC3339Nano)
		_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state=?, next_attempt_at=?, last_error=?, updated_at=? WHERE id=?`,
			string(Queued), next, msg, nowStr, j.ID)
		return true, err
	}
	_, err = q.db.ExecContext(ctx, `UPDATE jobs SET state=?, last_error=?, updated_at=? WHERE id=?`,
		string(Failed), msg, nowStr, j.ID)
	return false, err
}

// LastError returns the recorded failure message for a job (empty if none),
// for the dashboard and for dead-letter inspection.
func (q *Queue) LastError(ctx context.Context, id int64) (string, error) {
	var msg string
	err := q.db.QueryRowContext(ctx, `SELECT last_error FROM jobs WHERE id=?`, id).Scan(&msg)
	return msg, err
}

// Drain claims and runs claimable jobs until none remain. A handler error does
// not stop the drain or lose the job: the job is retried with backoff, or
// dead-lettered once it is out of attempts, and the drain moves on to the next
// job (a retried job is not reclaimed this pass — its backoff has not elapsed).
// The first handler error is returned so the caller can log it.
func (q *Queue) Drain(ctx context.Context, handler func(context.Context, Job) error) error {
	var firstErr error
	for {
		j, ok, err := q.Claim(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return firstErr
		}
		if herr := handler(ctx, j); herr != nil {
			if _, rerr := q.retryOrFail(ctx, j, herr); rerr != nil {
				return rerr
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("queue: job %d (%s): %w", j.ID, j.Key, herr)
			}
			continue
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
