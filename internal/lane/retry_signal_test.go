package lane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"ovd/internal/domain"
	"ovd/internal/pkg/breaker"
)

type staticCompleter struct {
	out   string
	calls int
}

func (c *staticCompleter) Complete(context.Context, string, string) (string, error) {
	c.calls++
	return c.out, nil
}

type markerReleaser struct {
	releaseCalls int
	markSeenCalls int
	marked       domain.RawSignal
}

func (m *markerReleaser) Release(context.Context, domain.RawSignal) error {
	m.releaseCalls++
	return nil
}

func (m *markerReleaser) MarkSeen(_ context.Context, sig domain.RawSignal) error {
	m.markSeenCalls++
	m.marked = sig
	return nil
}

const validQualificationJSON = `{"is_relevant":true,"score":8,"estimated_budget":"100k","pain_points":"none","draft_proposal":"pitch","tech_stack":["go"],"detected_job_type":"freelance"}`

func newRetryQualifier(t *testing.T, completer Completer, releaser SignalReleaser) *Qualifier {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log, releaser)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	return q
}

func TestRetrySignalSuccessMarksDedupAndReturnsLead(t *testing.T) {
	sig := domain.RawSignal{ID: "retry-1", Source: "reddit", Text: passingPrefilterText, JobType: "freelance"}
	completer := &staticCompleter{out: validQualificationJSON}
	releaser := &markerReleaser{}
	q := newRetryQualifier(t, completer, releaser)

	lead, err := q.RetrySignal(context.Background(), sig)
	if err != nil {
		t.Fatalf("RetrySignal: %v", err)
	}
	if lead.QualifiedAt.IsZero() {
		t.Fatal("expected qualified lead, got zero value (rejected?)")
	}
	if lead.JobType != "freelance" || lead.Qualification.Score != 8 {
		t.Errorf("lead = %+v", lead)
	}
	if releaser.markSeenCalls != 1 {
		t.Fatalf("MarkSeen calls = %d, want 1", releaser.markSeenCalls)
	}
	if releaser.marked.ID != sig.ID {
		t.Errorf("marked signal = %q, want %q", releaser.marked.ID, sig.ID)
	}
	if releaser.releaseCalls != 0 {
		t.Errorf("Release calls = %d, want 0 on success", releaser.releaseCalls)
	}
}

func TestRetrySignalFailurePropagatesWithoutFailureHandler(t *testing.T) {
	sig := domain.RawSignal{ID: "retry-2", Source: "hh", Text: passingPrefilterText}
	completer := &failingCompleter{err: &StatusError{Code: 503}}
	handler := &fakeFailureHandler{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	q.SetFailureHandler(handler)

	lead, err := q.RetrySignal(context.Background(), sig)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !lead.QualifiedAt.IsZero() {
		t.Error("expected zero lead on failure")
	}
	if handler.calls != 0 {
		t.Fatalf("HandleLLMFailure calls = %d, want 0 — worker owns retry state", handler.calls)
	}
	if completer.calls != maxAttempts {
		t.Errorf("LLM calls = %d, want %d", completer.calls, maxAttempts)
	}
}

func TestRetrySignalOpenBreakerPropagatesError(t *testing.T) {
	sig := domain.RawSignal{ID: "retry-3", Source: "hh", Text: passingPrefilterText}
	completer := &failingCompleter{err: &StatusError{Code: 500}}
	handler := &fakeFailureHandler{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	q.SetFailureHandler(handler)
	openBreaker := breaker.New(breaker.Options{FailureThreshold: 1, Cooldown: time.Hour})
	_ = openBreaker.Execute(func() error { return &StatusError{Code: 500} })
	q.SetBreaker(openBreaker)

	_, err = q.RetrySignal(context.Background(), sig)
	if err == nil {
		t.Fatal("expected ErrOpen-derived error, got nil")
	}
	if !errors.Is(err, breaker.ErrOpen) {
		t.Errorf("err = %v, want breaker.ErrOpen in chain", err)
	}
	if completer.calls != 0 {
		t.Errorf("LLM calls = %d, want 0 while breaker open", completer.calls)
	}
	if handler.calls != 0 {
		t.Fatalf("HandleLLMFailure calls = %d, want 0", handler.calls)
	}
}

func TestRetrySignalPrefilterDropResolvesWithoutLLM(t *testing.T) {
	sig := domain.RawSignal{ID: "retry-4", Source: "reddit", Text: "tiny"}
	completer := &staticCompleter{out: validQualificationJSON}
	q := newRetryQualifier(t, completer, nil)

	lead, err := q.RetrySignal(context.Background(), sig)
	if err != nil {
		t.Fatalf("RetrySignal: %v", err)
	}
	if !lead.QualifiedAt.IsZero() {
		t.Error("expected zero lead for prefilter drop")
	}
	if completer.calls != 0 {
		t.Errorf("LLM calls = %d, want 0", completer.calls)
	}
}