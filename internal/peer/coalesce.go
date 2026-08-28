package peer

import (
	"sync"
	"time"
)

// PeerReadCoalesceTTL bounds how long resultCache treats a completed read as
// fresh enough to hand to a second caller instead of firing a second
// request (T-3712). It only needs to cover the width of one
// findings-engine/collector cycle's own internal fan-out, not the interval
// *between* cycles: T-3712's evidence
// (planning/reports/evidence/peer-neighbors-duplicate-poll-2026-08-25.txt)
// showed internal/ipam's Conflicts() and cmd/vnproxd's rogueScanAdapter
// both calling neighbor.Service.Neighbors within the same 30s
// findings.Engine cycle, 24ms apart — two sequential calls, not concurrent
// goroutines, so a pure in-flight singleflight (which only coalesces calls
// that overlap in time) would have missed this case entirely; the result
// has to stay cached briefly after the first call *returns*. Five seconds
// comfortably covers any such within-cycle burst while staying well under
// every UI refetchInterval in web/src (10s or slower) and the 30s
// findings/collector cadence itself, so cross-cycle reads still see fresh
// data on every cycle, never a stale one from the cycle before.
const PeerReadCoalesceTTL = 5 * time.Second

// resultCache coalesces calls to fn that share a key: the first caller for
// a key actually invokes fn, every other caller for that same key — whether
// it arrives while fn is still running (a true in-flight collision) or
// shortly after fn has already returned (the sequential-within-one-cycle
// case above) — gets the same result without a second upstream call, as
// long as it arrives within ttl of when that call *started*.
//
// Measured from call start, deliberately, not from completion: ttl bounds
// how stale the returned data may be, and the data is as old as the moment
// the upstream request was issued. A slow peer therefore shortens the
// reuse window rather than extending staleness past ttl — the conservative
// direction. Do not "fix" this to now-at-completion without re-reading
// that sentence; it would let a 3s peer call hand out 8s-old neighbours
// under a 5s ttl.
//
// This is deliberately not golang.org/x/sync/singleflight: singleflight
// only coalesces calls that are literally concurrent with the in-flight
// one and forgets the result the instant it returns, which does not cover
// the sequential-32ms-apart shape T-3712 found in production. Recording a
// short-lived, keyed result after completion is the whole of the
// difference, and stdlib sync is sufficient for it — see this task's
// report for why golang.org/x/sync/singleflight itself (present in go.mod
// only as an indirect dependency) was not pulled in for this.
//
// A nil *resultCache is not valid to call do on; every resultCache used in
// this package is constructed by NewClient.
// Field order is pointer-bearing first (now, entries), pointer-free last
// (mu, ttl): govet's fieldalignment measures bytes up to the final pointer,
// so a pointer-free field sitting above one costs alignment for nothing.
type resultCache[T any] struct {
	now     func() time.Time
	entries map[string]*cacheEntry[T]
	mu      sync.Mutex
	ttl     time.Duration
}

type cacheEntry[T any] struct {
	// ready is closed exactly once, when the call that owns this entry
	// (whichever goroutine created it) finishes. Every other caller that
	// found this entry already present blocks on ready rather than calling
	// fn itself.
	ready   chan struct{}
	val     T
	err     error
	expires time.Time
}

func newResultCache[T any](ttl time.Duration, now func() time.Time) *resultCache[T] {
	if now == nil {
		now = time.Now
	}
	return &resultCache[T]{ttl: ttl, now: now, entries: make(map[string]*cacheEntry[T])}
}

// do returns fn's result for key, calling fn at most once per ttl window
// regardless of how many callers ask for key concurrently or in quick
// succession. A failed call (err != nil) is never cached beyond delivering
// its result to callers who were already waiting on it — the very next
// caller for that key triggers a fresh attempt — so a transient peer
// failure can't hold every consumer's read hostage for the rest of the TTL
// window the way caching the error would.
func (c *resultCache[T]) do(key string, fn func() (T, error)) (T, error) {
	c.mu.Lock()
	now := c.now()
	if e, ok := c.entries[key]; ok {
		select {
		case <-e.ready:
			// e's owning call has already returned: reuse its result only
			// if it's still within ttl. A zero-value e.expires (impossible
			// here, since ready is only closed after expires is set) would
			// otherwise be misread as "expired" and this branch would
			// never coalesce a completed call — the bug this comment
			// exists to warn a future edit away from reintroducing.
			if e.expires.After(now) {
				c.mu.Unlock()
				return e.val, e.err
			}
			// Expired: fall through and start a fresh call, replacing this
			// entry below.
		default:
			// e's owning call is still in flight. It must be waited on
			// regardless of e.expires (which isn't set yet) — this is the
			// true in-flight collision case, distinct from the
			// already-completed-and-still-fresh case above.
			c.mu.Unlock()
			<-e.ready
			return e.val, e.err
		}
	}
	e := &cacheEntry[T]{ready: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	val, err := fn()

	c.mu.Lock()
	e.val, e.err = val, err
	if err == nil {
		e.expires = now.Add(c.ttl)
	} else {
		// Already-expired sentinel: leaves the entry in place (so any
		// caller currently blocked on e.ready still gets this result) but
		// makes the very next do() call for key take the fresh-call branch
		// above instead of reusing a failure.
		e.expires = now
	}
	close(e.ready)
	c.mu.Unlock()

	return val, err
}
