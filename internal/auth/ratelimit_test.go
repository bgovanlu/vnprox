package auth

import (
	"testing"
	"time"
)

func TestLoginLimiter_ExhaustsAfterCapacityThenRecoversOnRefill(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 3, RefillEvery: time.Second}, clock)

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
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 2, RefillEvery: time.Minute}, clock)

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
	limiter := newLoginLimiter(RateLimitConfig{Capacity: 1, RefillEvery: time.Minute}, clock)

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
