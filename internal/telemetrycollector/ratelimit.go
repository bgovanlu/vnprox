// SPDX-License-Identifier: Apache-2.0

package telemetrycollector

// ratelimit.go bounds submission throughput WITHOUT ever reading the
// request's source IP (see doc.go and docs/security.md's collector
// section for why). Two independent buckets:
//
//   - per-install: keyed on the payload's own InstallID, the one
//     correlator the client already sends. This is what "rate limiting
//     per source" means for a payload whose whole design goal is to carry
//     no other notion of "source".
//   - global: one shared bucket with no key at all, bounding total
//     throughput regardless of how many distinct (but validly-shaped)
//     install ids a flood presents — a per-install limiter alone does not
//     bound a flood of freshly-generated ULIDs, each submitting once.
//
// The design mirrors internal/auth's tokenBucket (loginLimiter's per-IP /
// per-username split) closely enough to recognise, but is not shared code:
// that package's tokenBucket is unexported, and importing internal/auth
// into a payload-privacy-focused package for one generic data structure
// would be the wrong dependency to take on for it.

import (
	"sync"
	"time"
)

// maxBucketEntries bounds the per-install map's growth: a flood of freshly
// generated ULIDs, each used once, must not grow this map without limit.
// Matches internal/auth's tokenBucket sizing rationale.
const maxBucketEntries = 65536

// limiter is a keyed token bucket family. The empty key ("") is a valid
// key like any other — used by the global, unkeyed bucket.
type limiter struct {
	now         func() time.Time
	buckets     map[string]*bucketState
	lastSweep   time.Time
	capacity    float64
	refillEvery time.Duration
	mu          sync.Mutex
}

type bucketState struct {
	last   time.Time
	tokens float64
}

// newLimiter builds a limiter with the given burst capacity and refill
// cadence (one token added back every refillEvery). now defaults to
// time.Now; tests inject a controllable clock.
func newLimiter(capacity int, refillEvery time.Duration, now func() time.Time) *limiter {
	if now == nil {
		now = time.Now
	}
	if capacity <= 0 {
		capacity = 1
	}
	if refillEvery <= 0 {
		refillEvery = time.Second
	}
	return &limiter{
		capacity:    float64(capacity),
		refillEvery: refillEvery,
		buckets:     make(map[string]*bucketState),
		now:         now,
	}
}

// allow consumes one token for key, returning false if none was available.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	st, ok := l.buckets[key]
	if !ok {
		st = &bucketState{tokens: l.capacity, last: now}
		l.buckets[key] = st
	} else {
		elapsed := now.Sub(st.last)
		if elapsed > 0 {
			st.tokens += elapsed.Seconds() / l.refillEvery.Seconds()
			if st.tokens > l.capacity {
				st.tokens = l.capacity
			}
			st.last = now
		}
	}

	if st.tokens < 1 {
		return false
	}
	st.tokens--
	return true
}

// sweep drops fully-refilled entries once the map is large, and at most
// once a minute — cheap enough to run on the hot path unconditionally, but
// gated so a burst of distinct keys doesn't make every call pay for a full
// map scan. Mirrors internal/auth's tokenBucket.sweep, including its
// fail-open ceiling: an entry counts as refilled either because its stored
// token count already says so, or because enough wall-clock time has
// passed since it was last touched that it would refill on next use even
// though nothing has recomputed that yet — and if the map is still at the
// ceiling after removing every refilled entry, every remaining key is
// genuinely mid-throttle, which at maxBucketEntries keys is a spray; the
// map is dropped rather than refusing new keys, because refusing them
// would lock out legitimate installs behind fresh ids while the per-key
// throttle itself is failing safe (the global limiter still stands).
func (l *limiter) sweep(now time.Time) {
	if len(l.buckets) < maxBucketEntries {
		return
	}
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	refillWindow := time.Duration(l.capacity * float64(l.refillEvery))
	for k, st := range l.buckets {
		if st.tokens >= l.capacity || now.Sub(st.last) >= refillWindow {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= maxBucketEntries {
		l.buckets = make(map[string]*bucketState)
	}
}
