package storage

import (
	"context"
	"fmt"
)

// HasReviewed reports whether a review has already been posted for the
// given (owner, repo, after_sha) tuple.
func (s *Store) HasReviewed(ctx context.Context, owner, repo, afterSHA string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reviews WHERE owner = ? AND repo = ? AND after_sha = ?`,
		owner, repo, afterSHA,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("HasReviewed query: %w", err)
	}
	return n > 0, nil
}

// MarkReviewed inserts an idempotency row for the given push head.
// checkRunID may be 0; it lands as SQL NULL so ad-hoc queries can
// distinguish "unknown" from a real Check Run id.
//
// Important ordering note for callers: complete the Check Run BEFORE
// calling MarkReviewed. The lesser evil between (a) marked-but-not-posted
// (silent failure on retry) and (b) posted-but-not-marked (duplicate on
// retry) is (b). Don't reorder this.
func (s *Store) MarkReviewed(ctx context.Context, owner, repo, afterSHA string, checkRunID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO reviews (owner, repo, after_sha, check_run_id) VALUES (?, ?, ?, ?)`,
		owner, repo, afterSHA, nullIfZero(checkRunID),
	)
	if err != nil {
		return fmt.Errorf("MarkReviewed insert: %w", err)
	}
	return nil
}
