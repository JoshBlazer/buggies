package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/joshblazer/kernel/finding"
)

// Store is a finding.Store on SQLite. Only the columns needed to filter and
// rank are stored in the clear; the finding itself — claim, location detail,
// the failing test, the fix — is an AES-256-GCM sealed blob.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

var _ finding.Store = (*Store)(nil)

const schema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS findings (
  id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  path TEXT NOT NULL,
  type TEXT NOT NULL,
  confidence INTEGER NOT NULL,
  impact INTEGER NOT NULL,
  state TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  blob BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS findings_repo ON findings(repo, path);
CREATE TABLE IF NOT EXISTS transitions (
  id TEXT NOT NULL,
  from_state TEXT NOT NULL,
  to_state TEXT NOT NULL,
  reason TEXT NOT NULL,
  at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS transitions_id ON transitions(id);
`

// Open opens the database at dsn. The encryption key comes from
// BUGGIES_STORE_KEY (64 hex chars) or, failing that, from a 0600 key file
// beside the database, generated on first use. Losing the key loses the
// findings — that is the intended failure mode for a breach target.
func Open(dsn string) (*Store, error) {
	key, err := loadKey(dsn)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	return &Store{db: db, aead: aead}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func loadKey(dsn string) ([]byte, error) {
	if h := os.Getenv("BUGGIES_STORE_KEY"); h != "" {
		k, err := hex.DecodeString(strings.TrimSpace(h))
		if err != nil || len(k) != 32 {
			return nil, errors.New("store: BUGGIES_STORE_KEY must be 64 hex characters")
		}
		return k, nil
	}
	if dsn == ":memory:" {
		k := make([]byte, 32)
		_, err := rand.Read(k)
		return k, err
	}
	kf := filepath.Join(filepath.Dir(dsn), ".buggies-store.key")
	if b, err := os.ReadFile(kf); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("store: key file %s is corrupt", kf)
		}
		return k, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(kf), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(kf, []byte(hex.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) seal(f finding.Finding) ([]byte, error) {
	plain, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	// The ID is additional authenticated data so a blob cannot be swapped
	// between rows.
	return append(nonce, s.aead.Seal(nil, nonce, plain, []byte(f.ID))...), nil
}

func (s *Store) open(id string, blob []byte) (finding.Finding, error) {
	var f finding.Finding
	n := s.aead.NonceSize()
	if len(blob) < n {
		return f, errors.New("store: blob too short")
	}
	plain, err := s.aead.Open(nil, blob[:n], blob[n:], []byte(id))
	if err != nil {
		return f, fmt.Errorf("store: cannot decrypt %s: %w", id, err)
	}
	return f, json.Unmarshal(plain, &f)
}

// Put implements finding.Store. Re-putting an existing ID updates it in
// place and preserves CreatedAt and any lifecycle state that has moved past
// the incoming one — a re-run never demotes a dismissed or fixed finding.
func (s *Store) Put(ctx context.Context, f finding.Finding) error {
	now := time.Now().UTC()
	if f.ID == "" {
		return errors.New("store: finding has no ID")
	}
	existing, err := s.Get(ctx, f.ID)
	switch {
	case err == nil:
		f.CreatedAt = existing.CreatedAt
		if rank(existing.State) > rank(f.State) {
			f.State = existing.State
		}
	case errors.Is(err, finding.ErrNotFound):
		if f.CreatedAt.IsZero() {
			f.CreatedAt = now
		}
	default:
		return err
	}
	f.UpdatedAt = now
	blob, err := s.seal(f)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO findings(id, repo, path, type, confidence, impact, state, created_at, updated_at, blob)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET repo=excluded.repo, path=excluded.path, type=excluded.type,
  confidence=excluded.confidence, impact=excluded.impact, state=excluded.state,
  updated_at=excluded.updated_at, blob=excluded.blob`,
		f.ID, f.Location.Repo, f.Location.Path, string(f.Type), int(f.Confidence), int(f.Impact), string(f.State),
		f.CreatedAt.Format(time.RFC3339Nano), f.UpdatedAt.Format(time.RFC3339Nano), blob)
	return err
}

// rank orders lifecycle states so a re-run cannot move a finding backwards.
func rank(st finding.State) int {
	switch st {
	case finding.Predicted:
		return 0
	case finding.Verified:
		return 1
	case finding.ShippedAnyway, finding.Dismissed:
		return 2
	case finding.Manifested:
		return 3
	case finding.Attributed:
		return 4
	case finding.Fixed:
		return 5
	case finding.Closed:
		return 6
	}
	return 0
}

// Get implements finding.Store.
func (s *Store) Get(ctx context.Context, id string) (finding.Finding, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, `SELECT blob FROM findings WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return finding.Finding{}, finding.ErrNotFound
	}
	if err != nil {
		return finding.Finding{}, err
	}
	return s.open(id, blob)
}

// Transition implements finding.Store.
func (s *Store) Transition(ctx context.Context, id string, to finding.State, reason string) error {
	f, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	from := f.State
	f.State = to
	f.UpdatedAt = time.Now().UTC()
	blob, err := s.seal(f)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET state = ?, updated_at = ?, blob = ? WHERE id = ?`,
		string(to), f.UpdatedAt.Format(time.RFC3339Nano), blob, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO transitions(id, from_state, to_state, reason, at) VALUES (?,?,?,?,?)`,
		id, string(from), string(to), reason, f.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// List implements finding.Store: verification first, severity second.
func (s *Store) List(ctx context.Context, q finding.Query) ([]finding.Finding, error) {
	var where []string
	var args []any
	if q.Repo != "" {
		where, args = append(where, "repo = ?"), append(args, q.Repo)
	}
	if q.Path != "" {
		where, args = append(where, "path = ?"), append(args, q.Path)
	}
	if q.Type != "" {
		where, args = append(where, "type = ?"), append(args, string(q.Type))
	}
	if q.State != "" {
		where, args = append(where, "state = ?"), append(args, string(q.State))
	}
	if q.MinConfidence > 0 {
		where, args = append(where, "confidence >= ?"), append(args, int(q.MinConfidence))
	}
	sqlq := `SELECT id, blob FROM findings`
	if len(where) > 0 {
		sqlq += " WHERE " + strings.Join(where, " AND ")
	}
	sqlq += ` ORDER BY confidence DESC, impact DESC, updated_at DESC`
	if q.Limit > 0 {
		sqlq += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []finding.Finding
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		f, err := s.open(id, blob)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DismissalRate returns dismissed/total decided findings of a type in a
// repo, feeding the self-throttle rule in package policy.
func (s *Store) DismissalRate(ctx context.Context, repo string, typ finding.Type) (dismissed, total int, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0), COUNT(*)
FROM findings WHERE repo = ? AND type = ? AND state IN (?,?,?,?)`,
		string(finding.Dismissed), repo, string(typ),
		string(finding.Dismissed), string(finding.Fixed), string(finding.Closed), string(finding.ShippedAnyway)).
		Scan(&dismissed, &total)
	return
}
