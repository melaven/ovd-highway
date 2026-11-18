package dlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DeadLetterEntry is one archived signal that failed downstream processing.
type DeadLetterEntry struct {
	Source      string
	Payload     json.RawMessage
	ErrorReason string
	RetryCount  int
	NextRetryAt time.Time
	// DedupeKey is the caller-supplied deduplication key for this
	// signal. Archive implementations should store it in the
	// dedupe_key column, which has a UNIQUE index, so retried
	// HandleLLMFailure calls archive at most one row per key.
	// Empty means "no dedupe" and is stored as NULL.
	DedupeKey string
}

// ArchiveStore persists a dead-letter entry. Implementations must be
// safe for concurrent use.
type ArchiveStore interface {
	Archive(ctx context.Context, entry DeadLetterEntry) error
}

// DedupStore releases a deduplication key so the signal can be
// re-ingested later. Implementations must be safe for concurrent use.
type DedupStore interface {
	DeleteKey(ctx context.Context, key string) error
}

// Options configures a Manager. Zero values fall back to defaults.
type Options struct {
	// RetryDelay is how far in the future NextRetryAt is set for a
	// freshly archived entry, making it eligible for the retry worker
	// after this delay. Default: 0 (immediately eligible).
	RetryDelay time.Duration
	// Now is an optional clock override for testing. Default: time.Now.
	Now func() time.Time
}

// Manager archives failed signals and releases their dedupe keys.
// It is safe for concurrent use.
type Manager struct {
	archive    ArchiveStore
	dedup      DedupStore
	retryDelay time.Duration
	now        func() time.Time
}

// NewManager creates a Manager. Both stores are required.
func NewManager(archive ArchiveStore, dedup DedupStore, opts Options) (*Manager, error) {
	if archive == nil {
		return nil, errors.New("dlq: archive store is required")
	}
	if dedup == nil {
		return nil, errors.New("dlq: dedup store is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{
		archive:    archive,
		dedup:      dedup,
		retryDelay: opts.RetryDelay,
		now:        opts.Now,
	}, nil
}

// HandleLLMFailure archives a failed payload and then releases its
// dedupe key so a background worker can re-ingest the signal later.
//
// Ordering guarantee: the dedupe key is deleted only after the archive
// succeeds. If archiving fails, the key is left untouched and the
// error is returned — the signal stays deduplicated and nothing is
// lost silently.
//
// If the archive succeeds but the dedupe delete fails, an error is
// still returned; the entry is safely stored. A retried call hits the
// dedupe_key UNIQUE index: with a conforming ArchiveStore
// (ON CONFLICT DO NOTHING) no duplicate row is written and the
// dedupe release is retried. Without a dedupeKey, delivery is
// at-least-once and duplicates are possible.
func (m *Manager) HandleLLMFailure(ctx context.Context, source string, payload []byte, dedupeKey string, errReason string) error {
	if ctx == nil {
		return errors.New("dlq: context is required")
	}
	if len(payload) == 0 {
		return errors.New("dlq: payload is required")
	}
	if !json.Valid(payload) {
		return fmt.Errorf("dlq: payload is not valid JSON")
	}
	if errReason == "" {
		errReason = "unknown LLM failure"
	}

	entry := DeadLetterEntry{
		Source:      source,
		Payload:     json.RawMessage(append([]byte(nil), payload...)),
		ErrorReason: errReason,
		RetryCount:  0,
		NextRetryAt: m.now().Add(m.retryDelay),
		DedupeKey:   dedupeKey,
	}

	if err := m.archive.Archive(ctx, entry); err != nil {
		return fmt.Errorf("dlq: archive signal from %q: %w", source, err)
	}

	// Only reached when the payload is durably stored.
	if dedupeKey != "" {
		if err := m.dedup.DeleteKey(ctx, dedupeKey); err != nil {
			return fmt.Errorf("dlq: archived signal but failed to release dedupe key %q: %w", dedupeKey, err)
		}
	}
	return nil
}
