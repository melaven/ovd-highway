package lane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"ovd/internal/domain"
)

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
	signal := domain.RawSignal{ID: "signal-1", Source: "test", Text: "test signal"}
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
	signal := domain.RawSignal{ID: "signal-1", Source: "test", Text: "test signal"}
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
