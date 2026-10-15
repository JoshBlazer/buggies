package store

import (
	"context"
	"time"

	"github.com/joshblazer/kernel/finding"
)

// Transition is one row of a finding's lifecycle audit trail. The write path
// (Store.Transition) already records these into the transitions table; this is
// the matching read path the admin API needs for a finding's history.
type Transition struct {
	From   finding.State
	To     finding.State
	Reason string
	At     time.Time
}

// ListTransitions returns a finding's lifecycle history, oldest first. An
// unknown id yields an empty slice, not an error: a finding with no recorded
// transitions is a normal state, not a failure.
func (s *Store) ListTransitions(ctx context.Context, id string) ([]Transition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_state, to_state, reason, at FROM transitions WHERE id = ? ORDER BY at ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transition
	for rows.Next() {
		var (
			from, to, reason, at string
		)
		if err := rows.Scan(&from, &to, &reason, &at); err != nil {
			return nil, err
		}
		t := Transition{From: finding.State(from), To: finding.State(to), Reason: reason}
		t.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, t)
	}
	return out, rows.Err()
}
