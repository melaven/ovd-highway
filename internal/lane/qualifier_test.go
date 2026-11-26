package lane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"ovd/internal/domain"
)

const passingPrefilterText = "We need an experienced Go engineer to build a data integration service with PostgreSQL."

type failingCompleter struct {
	err   error
	calls int
}

func (c *failingCompleter) Complete(context.Context, string, string) (string, error) {
	c.calls++
	return "", c.err
}

type recordingReleaser struct {
	calls  int
	signal domain.RawSignal
	err    error
}

func (r *recordingReleaser) Release(_ context.Context, sig domain.RawSignal) error {
	r.calls++
	r.signal = sig
	return r.err
}

func TestQualifierReleasesDedupAfterTransientRetries(t *testing.T) {
	signal := domain.RawSignal{ID: "signal-1", Source: "test", Text: passingPrefilterText}
	completer := &failingCompleter{err: &StatusError{Code: 503}}
	releaser := &recordingReleaser{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log, releaser)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}

	in := make(chan domain.RawSignal, 1)
	in <- signal
	close(in)
	if err := q.worker(context.Background(), 0, in, make(chan domain.QualifiedLead)); err != nil {
		t.Fatalf("worker: %v", err)
	}

	if completer.calls != maxAttempts {
		t.Errorf("LLM calls = %d, want %d", completer.calls, maxAttempts)
	}
	if releaser.calls != 1 {
		t.Fatalf("Release calls = %d, want 1", releaser.calls)
	}
	if releaser.signal.ID != signal.ID {
		t.Errorf("released signal ID = %q, want %q", releaser.signal.ID, signal.ID)
	}
}

func TestQualifierKeepsDedupAfterPermanentLLMError(t *testing.T) {
	signal := domain.RawSignal{ID: "signal-1", Source: "test", Text: passingPrefilterText}
	completer := &failingCompleter{err: &StatusError{Code: 400}}
	releaser := &recordingReleaser{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log, releaser)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}

	in := make(chan domain.RawSignal, 1)
	in <- signal
	close(in)
	if err := q.worker(context.Background(), 0, in, make(chan domain.QualifiedLead)); err != nil {
		t.Fatalf("worker: %v", err)
	}

	if completer.calls != 1 {
		t.Errorf("LLM calls = %d, want 1", completer.calls)
	}
	if releaser.calls != 0 {
		t.Errorf("Release calls = %d, want 0", releaser.calls)
	}
}

func TestQualifierDropsSignalRejectedByPreFilter(t *testing.T) {
	completer := &failingCompleter{err: &StatusError{Code: 503}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}

	in := make(chan domain.RawSignal, 1)
	in <- domain.RawSignal{ID: "signal-1", Source: "test", Text: " \n\t "}
	close(in)
	if err := q.worker(context.Background(), 0, in, make(chan domain.QualifiedLead)); err != nil {
		t.Fatalf("worker: %v", err)
	}

	if completer.calls != 0 {
		t.Errorf("LLM calls = %d, want 0", completer.calls)
	}
}

func TestIsRetryableIncludesTimeoutAndEOF(t *testing.T) {
	for _, err := range []error{
		context.DeadlineExceeded,
		io.EOF,
		io.ErrUnexpectedEOF,
	} {
		if !isRetryable(err) {
			t.Errorf("isRetryable(%v) = false, want true", err)
		}
	}
	if isRetryable(errors.New("permanent failure")) {
		t.Error("isRetryable(permanent failure) = true, want false")
	}
}

type slowJSONCompleter struct {
	current atomic.Int32
	maxSeen atomic.Int32
	calls   atomic.Int32
}

func (c *slowJSONCompleter) Complete(ctx context.Context, _, _ string) (string, error) {
	cur := c.current.Add(1)
	for {
		old := c.maxSeen.Load()
		if cur <= old || c.maxSeen.CompareAndSwap(old, cur) {
			break
		}
	}
	defer c.current.Add(-1)
	c.calls.Add(1)
	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return `{"is_relevant":true,"score":8,"estimated_budget":"100","pain_points":"p","draft_proposal":"d","tech_stack":[],"detected_job_type":"freelance"}`, nil
}

func TestQualifierPoolProcessesBatchConcurrently(t *testing.T) {
	completer := &slowJSONCompleter{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 3, 5*time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}

	const n = 9
	in := make(chan domain.RawSignal, n)
	for i := 0; i < n; i++ {
		in <- domain.RawSignal{ID: fmt.Sprintf("s%d", i), Source: "test", Text: passingPrefilterText, JobType: "freelance"}
	}
	close(in)

	out := make(chan domain.QualifiedLead, n)
	if err := q.Run(context.Background(), in, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(out)

	got := 0
	for range out {
		got++
	}
	if got != n {
		t.Fatalf("leads = %d, want %d", got, n)
	}
	if calls := completer.calls.Load(); calls != n {
		t.Fatalf("LLM calls = %d, want %d", calls, n)
	}
	if max := completer.maxSeen.Load(); max < 2 || max > 3 {
		t.Fatalf("max in-flight = %d, want within (1, 3]", max)
	}
}
