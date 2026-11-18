package breaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func failFunc() func() error {
	return func() error { return errBoom }
}

func okFunc() func() error {
	return func() error { return nil }
}

func TestClosedToOpenAfterThreshold(t *testing.T) {
	b := New(Options{FailureThreshold: 5, Cooldown: time.Minute})

	for i := 0; i < 4; i++ {
		if err := b.Execute(failFunc()); !errors.Is(err, errBoom) {
			t.Fatalf("call %d: expected errBoom, got %v", i, err)
		}
		if b.State() != Closed {
			t.Fatalf("call %d: state = %v, want closed", i, b.State())
		}
	}

	if err := b.Execute(failFunc()); !errors.Is(err, errBoom) {
		t.Fatalf("5th call: expected errBoom, got %v", err)
	}
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}

	if err := b.Execute(okFunc()); !errors.Is(err, ErrOpen) {
		t.Fatalf("open circuit: expected ErrOpen, got %v", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	b := New(Options{FailureThreshold: 3, Cooldown: time.Minute})

	b.Execute(failFunc())
	b.Execute(failFunc())
	if err := b.Execute(okFunc()); err != nil {
		t.Fatalf("ok call: %v", err)
	}
	if got := b.ConsecutiveFailures(); got != 0 {
		t.Fatalf("failures = %d, want 0", got)
	}

	b.Execute(failFunc())
	b.Execute(failFunc())
	if b.State() != Closed {
		t.Fatalf("state = %v, want closed after reset", b.State())
	}
}

func TestOpenToHalfOpenToClosed(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()) }

	b := New(Options{
		FailureThreshold: 2,
		Cooldown:         60 * time.Second,
		Now:              clock,
	})

	b.Execute(failFunc())
	b.Execute(failFunc())
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}

	// Still inside cooldown: rejected.
	if err := b.Execute(okFunc()); !errors.Is(err, ErrOpen) {
		t.Fatalf("cooldown: expected ErrOpen, got %v", err)
	}

	// Advance past cooldown: half-open probe allowed.
	now.Add(int64(61 * time.Second))
	if err := b.Execute(okFunc()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if b.State() != Closed {
		t.Fatalf("state = %v, want closed after successful probe", b.State())
	}
}

func TestHalfOpenProbeFailureReopens(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()) }

	b := New(Options{
		FailureThreshold: 1,
		Cooldown:         10 * time.Second,
		Now:              clock,
	})

	b.Execute(failFunc())
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}

	now.Add(int64(11 * time.Second))
	if err := b.Execute(failFunc()); !errors.Is(err, errBoom) {
		t.Fatalf("probe: expected errBoom, got %v", err)
	}
	if b.State() != Open {
		t.Fatalf("state = %v, want open after failed probe", b.State())
	}

	// Reopened: immediate calls rejected again until next cooldown.
	if err := b.Execute(okFunc()); !errors.Is(err, ErrOpen) {
		t.Fatalf("reopened: expected ErrOpen, got %v", err)
	}
}

func TestHalfOpenAllowsSingleProbe(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()) }

	b := New(Options{
		FailureThreshold: 1,
		Cooldown:         time.Second,
		Now:              clock,
	})

	b.Execute(failFunc())
	now.Add(int64(2 * time.Second))

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Execute(func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	if err := b.Execute(okFunc()); !errors.Is(err, ErrOpen) {
		t.Fatalf("concurrent probe: expected ErrOpen, got %v", err)
	}
	close(release)
	<-done
}

func TestStaleRequestDoesNotResolveHalfOpen(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()) }

	b := New(Options{
		FailureThreshold: 1,
		Cooldown:         time.Second,
		Now:              clock,
	})

	// A request admitted in Closed that blocks inside req().
	staleEntered := make(chan struct{})
	staleRelease := make(chan struct{})
	staleDone := make(chan struct{})
	go func() {
		defer close(staleDone)
		_ = b.Execute(func() error {
			close(staleEntered)
			<-staleRelease
			return nil
		})
	}()
	<-staleEntered

	// Trip the circuit open, then move past cooldown.
	if err := b.Execute(failFunc()); !errors.Is(err, errBoom) {
		t.Fatalf("trip: %v", err)
	}
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}
	now.Add(int64(2 * time.Second))

	// The half-open probe starts and blocks.
	probeEntered := make(chan struct{})
	probeRelease := make(chan struct{})
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		_ = b.Execute(func() error {
			close(probeEntered)
			<-probeRelease
			return nil
		})
	}()
	<-probeEntered

	// Stale Closed-admitted request finishes mid-probe: must be ignored.
	close(staleRelease)
	<-staleDone

	if got := b.State(); got != HalfOpen {
		t.Fatalf("state = %v, want half-open (stale result leaked)", got)
	}
	if err := b.Execute(okFunc()); !errors.Is(err, ErrOpen) {
		t.Fatalf("second probe while one in flight: expected ErrOpen, got %v", err)
	}

	// Probe completes successfully -> Closed.
	close(probeRelease)
	<-probeDone
	if got := b.State(); got != Closed {
		t.Fatalf("state = %v, want closed", got)
	}
}

func TestPanicCountsAsFailure(t *testing.T) {
	b := New(Options{FailureThreshold: 1, Cooldown: time.Minute})

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic to propagate")
			}
		}()
		_ = b.Execute(func() error { panic("kaboom") })
	}()

	if b.State() != Open {
		t.Fatalf("state = %v, want open after panic", b.State())
	}
}

func TestConcurrentExecute(t *testing.T) {
	b := New(Options{FailureThreshold: 50, Cooldown: time.Minute})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = b.Execute(func() error {
					if i%2 == 0 {
						return errBoom
					}
					return nil
				})
			}
		}(i)
	}
	wg.Wait()

	// State must be consistent; no data race reported under -race.
	_ = b.State()
	_ = b.ConsecutiveFailures()
}
