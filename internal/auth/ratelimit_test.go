package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginLimiter_ExhaustsAfterCapacityThenRecoversOnRefill(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 3, RefillEvery: time.Second}, RateLimitConfig{}, clock)

	for i := 0; i < 3; i++ {
		if !limiter.allow("1.2.3.4", "alice") {
			t.Fatalf("attempt %d: allow = false, want true (within capacity)", i)
		}
	}
	if limiter.allow("1.2.3.4", "alice") {
		t.Fatal("4th rapid attempt: allow = true, want false (capacity exhausted)")
	}

	// Advance the clock by exactly one refill interval: exactly one more
	// token should be available, then exhausted again.
	now = now.Add(time.Second)
	if !limiter.allow("1.2.3.4", "alice") {
		t.Fatal("after refill: allow = false, want true")
	}
	if limiter.allow("1.2.3.4", "alice") {
		t.Fatal("second attempt right after refill: allow = true, want false")
	}
}

// TestLoginLimiter_DifferentIPUnaffectedByExhaustedIP is T-105 acceptance
// criterion 4's second half: "a good login from a different IP is
// unaffected". Deliberately uses a different username for the second IP
// too — the per-username bucket is a separate, intentional lockout
// dimension (docs/security.md: "per-IP and per-username token bucket"),
// so an attacker hammering one victim username from many IPs should still
// be throttled per-username; what must NOT happen is one attacking IP's
// exhaustion bleeding over into an unrelated user's login from a different
// IP.
func TestLoginLimiter_DifferentIPUnaffectedByExhaustedIP(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 2, RefillEvery: time.Minute}, RateLimitConfig{}, clock)

	for i := 0; i < 2; i++ {
		if !limiter.allow("10.0.0.1", "attacker-target") {
			t.Fatalf("attempt %d from 10.0.0.1: allow = false, want true", i)
		}
	}
	if limiter.allow("10.0.0.1", "attacker-target") {
		t.Fatal("10.0.0.1 should be exhausted")
	}

	if !limiter.allow("10.0.0.2", "gooduser") {
		t.Fatal("a different IP with a different, unrelated username should be unaffected by 10.0.0.1's exhaustion")
	}
}

func TestLoginLimiter_DifferentUsernameFromSameIPStillGatedByIP(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 1, RefillEvery: time.Minute}, RateLimitConfig{}, clock)

	if !limiter.allow("10.0.0.5", "alice") {
		t.Fatal("first attempt should be allowed")
	}
	if limiter.allow("10.0.0.5", "carol") {
		t.Fatal("a different username from the same already-exhausted IP should still be blocked")
	}
}

func TestTokenBucket_TenRapidAttemptsThenBlocked(t *testing.T) {
	// Mirrors T-105 acceptance criterion 4's literal wording: "10 rapid
	// bad logins -> 429" against the production DefaultRateLimitConfig
	// (capacity 10).
	now := time.Now()
	clock := func() time.Time { return now }
	b := newTokenBucket(DefaultRateLimitConfig, clock)

	for i := 0; i < DefaultRateLimitConfig.Capacity; i++ {
		if !b.allow("attacker") {
			t.Fatalf("attempt %d: allow = false, want true", i)
		}
	}
	if b.allow("attacker") {
		t.Fatal("11th rapid attempt: allow = true, want false")
	}
}

// TestTokenBucket_BoundedUnderDistinctKeyFlood is T-2905: the buckets map
// is keyed on attacker-supplied usernames, so a spray of distinct names
// must not grow it without bound. With a fake clock advancing past the
// full-refill window, swept entries disappear; even inside one window the
// hard ceiling holds.
func TestTokenBucket_BoundedUnderDistinctKeyFlood(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	b := newTokenBucket(RateLimitConfig{Capacity: 10, RefillEvery: 30 * time.Second}, func() time.Time { return clock })

	for i := 0; i < 200_000; i++ {
		b.allow(fmt.Sprintf("user-%d@pam", i))
		if i%10_000 == 0 {
			clock = clock.Add(time.Minute) // let sweeps run
		}
	}
	b.mu.Lock()
	n := len(b.buckets)
	b.mu.Unlock()
	if n > maxBucketEntries {
		t.Fatalf("bucket map grew to %d entries under a distinct-key flood, ceiling is %d", n, maxBucketEntries)
	}
}

// TestLoginLimiter_UsernameBucketOverrideIsIndependentOfIPBucket is T-3303:
// a public demo instance widens the login limiter's per-username bucket
// (every visitor mints against the same shared, low-privilege fixture
// credential) without touching its per-IP bucket. Proves both directions:
// a wide username bucket does not defeat per-IP throttling, and a narrow
// IP bucket does not get accidentally widened by the username override.
func TestLoginLimiter_UsernameBucketOverrideIsIndependentOfIPBucket(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	limiter := newLoginLimiter(
		RateLimitConfig{Capacity: 1, RefillEvery: time.Minute},   // per-IP: stays narrow
		RateLimitConfig{Capacity: 100, RefillEvery: time.Second}, // per-username: widened
		clock,
	)

	if !limiter.allow("10.0.0.9", "demo") {
		t.Fatal("first attempt should be allowed")
	}
	if limiter.allow("10.0.0.9", "demo") {
		t.Fatal("second rapid attempt from the SAME IP should still be blocked — the IP bucket was not widened")
	}

	// A flood of DIFFERENT IPs sharing the one demo username must not
	// exhaust the shared username bucket the way the pre-T-3303 behavior
	// would have (capacity 10 total, globally, across every visitor).
	for i := 0; i < 50; i++ {
		ip := fmt.Sprintf("10.1.2.%d", i)
		if !limiter.allow(ip, "demo") {
			t.Fatalf("visitor %d (ip %s): allow = false, want true — the widened username bucket should absorb this", i, ip)
		}
	}
}

// TestLoginLimiter_ZeroUsernameConfigFallsBackToIPConfig guards the
// backward-compatible default: a caller (every one before T-3303) that
// only ever set one RateLimitConfig must see identical behavior to before
// RateLimitByUsername existed — a per-IP-capacity-sized username bucket,
// not an unlimited or zero-capacity one.
func TestLoginLimiter_ZeroUsernameConfigFallsBackToIPConfig(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 2, RefillEvery: time.Minute}, RateLimitConfig{}, clock)

	for i := 0; i < 2; i++ {
		if !limiter.allow(fmt.Sprintf("10.2.0.%d", i), "shared") {
			t.Fatalf("attempt %d: allow = false, want true (within the fallback capacity)", i)
		}
	}
	if limiter.allow("10.2.0.99", "shared") {
		t.Fatal("3rd distinct-IP attempt against the same username: allow = true, want false — the username bucket should have fallen back to capacity 2, not gone unlimited")
	}
}
