package dlq

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// maxDedupeKeyLen mirrors the VARCHAR(255) dedupe_key column. Longer
// keys are stored as their SHA-256 hex digest (64 chars) to stay
// index-friendly and collision-safe.
const maxDedupeKeyLen = 255

// PostgresArchive persists dead-letter entries into
// dead_letter_signals. It relies on the partial unique index
// uq_dead_letter_signals_dedupe_key for idempotent inserts.
type PostgresArchive struct {
	db *sql.DB
}

// NewPostgresArchive creates an ArchiveStore backed by *sql.DB
// (pgx stdlib driver).
func NewPostgresArchive(db *sql.DB) (*PostgresArchive, error) {
	if db == nil {
		return nil, errors.New("dlq: sql.DB is required")
	}
	return &PostgresArchive{db: db}, nil
}

// normalizeDedupeKey maps an empty key to NULL and hashes keys longer
// than the column allows. Returns nil for "store as NULL".
func normalizeDedupeKey(key string) any {
	if key == "" {
		return nil
	}
	if len(key) > maxDedupeKeyLen {
		sum := sha256.Sum256([]byte(key))
		return hex.EncodeToString(sum[:])
	}
	return key
}

const archiveQuery = `
INSERT INTO dead_letter_signals
    (source, raw_payload, error_reason, retry_count, next_retry_at, dedupe_key)
VALUES ($1, $2::jsonb, $3, $4, $5, $6)
ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
`

func (a *PostgresArchive) Archive(ctx context.Context, entry DeadLetterEntry) error {
	var nextRetryAt any
	if !entry.NextRetryAt.IsZero() {
		nextRetryAt = entry.NextRetryAt
	}
	res, err := a.db.ExecContext(ctx, archiveQuery,
		entry.Source,
		string(entry.Payload),
		entry.ErrorReason,
		entry.RetryCount,
		nextRetryAt,
		normalizeDedupeKey(entry.DedupeKey),
	)
	if err != nil {
		return fmt.Errorf("insert dead letter signal: %w", err)
	}
	// RowsAffected == 0 means the dedupe_key already existed; that is a
	// successful idempotent no-op, not an error.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return nil
	}
	return nil
}
