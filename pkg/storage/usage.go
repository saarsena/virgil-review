package storage

import (
	"context"
	"fmt"
)

// UsageRecord is one row in the usage table.
//
// Owner/Repo/AfterSHA are optional. Empty strings are stored as SQL NULL
// to keep ad-hoc queries clean.
type UsageRecord struct {
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	Owner               string
	Repo                string
	AfterSHA            string
}

// RecordUsage inserts a usage row.
func (s *Store) RecordUsage(ctx context.Context, u UsageRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO usage (model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, owner, repo, after_sha)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Model,
		u.InputTokens,
		u.OutputTokens,
		u.CacheReadTokens,
		u.CacheCreationTokens,
		nullIfEmpty(u.Owner),
		nullIfEmpty(u.Repo),
		nullIfEmpty(u.AfterSHA),
	)
	if err != nil {
		return fmt.Errorf("RecordUsage insert: %w", err)
	}
	return nil
}
