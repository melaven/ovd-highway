package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State represents the circuit breaker state machine state.
type State int

const (
	// Closed is the normal operating state: requests pass through.
	Closed State = iota
	// Open means failures exceeded the threshold; requests are rejected
	// until the cooldown period elapses.
	Open
	// HalfOpen is a probation state after cooldown: a single probe request
	// is allowed to test whether the dependency has recovered.
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrOpen is returned by Execute when the circuit is open and the
// cooldown period has not yet elapsed, or when a half-open probe is
// already in flight.
var ErrOpen = errors.New("circuit breaker is open")

const (
	defaultFailureThreshold = 5
	defaultCooldown         = 60 * time.Second
)

// Options configures a Breaker. Zero values fall back to defaults.
type Options struct {
	// FailureThreshold is the number of consecutive failures that opens
	// the circuit. Default: 5.
	FailureThreshold int
	// Cooldown is how long the circuit stays Open before allowing a
	// half-open probe. Default: 60s.
	Cooldown time.Duration
	// Now is an optional clock override for testing. Default: time.Now.
	Now func() time.Time
}

// Breaker is a thread-safe circuit breaker.
type Breaker struct {
	mu sync.RWMutex

	state                 State
	consecutiveFailures   int
	openedAt              time.Time
	halfOpenProbeInFlight bool
	nextProbeID           uint64
	halfOpenProbeID       uint64

	failureThreshold int
	cooldown         time.Duration
	now              func() time.Time
}

// New creates a Breaker in the Closed state.
func New(opts Options) *Breaker {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = defaultFailureThreshold
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = defaultCooldown
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Breaker{
		state:            Closed,
		failureThreshold: opts.FailureThreshold,
		cooldown:         opts.Cooldown,
		now:              opts.Now,
	}
}

// Execute runs req if the circuit allows it, and records the outcome.
// It returns ErrOpen without calling req when the circuit is open.
// A panic inside req is recorded as a failure and then re-panicked.
func (b *Breaker) Execute(req func() error) (err error) {
	probeID, err := b.beforeCall()
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			b.afterCall(probeID, fmt.Errorf("request panicked: %v", r))
			panic(r)
		}
	}()
	err = req()
	b.afterCall(probeID, err)
	return err
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// ConsecutiveFailures returns the current failure streak.
func (b *Breaker) ConsecutiveFailures() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.consecutiveFailures
}

// beforeCall decides whether a request may proceed. It returns a
// non-zero probeID only for the request admitted as the half-open
// probe; ordinary requests return 0.
func (b *Breaker) beforeCall() (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		return 0, nil
	case HalfOpen:
		// Only one probe may be in flight at a time.
		if b.halfOpenProbeInFlight {
			return 0, ErrOpen
		}
		b.nextProbeID++
		b.halfOpenProbeID = b.nextProbeID
		b.halfOpenProbeInFlight = true
		return b.halfOpenProbeID, nil
	case Open:
		if b.now().Sub(b.openedAt) < b.cooldown {
			return 0, ErrOpen
		}
		b.state = HalfOpen
		b.nextProbeID++
		b.halfOpenProbeID = b.nextProbeID
		b.halfOpenProbeInFlight = true
		return b.halfOpenProbeID, nil
	}
	return 0, ErrOpen
}

// afterCall records the outcome of a request admitted by beforeCall.
// Only the half-open probe's own result may resolve the HalfOpen
// state; results from requests admitted earlier are handled by the
// state they actually find the breaker in.
func (b *Breaker) afterCall(probeID uint64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case HalfOpen:
		// Stale request finishing mid-probe: ignore it entirely so it
		// cannot clear the in-flight flag or hijack the transition.
		if probeID != b.halfOpenProbeID {
			return
		}
		b.halfOpenProbeInFlight = false
		if err == nil {
			b.state = Closed
			b.consecutiveFailures = 0
		} else {
			b.state = Open
			b.openedAt = b.now()
		}
	case Closed:
		// Requests admitted in Closed (probeID == 0) count normally.
		// The probe itself (probeID != 0) only reaches this branch if
		// another path already moved the breaker to Closed; its success
		// was already accounted for, a late failure still counts.
		if err == nil {
			b.consecutiveFailures = 0
		} else {
			b.consecutiveFailures++
			if b.consecutiveFailures >= b.failureThreshold {
				b.state = Open
				b.openedAt = b.now()
			}
		}
	case Open:
		// A request admitted before the circuit opened that finishes
		// late does not alter the state; openedAt stays put.
	}
}
