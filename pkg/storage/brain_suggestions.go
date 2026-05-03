package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Brain-suggestion lifecycle states.
const (
	BrainStatusPending  = "pending"
	BrainStatusAccepted = "accepted"
	BrainStatusRejected = "rejected"
)

// ErrBrainSuggestionNotFound is returned by GetBrainSuggestion when no
// row matches the requested id.
var ErrBrainSuggestionNotFound = errors.New("brain suggestion not found")

// BrainSuggestion is a persisted row from the brain_suggestions table.
//
// Status is one of "pending" | "accepted" | "rejected".
// DecidedAt is nil while Status == "pending".
type BrainSuggestion struct {
	ID          int64
	SuggestedAt time.Time
	Owner       string
	Repo        string
	AfterSHA    string
	Text        string
	Reason      string
	Status      string
	DecidedAt   *time.Time
}

// InsertBrainSuggestion persists a new pending suggestion and returns
// its assigned id.
func (s *Store) InsertBrainSuggestion(ctx context.Context, owner, repo, afterSHA, text, reason string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO brain_suggestions (owner, repo, after_sha, text, reason)
		 VALUES (?, ?, ?, ?, ?)`,
		owner, repo, afterSHA, text, reason)
	if err != nil {
		return 0, fmt.Errorf("insert brain suggestion: %w", err)
	}
	return res.LastInsertId()
}

// ListBrainSuggestions returns suggestions filtered by status (one of
// the BrainStatus* constants). Empty status returns all rows.
// Results are ordered most-recent first.
func (s *Store) ListBrainSuggestions(ctx context.Context, status string) ([]BrainSuggestion, error) {
	const cols = `id, suggested_at, owner, repo, after_sha, text, reason, status, decided_at`
	var (
		rows *sql.Rows
		err  error
	)
	if status == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM brain_suggestions ORDER BY suggested_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM brain_suggestions WHERE status = ? ORDER BY suggested_at DESC`,
			status)
	}
	if err != nil {
		return nil, fmt.Errorf("query brain suggestions: %w", err)
	}
	defer rows.Close()

	var out []BrainSuggestion
	for rows.Next() {
		b, err := scanBrainSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBrainSuggestion fetches a single suggestion by id.
// Returns ErrBrainSuggestionNotFound if no row matches.
func (s *Store) GetBrainSuggestion(ctx context.Context, id int64) (BrainSuggestion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, suggested_at, owner, repo, after_sha, text, reason, status, decided_at
		 FROM brain_suggestions WHERE id = ?`, id)
	b, err := scanBrainSuggestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BrainSuggestion{}, ErrBrainSuggestionNotFound
	}
	if err != nil {
		return BrainSuggestion{}, fmt.Errorf("get brain suggestion %d: %w", id, err)
	}
	return b, nil
}

// DecideBrainSuggestion transitions a pending suggestion to accepted or
// rejected. Calling on a non-pending suggestion returns an error
// describing the current status — protects against double-applies.
//
// Read + write happen in one transaction so the diagnostic status read
// can't observe a different state than the one the UPDATE checked.
// Concurrent decide calls serialize via SQLite's busy_timeout (set on
// the DSN); the loser sees the winner's commit and reports correctly.
func (s *Store) DecideBrainSuggestion(ctx context.Context, id int64, status string) error {
	if status != BrainStatusAccepted && status != BrainStatusRejected {
		return fmt.Errorf("invalid status %q: must be %q or %q", status, BrainStatusAccepted, BrainStatusRejected)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var current string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM brain_suggestions WHERE id = ?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBrainSuggestionNotFound
	}
	if err != nil {
		return fmt.Errorf("get brain suggestion %d: %w", id, err)
	}
	if current != BrainStatusPending {
		return fmt.Errorf("brain suggestion %d already in status %q", id, current)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE brain_suggestions
		 SET status = ?, decided_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		status, id); err != nil {
		return fmt.Errorf("update brain suggestion %d: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit brain suggestion %d: %w", id, err)
	}
	return nil
}

// rowScanner abstracts *sql.Row vs *sql.Rows so scanBrainSuggestion
// can be used by both single-row and multi-row callers.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBrainSuggestion(r rowScanner) (BrainSuggestion, error) {
	var b BrainSuggestion
	var decided sql.NullTime
	if err := r.Scan(&b.ID, &b.SuggestedAt, &b.Owner, &b.Repo, &b.AfterSHA, &b.Text, &b.Reason, &b.Status, &decided); err != nil {
		return BrainSuggestion{}, err
	}
	if decided.Valid {
		t := decided.Time
		b.DecidedAt = &t
	}
	return b, nil
}
