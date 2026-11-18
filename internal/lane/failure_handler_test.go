package lane

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"ovd/internal/domain"
	"ovd/internal/pkg/breaker"
)

type fakeFailureHandler struct {
	calls    int
	source   string
	payload  []byte
	dedupe   string
	reason   string
}

func (f *fakeFailureHandler) HandleLLMFailure(_ context.Context, source string, payload []byte, dedupeKey, errReason string) error {
	f.calls++
	f.source = source
	f.payload = payload
	f.dedupe = dedupeKey
	f.reason = errReason
	return nil
}

func TestQualifierRoutesTransientFailureToFailureHandler(t *testing.T) {
	signal := domain.RawSignal{ID: "sig-dlq", Source: "reddit", Text: passingPrefilterText}
	completer := &failingCompleter{err: &StatusError{Code: 503}}
	handler := &fakeFailureHandler{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	q.SetFailureHandler(handler)

	in := make(chan domain.RawSignal, 1)
	in <- signal
	close(in)
	if err := q.worker(context.Background(), 0, in, make(chan domain.QualifiedLead)); err != nil {
		t.Fatalf("worker: %v", err)
	}

	if handler.calls != 1 {
		t.Fatalf("HandleLLMFailure calls = %d, want 1", handler.calls)
	}
	if handler.source != "reddit" {
		t.Errorf("source = %q, want reddit", handler.source)
	}
	if handler.dedupe != DedupKey(signal) {
		t.Errorf("dedupeKey = %q, want %q", handler.dedupe, DedupKey(signal))
	}
	if len(handler.payload) == 0 {
		t.Error("payload empty")
	}
}

func TestQualifierRoutesOpenBreakerToFailureHandler(t *testing.T) {
	signal := domain.RawSignal{ID: "sig-open", Source: "hh", Text: passingPrefilterText}
	completer := &failingCompleter{err: &StatusError{Code: 500}}
	handler := &fakeFailureHandler{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	q.SetFailureHandler(handler)
	// Force the breaker open so callOnce returns ErrOpen immediately.
	openBreaker := breaker.New(breaker.Options{FailureThreshold: 1, Cooldown: time.Hour})
	_ = openBreaker.Execute(func() error { return &StatusError{Code: 500} })
	q.SetBreaker(openBreaker)

	in := make(chan domain.RawSignal, 1)
	in <- signal
	close(in)
	if err := q.worker(context.Background(), 0, in, make(chan domain.QualifiedLead)); err != nil {
		t.Fatalf("worker: %v", err)
	}

	if handler.calls != 1 {
		t.Fatalf("HandleLLMFailure calls = %d, want 1", handler.calls)
	}
	if completer.calls != 0 {
		t.Errorf("LLM calls = %d, want 0 while breaker open", completer.calls)
	}
}

func TestQualifierDropsSpamKeywordBeforeLLM(t *testing.T) {
	completer := &failingCompleter{err: &StatusError{Code: 503}}
	handler := &fakeFailureHandler{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	q.SetFailureHandler(handler)

	spamText := "Нужен разработчик без опыта, оплата копейки, это скам"
	if utf8Len := len([]rune(spamText)); utf8Len < 40 {
		spamText += " еще немного текста чтобы пройти минимальную длину"
	}
	in := make(chan domain.RawSignal, 1)
	in <- domain.RawSignal{ID: "sig-spam", Source: "reddit", Text: spamText}
	close(in)
	if err := q.worker(context.Background(), 0, in, make(chan domain.QualifiedLead)); err != nil {
		t.Fatalf("worker: %v", err)
	}

	if completer.calls != 0 {
		t.Errorf("LLM calls = %d, want 0 for spam signal", completer.calls)
	}
	if handler.calls != 0 {
		t.Errorf("DLQ calls = %d, want 0 (spam is dropped, not archived)", handler.calls)
	}
}