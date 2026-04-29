// Package storage holds the SQLite-backed state used by the webhook
// server: idempotency tracking and per-call usage records.
//
// The driver is modernc.org/sqlite (pure Go, no CGO). The DSN enables
// WAL journaling and a 5s busy_timeout so concurrent webhook deliveries
// don't trip "database is locked" under retry pressure.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// Store is the persistent-state handle. It is safe for concurrent use;
// the underlying *sql.DB has its own connection pool.
type Store struct {
	db *sql.DB
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS reviews (
    owner        TEXT NOT NULL,
    repo         TEXT NOT NULL,
    after_sha    TEXT NOT NULL,
    reviewed_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    check_run_id INTEGER,
    PRIMARY KEY (owner, repo, after_sha)
);

CREATE TABLE IF NOT EXISTS usage (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    model                 TEXT NOT NULL,
    input_tokens          INTEGER NOT NULL,
    output_tokens         INTEGER NOT NULL,
    cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    owner                 TEXT,
    repo                  TEXT,
    after_sha             TEXT
);

CREATE INDEX IF NOT EXISTS idx_usage_occurred_at ON usage(occurred_at);
`

// Open opens the SQLite database at path and runs the idempotent schema
// migrations. The DSN sets WAL journal mode and a 5s busy timeout.
func Open(path string) (*Store, error) {
	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running schema migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// buildDSN constructs the modernc.org/sqlite DSN with our standard
// pragmas. busy_timeout must come before WAL because rapid consecutive
// webhook deliveries are the realistic contention case.
func buildDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(wal)")
	return "file:" + path + "?" + q.Encode()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
