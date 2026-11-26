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

// DLQItem is one dead-letter row exposed to the Control Plane API.
type DLQItem struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Error          string    `json:"error"`
	Attempts       int       `json:"attempts"`
	CreatedAt      time.Time `json:"created_at"`
	PayloadPreview string    `json:"payload_preview"`
}

const listDLQQuery = `
SELECT id::text, source, error_reason, retry_count, created_at,
       left(raw_payload::text, $2)
FROM dead_letter_signals
ORDER BY created_at DESC
LIMIT $1
`

// List returns up to limit most recent dead-letter rows, newest first.
func (s *PostgresRetryStore) List(ctx context.Context, limit int) ([]DLQItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, listDLQQuery, limit, dlqPreviewLen)
	if err != nil {
		return nil, fmt.Errorf("list dead letters: %w", err)
	}
	defer rows.Close()

	var out []DLQItem
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
// inserts duplicates.
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
