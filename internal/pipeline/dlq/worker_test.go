package dlq

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeRetryStore struct {
	due       []RetryRecord
	fetchErr  error
	resolved  []string
	failed    []failCall
	fetchNow  time.Time
	maxRetries int
	limit     int
}

type failCall struct {
	id         string
	retryCount int
	next       time.Time
	reason     string
}

func (f *fakeRetryStore) FetchDue(ctx context.Context, now time.Time, maxRetries, limit int) ([]RetryRecord, error) {
	f.fetchNow = now
	f.maxRetries = maxRetries
	f.limit = limit
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.due, nil
}

func (f *fakeRetryStore) Resolve(ctx context.Context, id string) error {
	f.resolved = append(f.resolved, id)
	return nil
}

func (f *fakeRetryStore) Fail(ctx context.Context, id string, retryCount int, nextRetryAt time.Time, reason string) error {
	f.failed = append(f.failed, failCall{id: id, retryCount: retryCount, next: nextRetryAt, reason: reason})
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWorkerResolvesSuccessfulRecord(t *testing.T) {
	store := &fakeRetryStore{due: []RetryRecord{{ID: "r1", Source: "reddit", Payload: []byte(`{"id":"s1"}`), RetryCount: 1}}}
	var processed []RetryRecord
	w, err := NewWorker(store, func(ctx context.Context, rec RetryRecord) error {
		processed = append(processed, rec)
		return nil
	}, WorkerOptions{Interval: time.Hour, BatchSize: 5, MaxRetries: 3, BaseBackoff: time.Minute}, testLogger())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()

	// Wait for the immediate first batch.
	deadline := time.Now().Add(2 * time.Second)
	for len(processed) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if len(processed) != 1 || processed[0].ID != "r1" {
		t.Fatalf("processed = %+v", processed)
	}
	if len(store.resolved) != 1 || store.resolved[0] != "r1" {
		t.Fatalf("resolved = %v", store.resolved)
	}
	if len(store.failed) != 0 {
		t.Fatalf("failed = %+v", store.failed)
	}
	if store.maxRetries != 3 || store.limit != 5 {
		t.Errorf("FetchDue args maxRetries=%d limit=%d", store.maxRetries, store.limit)
	}
}

func TestWorkerSchedulesRetryWithBackoff(t *testing.T) {
	before := time.Now()
	store := &fakeRetryStore{due: []RetryRecord{{ID: "r2", RetryCount: 2}}}
	w, err := NewWorker(store, func(ctx context.Context, rec RetryRecord) error {
		return errors.New("llm still 429")
	}, WorkerOptions{Interval: time.Hour, BatchSize: 5, MaxRetries: 5, BaseBackoff: time.Minute, MaxBackoff: time.Hour}, testLogger())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(store.failed) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if len(store.failed) != 1 {
		t.Fatalf("failed calls = %+v", store.failed)
	}
	fc := store.failed[0]
	if fc.retryCount != 3 {
		t.Errorf("retryCount = %d, want 3", fc.retryCount)
	}
	// base(1m) * 2^2 = 4m + jitter[0,1m) => between 4m and 5m
	minDelay := 4 * time.Minute
	maxDelay := 5 * time.Minute
	if fc.next.Before(before.Add(minDelay)) || fc.next.After(time.Now().Add(maxDelay)) {
		t.Errorf("next_retry_at = %v, want ~%v..%v from now", fc.next, minDelay, maxDelay)
	}
}

func TestWorkerFetchErrorDoesNotPanic(t *testing.T) {
	store := &fakeRetryStore{fetchErr: errors.New("db gone")}
	w, err := NewWorker(store, func(ctx context.Context, rec RetryRecord) error { return nil },
		WorkerOptions{Interval: time.Hour}, testLogger())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Run returns only on ctx cancel; fetch error is logged and skipped.
	_ = w.Run(ctx)
}