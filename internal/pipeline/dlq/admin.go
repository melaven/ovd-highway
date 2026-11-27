package dlq

import (
	"context"
	"fmt"
	"time"
)

// dlqPreviewLen caps the payload preview returned by the Control Plane
// API. The payload is a serialized RawSignal (post text, URL, metadata)
// — never credentials — but it can be long, so it is truncated.
const dlqPreviewLen = 240

// dlqMaxListLimit caps the row count List will ever return, regardless of
// the caller-supplied limit.
const dlqMaxListLimit = 100

// dlqListLimit normalizes a caller-supplied limit into [1, dlqMaxListLimit].
func dlqListLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > dlqMaxListLimit {
		return dlqMaxListLimit
	}
	return limit
}

// DLQItem is one dead-letter row exposed to the Control Plane API.
type DLQItem struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Error          string    `json:"error"`
	Attempts       int       `json:"attempts"`
	CreatedAt      time.Time `json:"created_at"`
	PayloadPreview string    `json:"payload_preview"`
}

// listDLQQuery reads the newest dead-letter rows. The schema marks every
// selected column NOT NULL, but COALESCE guards rows.Scan against legacy
// or manually edited rows so a NULL can never fail the whole listing.
const listDLQQuery = `
SELECT id::text,
       COALESCE(source, ''),
       COALESCE(error_reason, ''),
       COALESCE(retry_count, 0),
       created_at,
       COALESCE(left(raw_payload::text, $2), '')
FROM dead_letter_signals
ORDER BY created_at DESC
LIMIT $1
`

// List returns up to limit most recent dead-letter rows, newest first.
// limit is clamped to [1, dlqMaxListLimit]; non-positive values fall back
// to 50.
func (s *PostgresRetryStore) List(ctx context.Context, limit int) ([]DLQItem, error) {
	limit = dlqListLimit(limit)
	rows, err := s.db.QueryContext(ctx, listDLQQuery, limit, dlqPreviewLen)
	if err != nil {
		return nil, fmt.Errorf("list dead letters: %w", err)
	}
	defer rows.Close()

	out := []DLQItem{}
	for rows.Next() {
		var it DLQItem
		if err := rows.Scan(&it.ID, &it.Source, &it.Error, &it.Attempts, &it.CreatedAt, &it.PayloadPreview); err != nil {
			return nil, fmt.Errorf("scan dead letter row: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dead letter rows: %w", err)
	}
	return out, nil
}

const countDLQQuery = `SELECT count(*) FROM dead_letter_signals`

// Count returns the number of unresolved dead-letter rows.
func (s *PostgresRetryStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, countDLQQuery).Scan(&n); err != nil {
		return 0, fmt.Errorf("count dead letters: %w", err)
	}
	return n, nil
}

const retryAllDLQQuery = `
UPDATE dead_letter_signals
SET retry_count = 0, next_retry_at = NOW()
`

// RetryAll marks every dead-letter row for immediate reprocessing:
// retry_count is reset so rows that exhausted the automatic retry cap
// get another attempt, and next_retry_at is pulled to now so the worker
// picks them up on its next poll. Returns the number of rows queued.
// The operation is idempotent — it only mutates existing rows, it never
// inserts duplicates. No WHERE clause is needed: the schema has no status
// column and successfully retried rows are DELETEd by Resolve, so every
// row in the table is unresolved by definition.
func (s *PostgresRetryStore) RetryAll(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, retryAllDLQQuery)
	if err != nil {
		return 0, fmt.Errorf("queue dead letter retries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("dead letter retry rows affected: %w", err)
	}
	return n, nil
}
