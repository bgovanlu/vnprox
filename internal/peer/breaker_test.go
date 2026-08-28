// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"testing"
	"time"
)

func TestCircuitBreaker_OpensAfterThresholdAndRecoversAfterTimeout(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	b := newCircuitBreaker(3, 10*time.Second, clock.now)

	for i := 0; i < 2; i++ {
		if !b.allow() {
			t.Fatalf("call %d: allow() = false, want true (breaker should still be closed)", i)
		}
		b.recordFailure()
	}
	// Still below threshold.
	if !b.allow() {
		t.Fatal("allow() = false before the failure threshold was reached")
	}
	b.recordFailure() // 3rd consecutive failure: opens the breaker.

	if b.allow() {
		t.Fatal("allow() = true immediately after the breaker opened")
	}

	clock.advance(5 * time.Second)
	if b.allow() {
		t.Fatal("allow() = true before resetTimeout elapsed")
	}

	clock.advance(6 * time.Second) // total 11s > 10s resetTimeout
	if !b.allow() {
		t.Fatal("allow() = false after resetTimeout elapsed — half-open probe should be allowed")
	}

	// The half-open probe succeeds: breaker closes.
	b.recordSuccess()
	if !b.allow() {
		t.Fatal("allow() = false after a successful half-open probe closed the breaker")
	}
}

func TestCircuitBreaker_FailedHalfOpenProbeReopens(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	b := newCircuitBreaker(1, 10*time.Second, clock.now)

	if !b.allow() {
		t.Fatal("allow() = false on a fresh breaker")
	}
	b.recordFailure() // threshold 1: opens immediately.

	clock.advance(11 * time.Second)
	if !b.allow() {
		t.Fatal("allow() = false after resetTimeout elapsed")
	}
	b.recordFailure() // half-open probe fails: reopens immediately, not after another 3 failures.

	if b.allow() {
		t.Fatal("allow() = true immediately after a failed half-open probe")
	}

	clock.advance(11 * time.Second)
	if !b.allow() {
		t.Fatal("allow() = false after resetTimeout elapsed a second time")
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	b := newCircuitBreaker(3, 10*time.Second, clock.now)

	b.recordFailure()
	b.recordFailure()
	b.recordSuccess()
	b.recordFailure()
	b.recordFailure()
	// Only 2 consecutive failures since the last success — still closed.
	if !b.allow() {
		t.Fatal("allow() = false after only 2 consecutive failures post-reset")
	}
}
