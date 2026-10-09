// Package budget caps how much Buggies spends reviewing one repository over a
// period. Unattended operation at 3am on every PR is the point (USAGE.md), so
// cost must be bounded per repo or a busy repo (or a loop) can run up the bill.
// When a repo is over budget Buggies skips the review and says so on the PR,
// rather than silently dropping it.
package budget

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Policy is the spend cap. A zero or negative limit means "no cap".
type Policy struct {
	MonthlyLimitCents int
}

// Period is the ledger bucket for t (calendar month, UTC).
func (Policy) Period(t time.Time) string { return t.UTC().Format("2006-01") }

// Ledger records per-repo spend in SQLite (same self-host rationale as the
// queue and finding store: one embedded file, no external service).
type Ledger struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS spend (
  repo TEXT NOT NULL,
  period TEXT NOT NULL,
  cents INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (repo, period)
);
`

// Open opens (creating if needed) the ledger at dsn.
func Open(dsn string) (*Ledger, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("budget: schema: %w", err)
	}
	return &Ledger{db: db}, nil
}

// Close closes the database.
func (l *Ledger) Close() error { return l.db.Close() }

// Add records cents of spend for repo in the period containing at.
func (l *Ledger) Add(ctx context.Context, repo string, cents int, at time.Time) error {
	_, err := l.db.ExecContext(ctx, `
INSERT INTO spend(repo, period, cents) VALUES (?,?,?)
ON CONFLICT(repo, period) DO UPDATE SET cents = cents + excluded.cents`,
		repo, Policy{}.Period(at), cents)
	return err
}

// Spent returns cents spent on repo in the period containing at.
func (l *Ledger) Spent(ctx context.Context, repo string, at time.Time) (int, error) {
	var cents int
	err := l.db.QueryRowContext(ctx, `SELECT COALESCE(cents,0) FROM spend WHERE repo = ? AND period = ?`,
		repo, Policy{}.Period(at)).Scan(&cents)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return cents, err
}

// Allow reports whether a new review of repo may run under p as of now. When
// it may not, note explains why (for the skipped-for-budget PR message).
func (l *Ledger) Allow(ctx context.Context, repo string, p Policy, now time.Time) (allow bool, note string, err error) {
	if p.MonthlyLimitCents <= 0 {
		return true, "", nil
	}
	spent, err := l.Spent(ctx, repo, now)
	if err != nil {
		return false, "", err
	}
	if spent >= p.MonthlyLimitCents {
		return false, fmt.Sprintf("monthly budget reached ($%.2f of $%.2f for %s)",
			float64(spent)/100, float64(p.MonthlyLimitCents)/100, p.Period(now)), nil
	}
	return true, "", nil
}
