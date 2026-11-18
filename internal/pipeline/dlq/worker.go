package dlq

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"
)

// RetryRecord is a dead-letter row due for reprocessing.
type RetryRecord struct {
	ID          string
	Source      string
	Payload     json.RawMessage
	ErrorReason string
	RetryCount  int
	DedupeKey   string
}

// RetryStore is the persistence contract for the retry worker.
type RetryStore interface {
	// FetchDue returns up to limit rows with next_retry_at <= now and
	// retry_count < maxRetries, oldest first.
	FetchDue(ctx context.Context, now time.Time, maxRetries, limit int) ([]RetryRecord, error)
	// Resolve removes a successfully reprocessed row.
	Resolve(ctx context.Context, id string) error
	// Fail records a failed attempt: increments retry_count and sets the
	// next_retry_at deadline.
	Fail(ctx context.Context, id string, retryCount int, nextRetryAt time.Time, reason string) error
}

// PostgresRetryStore implements RetryStore over *sql.DB.
type PostgresRetryStore struct {
	db *sql.DB
}

func NewPostgresRetryStore(db *sql.DB) (*PostgresRetryStore, error) {
	if db == nil {
		return nil, errors.New("dlq: sql.DB is required")
	}
	return &PostgresRetryStore{db: db}, nil
}

const fetchDueQuery = `
SELECT id::text, source, raw_payload, error_reason, retry_count,
       COALESCE(dedupe_key, '')
FROM dead_letter_signals
WHERE next_retry_at <= $1 AND retry_count < $2
ORDER BY next_retry_at
LIMIT $3
`

func (s *PostgresRetryStore) FetchDue(ctx context.Context, now time.Time, maxRetries, limit int) ([]RetryRecord, error) {
	rows, err := s.db.QueryContext(ctx, fetchDueQuery, now, maxRetries, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch due dead letters: %w", err)
	}
	defer rows.Close()

	var out []RetryRecord
	for rows.Next() {
		var rec RetryRecord
		var payload []byte
		if err := rows.Scan(&rec.ID, &rec.Source, &payload, &rec.ErrorReason, &rec.RetryCount, &rec.DedupeKey); err != nil {
			return nil, fmt.Errorf("scan dead letter row: %w", err)
		}
		rec.Payload = json.RawMessage(payload)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dead letter rows: %w", err)
	}
	return out, nil
}

func (s *PostgresRetryStore) Resolve(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM dead_letter_signals WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("resolve dead letter %s: %w", id, err)
	}
	return nil
}

func (s *PostgresRetryStore) Fail(ctx context.Context, id string, retryCount int, nextRetryAt time.Time, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE dead_letter_signals SET retry_count = $2, next_retry_at = $3, error_reason = $4 WHERE id = $1::uuid`,
		id, retryCount, nextRetryAt, reason)
	if err != nil {
		return fmt.Errorf("mark dead letter %s failed: %w", id, err)
	}
	return nil
}

// Processor re-evaluates one dead-letter record. Returning nil marks it
// resolved; any error schedules the next retry.
type Processor func(ctx context.Context, rec RetryRecord) error

// WorkerOptions configures the retry worker.
type WorkerOptions struct {
	// Interval between polls. Default: 30s.
	Interval time.Duration
	// BatchSize caps rows processed per tick. Default: 10.
	BatchSize int
	// MaxRetries caps retry_count; rows at the cap are never fetched.
	// Default: 5.
	MaxRetries int
	// BaseBackoff is the first retry delay; it doubles per attempt.
	// Default: 1m.
	BaseBackoff time.Duration
	// MaxBackoff caps the computed delay. Default: 1h.
	MaxBackoff time.Duration
}

// Worker polls due dead-letter rows and reprocesses them.
type Worker struct {
	store     RetryStore
	processor Processor
	opts      WorkerOptions
	log       *slog.Logger
}

func NewWorker(store RetryStore, processor Processor, opts WorkerOptions, log *slog.Logger) (*Worker, error) {
	if store == nil || processor == nil || log == nil {
		return nil, errors.New("dlq: store, processor and logger are required")
	}
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 10
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 5
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = time.Minute
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = time.Hour
	}
	return &Worker{store: store, processor: processor, opts: opts, log: log.With("component", "dlq_worker")}, nil
}

// Run polls until ctx is cancelled. It processes one batch immediately,
// then on every Interval tick.
func (w *Worker) Run(ctx context.Context) error {
	w.processBatch(ctx)
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) {
	records, err := w.store.FetchDue(ctx, time.Now(), w.opts.MaxRetries, w.opts.BatchSize)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("fetch due dead letters failed", "err", err)
		}
		return
	}
	for _, rec := range records {
		if ctx.Err() != nil {
			return
		}
		w.processOne(ctx, rec)
	}
}

func (w *Worker) processOne(ctx context.Context, rec RetryRecord) {
	if err := w.processor(ctx, rec); err != nil {
		if ctx.Err() != nil {
			return
		}
		next := w.nextRetryAt(rec.RetryCount)
		if err := w.store.Fail(ctx, rec.ID, rec.RetryCount+1, next, err.Error()); err != nil {
			w.log.Error("mark dead letter retry failed", "id", rec.ID, "err", err)
			return
		}
		w.log.Warn("dead letter retry scheduled", "id", rec.ID, "retry", rec.RetryCount+1, "next", next, "err", err)
		return
	}
	if err := w.store.Resolve(ctx, rec.ID); err != nil {
		w.log.Error("resolve dead letter failed", "id", rec.ID, "err", err)
		return
	}
	w.log.Info("dead letter resolved", "id", rec.ID, "source", rec.Source)
}

// nextRetryAt computes exponential backoff with jitter:
// base * 2^retryCount + random [0, base), capped at MaxBackoff.
func (w *Worker) nextRetryAt(retryCount int) time.Time {
	shift := retryCount
	if shift > 10 {
		shift = 10
	}
	delay := w.opts.BaseBackoff << shift
	if delay > w.opts.MaxBackoff || delay <= 0 {
		delay = w.opts.MaxBackoff
	}
	jitter := time.Duration(rand.Int64N(int64(w.opts.BaseBackoff)))
	return time.Now().Add(delay + jitter)
}
