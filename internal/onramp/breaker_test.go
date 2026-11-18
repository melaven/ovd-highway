package onramp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ovd/internal/domain"
	"ovd/internal/pkg/breaker"
)

// pollViaBreaker mirrors the exact call path used by Connector.Start.
func pollViaBreaker(b *breaker.Breaker, poll func() error) error {
	return b.Execute(func() error {
		err := poll()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	})
}

func TestHHConnectorBreakerOpensOnHTTP500(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("hh is down"))
	}))
	defer server.Close()

	connector := NewHHConnector()
	connector.client = server.Client()
	connector.endpoint = server.URL
	connector.breaker = breaker.New(breaker.Options{FailureThreshold: 3, Cooldown: time.Minute})

	signals := make(chan domain.RawSignal, 1)
	ctx := context.Background()
	poll := func() error { return connector.poll(ctx, signals) }

	for i := 0; i < 3; i++ {
		if err := pollViaBreaker(connector.breaker, poll); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("server hits = %d, want 3", got)
	}
	if connector.breaker.State() != breaker.Open {
		t.Fatalf("breaker state = %v, want open", connector.breaker.State())
	}

	// Circuit open: no HTTP call is made.
	if err := pollViaBreaker(connector.breaker, poll); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("open circuit: expected ErrOpen, got %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("server hits after open = %d, want 3 (no call should be made)", got)
	}
}

func TestRedditConnectorBreakerOpensOnToken429(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	connector := NewRedditConnector()
	connector.client = server.Client()
	connector.tokens.client = server.Client()
	connector.tokens.tokenURL = server.URL + "/api/v1/access_token"
	connector.tokens.clientID = "id"
	connector.tokens.clientSecret = "secret"
	connector.breaker = breaker.New(breaker.Options{FailureThreshold: 2, Cooldown: time.Minute})

	signals := make(chan domain.RawSignal, 1)
	ctx := context.Background()
	poll := func() error { return connector.poll(ctx, signals) }

	for i := 0; i < 2; i++ {
		if err := pollViaBreaker(connector.breaker, poll); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
	if connector.breaker.State() != breaker.Open {
		t.Fatalf("breaker state = %v, want open", connector.breaker.State())
	}
	if err := pollViaBreaker(connector.breaker, poll); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("open circuit: expected ErrOpen, got %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2", got)
	}
}