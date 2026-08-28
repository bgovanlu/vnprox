// SPDX-License-Identifier: Apache-2.0

package collect

// Direct unit tests for the loop timing helpers (audit F-20): jitter
// bounds, backoff doubling, the maxBackoff cap, and degenerate inputs.
// withJitter is randomized, so bounds are asserted over many draws rather
// than on a single value.

import (
	"testing"
	"time"
)

func TestWithJitter(t *testing.T) {
	const draws = 2000

	t.Run("bounds", func(t *testing.T) {
		base := 10 * time.Second
		lo := time.Duration(float64(base) * (1 - jitterFraction))
		hi := time.Duration(float64(base) * (1 + jitterFraction))
		var sawBelow, sawAbove bool
		for i := 0; i < draws; i++ {
			got := withJitter(base)
			if got < lo || got > hi {
				t.Fatalf("withJitter(%v) = %v, outside [%v, %v]", base, got, lo, hi)
			}
			if got < base {
				sawBelow = true
			}
			if got > base {
				sawAbove = true
			}
		}
		// The jitter must actually spread in both directions, not be a
		// constant offset (thundering-herd avoidance is its whole point).
		if !sawBelow || !sawAbove {
			t.Errorf("withJitter never varied both directions over %d draws (below=%v above=%v)", draws, sawBelow, sawAbove)
		}
	})

	t.Run("degenerate bases pass through", func(t *testing.T) {
		for _, base := range []time.Duration{0, -time.Second, 1, 5} {
			// Zero/negative bases are returned unchanged; bases so small
			// their 10% delta truncates to zero are, too. Never negative,
			// never zero for a positive base.
			got := withJitter(base)
			if base <= 0 && got != base {
				t.Errorf("withJitter(%v) = %v, want unchanged", base, got)
			}
			if base > 0 && got <= 0 {
				t.Errorf("withJitter(%v) = %v, want > 0", base, got)
			}
		}
	})
}

func TestBackoffFor(t *testing.T) {
	base := 10 * time.Second
	// jitterBounds returns the inclusive range withJitter can map d onto.
	jitterBounds := func(d time.Duration) (time.Duration, time.Duration) {
		delta := time.Duration(float64(d) * jitterFraction)
		return d - delta, d + delta
	}

	t.Run("zero failures means the plain jittered interval", func(t *testing.T) {
		lo, hi := jitterBounds(base)
		for i := 0; i < 200; i++ {
			if got := backoffFor(base, 0); got < lo || got > hi {
				t.Fatalf("backoffFor(%v, 0) = %v, outside [%v, %v]", base, got, lo, hi)
			}
			if got := backoffFor(base, -1); got < lo || got > hi {
				t.Fatalf("backoffFor(%v, -1) = %v, outside [%v, %v]", base, got, lo, hi)
			}
		}
	})

	t.Run("doubles per failure until the cap", func(t *testing.T) {
		// Pre-jitter targets for base=10s: 20s, 40s, then 60s (capped;
		// maxBackoff is 60s) for every further failure count.
		want := []time.Duration{20 * time.Second, 40 * time.Second, maxBackoff, maxBackoff, maxBackoff}
		prevTarget := time.Duration(0)
		for failures := 1; failures <= len(want); failures++ {
			target := want[failures-1]
			if target < prevTarget {
				t.Fatalf("expected targets must grow monotonically (test bug)")
			}
			prevTarget = target
			lo, hi := jitterBounds(target)
			for i := 0; i < 200; i++ {
				got := backoffFor(base, failures)
				if got < lo || got > hi {
					t.Fatalf("backoffFor(%v, %d) = %v, outside [%v, %v]", base, failures, got, lo, hi)
				}
			}
		}
	})

	t.Run("never exceeds the jittered cap", func(t *testing.T) {
		_, hi := jitterBounds(maxBackoff)
		for _, failures := range []int{6, 10, 30, 63, 64, 1000} {
			for i := 0; i < 50; i++ {
				if got := backoffFor(base, failures); got > hi {
					t.Fatalf("backoffFor(%v, %d) = %v exceeds jittered cap %v", base, failures, got, hi)
				}
			}
		}
	})
}
