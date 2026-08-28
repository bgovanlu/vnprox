package peer

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestResultCache_ConcurrentCallsCoalesceIntoOne proves the true in-flight
// collision case: N goroutines calling do() for the same key at the same
// time produce exactly one call to fn, and every goroutine gets fn's
// result.
func TestResultCache_ConcurrentCallsCoalesceIntoOne(t *testing.T) {
	c := newResultCache[int](PeerReadCoalesceTTL, nil)

	var calls int32
	release := make(chan struct{})
	fn := func() (int, error) {
		atomic.AddInt32(&calls, 1)
		<-release // hold every concurrent caller here until they've all arrived
		return 42, nil
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([]int, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			v, err := c.do("k", fn)
			if err != nil {
				t.Errorf("caller %d: unexpected error: %v", i, err)
			}
			results[i] = v
		}(i)
	}

	// Give every goroutine a chance to reach do() and block on either fn or
	// the shared entry's ready channel before releasing fn.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("fn called %d times, want exactly 1", got)
	}
	for i, v := range results {
		if v != 42 {
			t.Errorf("caller %d got %d, want 42", i, v)
		}
	}
}

// TestResultCache_SequentialWithinTTLCoalesce proves the shape T-3712
// actually found in production: not two concurrent goroutines, but one
// caller finishing and a second caller starting a few milliseconds later
// (findings.Engine's ipamFindings then rogueFindings, in the same
// goroutine, 24ms apart per the evidence) — both within resultCache's TTL,
// so the second must reuse the first's result rather than calling fn again.
func TestResultCache_SequentialWithinTTLCoalesce(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := newResultCache[int](5*time.Second, clock.now)

	var calls int
	fn := func() (int, error) {
		calls++
		return calls, nil
	}

	v1, err := c.do("k", fn)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	clock.advance(24 * time.Millisecond)
	v2, err := c.do("k", fn)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("fn called %d times, want exactly 1", calls)
	}
	if v1 != 1 || v2 != 1 {
		t.Fatalf("v1=%d v2=%d, want both == 1 (the single call's result)", v1, v2)
	}
}

// TestResultCache_ExpiresAfterTTL proves the cache doesn't coalesce forever
// — once ttl has elapsed since the owning call completed, the next caller
// triggers a fresh upstream call, so cross-cycle reads stay live.
func TestResultCache_ExpiresAfterTTL(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := newResultCache[int](5*time.Second, clock.now)

	var calls int
	fn := func() (int, error) {
		calls++
		return calls, nil
	}

	if _, err := c.do("k", fn); err != nil {
		t.Fatalf("first call: %v", err)
	}
	clock.advance(5*time.Second + time.Millisecond)
	v, err := c.do("k", fn)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if calls != 2 {
		t.Fatalf("fn called %d times, want exactly 2 (cache should have expired)", calls)
	}
	if v != 2 {
		t.Fatalf("v = %d, want 2 (the fresh call's result)", v)
	}
}

// TestResultCache_DifferentKeysNotCoalesced proves keying is per-target:
// two different keys (e.g. two different peer/node pairs) never share a
// result.
func TestResultCache_DifferentKeysNotCoalesced(t *testing.T) {
	c := newResultCache[string](PeerReadCoalesceTTL, nil)

	var calls int32
	fn := func(v string) func() (string, error) {
		return func() (string, error) {
			atomic.AddInt32(&calls, 1)
			return v, nil
		}
	}

	v1, err := c.do("peer-a\x00node-a", fn("a"))
	if err != nil {
		t.Fatalf("key a: %v", err)
	}
	v2, err := c.do("peer-a\x00node-b", fn("b"))
	if err != nil {
		t.Fatalf("key b: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("fn called %d times, want 2 (distinct keys must not coalesce)", got)
	}
	if v1 != "a" || v2 != "b" {
		t.Fatalf("v1=%q v2=%q, want a/b", v1, v2)
	}
}

// TestResultCache_ErrorNotCached proves a failed call doesn't block a
// subsequent caller for the rest of the TTL window — the very next do()
// for that key retries fn rather than replaying the same error.
func TestResultCache_ErrorNotCached(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := newResultCache[int](5*time.Second, clock.now)

	boom := errors.New("boom")
	var calls int
	fn := func() (int, error) {
		calls++
		if calls == 1 {
			return 0, boom
		}
		return 99, nil
	}

	_, err := c.do("k", fn)
	if !errors.Is(err, boom) {
		t.Fatalf("first call error = %v, want boom", err)
	}

	// No clock advance: a real retry immediately after a failure must not
	// be treated as "still fresh" the way a successful result would be.
	v, err := c.do("k", fn)
	if err != nil {
		t.Fatalf("second call: unexpected error %v", err)
	}
	if v != 99 {
		t.Fatalf("v = %d, want 99", v)
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want exactly 2 (error must not be cached)", calls)
	}
}
