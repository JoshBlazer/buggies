package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// These read/write helpers back the admin JSON API (internal/api). The rows all
// already exist; Count/Claim/Enqueue in queue.go never exposed a plain list, a
// fetch-by-id, a requeue, or the set of distinct repos the queue has seen.

// ErrNotFound is returned when no job has the given id.
var ErrNotFound = errors.New("queue: job not found")

// ErrJobRunning is returned by Requeue when the job is mid-review: requeuing it
// would let a second worker pick it up while the first is still on it.
var ErrJobRunning = errors.New("queue: job is running")

// Record is a job row with its timestamps, for the read API. The queue's own
// hot path (Claim) does not need created_at/updated_at, so Job omits them;
// Record carries them for display.
type Record struct {
	Job
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Filter selects jobs for List. Zero fields mean "any".
type Filter struct {
	State  State
	Repo   string
	Limit  int // 0 = no limit
	Offset int
}

const recordCols = `id, key, delivery_id, repo, head_sha, payload, state, attempts, created_at, updated_at`

func scanRecord(sc interface{ Scan(...any) error }) (Record, error) {
	var (
		r                    Record
		state                string
		createdAt, updatedAt string
	)
	if err := sc.Scan(&r.ID, &r.Key, &r.DeliveryID, &r.Repo, &r.HeadSHA, &r.Payload, &state, &r.Attempts, &createdAt, &updatedAt); err != nil {
		return Record{}, err
	}
	r.State = State(state)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return r, nil
}

// List returns jobs matching f, newest first, plus the total number of rows
// that match the filter (ignoring Limit/Offset) so the caller can paginate.
func (q *Queue) List(ctx context.Context, f Filter) ([]Record, int, error) {
	var where []string
	var args []any
	if f.State != "" {
		where, args = append(where, "state = ?"), append(args, string(f.State))
	}
	if f.Repo != "" {
		where, args = append(where, "repo = ?"), append(args, f.Repo)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := q.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sqlq := "SELECT " + recordCols + " FROM jobs" + clause + " ORDER BY id DESC"
	switch {
	case f.Limit > 0:
		sqlq += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	case f.Offset > 0:
		sqlq += fmt.Sprintf(" LIMIT -1 OFFSET %d", f.Offset)
	}
	rows, err := q.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// GetByID returns one job by its numeric id. ErrNotFound when absent.
func (q *Queue) GetByID(ctx context.Context, id int64) (Record, error) {
	r, err := scanRecord(q.db.QueryRowContext(ctx, "SELECT "+recordCols+" FROM jobs WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return r, err
}

// Requeue flips a reviewed job's state back to 'queued' so the next drain
// re-reviews that PR head. ErrNotFound when the id is unknown; ErrJobRunning
// when the job is currently running (requeuing it would double-review). A job
// already queued is left as-is (idempotent).
func (q *Queue) Requeue(ctx context.Context, id int64) error {
	var state string
	err := q.db.QueryRowContext(ctx, "SELECT state FROM jobs WHERE id = ?", id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if State(state) == Running {
		return ErrJobRunning
	}
	if State(state) == Queued {
		return nil
	}
	_, err = q.db.ExecContext(ctx, "UPDATE jobs SET state = ?, updated_at = ? WHERE id = ?",
		string(Queued), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// Repos returns the distinct repositories (owner/repo) the queue has seen, in
// lexical order. It is the installations/repos enumerator the dashboard joins
// against the on-disk per-repo finding stores.
func (q *Queue) Repos(ctx context.Context) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT DISTINCT repo FROM jobs ORDER BY repo")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return nil, err
		}
		out = append(out, repo)
	}
	return out, rows.Err()
}
