package telemetrycollector

import (
	"strconv"
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenBlocks(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	l := newLimiter(3, time.Minute, clock)

	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatalf("call %d: expected allow", i)
		}
	}
	if l.allow("a") {
		t.Fatal("4th call within the burst window should be refused")
	}

	// A different key has its own bucket.
	if !l.allow("b") {
		t.Fatal("a distinct key must not be affected by key a's exhaustion")
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	l := newLimiter(1, time.Minute, clock)

	if !l.allow("a") {
		t.Fatal("first call should be allowed")
	}
	if l.allow("a") {
		t.Fatal("second call before refill should be refused")
	}
	now = now.Add(time.Minute)
	if !l.allow("a") {
		t.Fatal("call after one refill interval should be allowed")
	}
}

func TestLimiterSweepBoundsMapGrowth(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	l := newLimiter(1, time.Millisecond, clock)

	for i := 0; i < maxBucketEntries+10; i++ {
		l.allow(strconv.Itoa(i))
	}
	now = now.Add(time.Hour)
	// One more call triggers a sweep pass (len >= maxBucketEntries).
	l.allow("trigger-sweep")

	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n >= maxBucketEntries+11 {
		t.Fatalf("bucket map did not shrink after a sweep: %d entries", n)
	}
}
