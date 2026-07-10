package peer

import (
	"sync"
	"time"
)

// breakerState is a per-peer circuit breaker's state.
type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// circuitBreaker implements the classic closed/open/half-open state
// machine, scoped to one peer: after failureThreshold consecutive
// transport-level failures it "opens" (every call fast-fails without
// attempting the network) for resetTimeout, then allows exactly one
// half-open probe; that probe's outcome either closes the breaker
// (success) or re-opens it for another resetTimeout (failure). This is
// what makes a dead peer fast-fail (docs/architecture.md's cluster model:
// every node must degrade gracefully when a peer is unreachable) instead
// of every caller separately paying a full dial/TLS-handshake timeout.
type circuitBreaker struct {
	now                 func() time.Time
	openedAt            time.Time
	failureThreshold    int
	resetTimeout        time.Duration
	consecutiveFailures int
	state               breakerState
	mu                  sync.Mutex
}

func newCircuitBreaker(failureThreshold int, resetTimeout time.Duration, now func() time.Time) *circuitBreaker {
	return &circuitBreaker{
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		now:              now,
		state:            breakerClosed,
	}
}

// allow reports whether a call should be attempted. An open breaker whose
// resetTimeout has elapsed transitions to half-open and allows exactly this
// one probe through.
func (b *circuitBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case breakerOpen:
		if b.now().Sub(b.openedAt) < b.resetTimeout {
			return false
		}
		b.state = breakerHalfOpen
		return true
	case breakerHalfOpen:
		// Only one probe in flight at a time; further callers are told to
		// back off until the probe resolves (recordSuccess/recordFailure).
		return false
	default:
		return true
	}
}

// recordSuccess closes the breaker and resets the failure count.
func (b *circuitBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = breakerClosed
	b.consecutiveFailures = 0
}

// recordFailure counts a transport-level failure and opens the breaker
// once failureThreshold consecutive failures accumulate, or immediately if
// a half-open probe itself failed.
func (b *circuitBreaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures++
	if b.state == breakerHalfOpen || b.consecutiveFailures >= b.failureThreshold {
		b.state = breakerOpen
		b.openedAt = b.now()
	}
}
