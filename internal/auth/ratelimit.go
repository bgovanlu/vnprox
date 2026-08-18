package auth

import (
	"sync"
	"time"
)

// RateLimitConfig configures one keyed token bucket family (per-IP or
// per-username). Capacity is the burst size; RefillEvery is how often one
// token is added back (so steady-state throughput is 1/RefillEvery
// requests/sec). Zero values fall back to DefaultRateLimitConfig.
type RateLimitConfig struct {
	Capacity    int
	RefillEvery time.Duration
}

// DefaultRateLimitConfig is the production default: 10 attempts before
// throttling kicks in (matching T-105's acceptance criterion 4 wording,
// "10 rapid bad logins"), refilling one token every 30s thereafter.
var DefaultRateLimitConfig = RateLimitConfig{Capacity: 10, RefillEvery: 30 * time.Second}

func (c RateLimitConfig) orDefault() RateLimitConfig {
	if c.Capacity <= 0 {
		c.Capacity = DefaultRateLimitConfig.Capacity
	}
	if c.RefillEvery <= 0 {
		c.RefillEvery = DefaultRateLimitConfig.RefillEvery
	}
	return c
}

// tokenBucket is a simple keyed rate limiter: each key (an IP or a
// username) gets its own bucket, created lazily on first use. It is safe
// for concurrent use.
type tokenBucket struct {
	buckets map[string]*bucketState
	now     func() time.Time
	// lastSweep is T-2905's growth bound: buckets is keyed on
	// attacker-supplied values (req.Username), so without a sweep a
	// credential spray with distinct usernames grows it forever.
	lastSweep time.Time
	cfg       RateLimitConfig
	mu        sync.Mutex
}

// maxBucketEntries is the hard ceiling on tracked keys. Reaching it means
// something is spraying; dropping the oldest fully-refilled entries first
// keeps genuinely-throttled keys throttled.
const maxBucketEntries = 65536

type bucketState struct {
	last   time.Time
	tokens float64
}

func newTokenBucket(cfg RateLimitConfig, now func() time.Time) *tokenBucket {
	if now == nil {
		now = time.Now
	}
	return &tokenBucket{
		cfg:     cfg.orDefault(),
		buckets: make(map[string]*bucketState),
		now:     now,
	}
}

// allow consumes one token for key, returning false if none was available
// (the caller should respond 429). A fresh key starts with a full bucket
// (capacity - 1 tokens remaining after this call succeeds), so the very
// first attempt from any IP/username is never itself rate-limited.
func (b *tokenBucket) allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.sweep(now)
	st, ok := b.buckets[key]
	if !ok {
		st = &bucketState{tokens: float64(b.cfg.Capacity), last: now}
		b.buckets[key] = st
	} else {
		elapsed := now.Sub(st.last)
		if elapsed > 0 {
			refill := elapsed.Seconds() / b.cfg.RefillEvery.Seconds()
			st.tokens += refill
			if st.tokens > float64(b.cfg.Capacity) {
				st.tokens = float64(b.cfg.Capacity)
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

// loginLimiter pairs a per-IP and a per-username tokenBucket: a login
// attempt is allowed only if both have capacity, per docs/security.md
// ("Login rate limiting: per-IP and per-username token bucket").
type loginLimiter struct {
	byIP       *tokenBucket
	byUsername *tokenBucket
}

// newLoginLimiter builds the per-IP and per-username buckets. usernameCfg's
// zero value (Capacity <= 0 && RefillEvery <= 0) falls back to ipCfg, so a
// caller that only ever set one RateLimitConfig (every call site before
// T-3303) gets identical behavior to before this split existed.
func newLoginLimiter(ipCfg, usernameCfg RateLimitConfig, now func() time.Time) *loginLimiter {
	if usernameCfg.Capacity <= 0 && usernameCfg.RefillEvery <= 0 {
		usernameCfg = ipCfg
	}
	return &loginLimiter{
		byIP:       newTokenBucket(ipCfg, now),
		byUsername: newTokenBucket(usernameCfg, now),
	}
}

// allow reports whether a login attempt from ip for username may proceed.
// Both the per-IP and per-username buckets must have capacity; checking
// byIP first means a username whose bucket is still full never gets
// charged for an attempt that was going to be rejected anyway because its
// IP is already throttled.
func (l *loginLimiter) allow(ip, username string) bool {
	if !l.byIP.allow(ip) {
		return false
	}
	if !l.byUsername.allow(username) {
		return false
	}
	return true
}

// sweep (T-2905) drops entries whose bucket has fully refilled — a key
// with a full bucket is indistinguishable from an untracked key, since a
// fresh key starts full — at most once a minute, plus unconditionally when
// the map hits maxBucketEntries. Mirrors internal/peer's replayCache
// discipline: no map keyed on untrusted input grows without bound.
// Callers hold b.mu.
func (b *tokenBucket) sweep(now time.Time) {
	if now.Sub(b.lastSweep) < time.Minute && len(b.buckets) < maxBucketEntries {
		return
	}
	b.lastSweep = now
	refillWindow := time.Duration(float64(b.cfg.Capacity) * float64(b.cfg.RefillEvery))
	for k, st := range b.buckets {
		if st.tokens >= float64(b.cfg.Capacity) || now.Sub(st.last) >= refillWindow {
			delete(b.buckets, k)
		}
	}
	// Still at the ceiling after removing every refilled entry: every
	// remaining key is genuinely mid-throttle, which at 65k keys means a
	// distributed spray. Refusing NEW keys would lock out legitimate users
	// behind fresh IPs; dropping the map fails open on rate limiting but
	// bounded — and the per-username buckets (and PVE's own auth) still
	// stand behind it. Logged by the caller's own 429 pattern being absent.
	if len(b.buckets) >= maxBucketEntries {
		b.buckets = make(map[string]*bucketState)
	}
}
