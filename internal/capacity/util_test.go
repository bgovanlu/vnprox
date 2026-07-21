package capacity

import (
	"context"
	"testing"
	"time"
)

func TestLinkDailyUtil(t *testing.T) {
	base := day0
	// Interval 1: +12.5 MB rx over 1s = 1e8 bps = 10% of a 1000 Mbps link.
	// Interval 2: +25 MB tx over 1s = 2e8 bps = 20% (tx is the busier
	// direction, so it drives utilization).
	samples := []CounterSample{
		{At: base, RxBytes: 0, TxBytes: 0},
		{At: base.Add(time.Second), RxBytes: 12_500_000, TxBytes: 0},
		{At: base.Add(2 * time.Second), RxBytes: 12_500_000, TxBytes: 25_000_000},
	}
	avg, max, ok := LinkDailyUtil(samples, 1000)
	if !ok {
		t.Fatal("LinkDailyUtil ok=false for a valid series")
	}
	if !approx(max, 20, 0.01) {
		t.Errorf("max = %.4f, want ~20", max)
	}
	if !approx(avg, 15, 0.01) {
		t.Errorf("avg = %.4f, want ~15 (mean of 10%% and 20%%)", avg)
	}
}

func TestLinkDailyUtil_UnknownSpeedOrTooFewSamples(t *testing.T) {
	one := []CounterSample{{At: day0}}
	if _, _, ok := LinkDailyUtil(one, 1000); ok {
		t.Error("ok=true for a single sample, want false")
	}
	two := []CounterSample{{At: day0}, {At: day0.Add(time.Second), RxBytes: 1000}}
	if _, _, ok := LinkDailyUtil(two, 0); ok {
		t.Error("ok=true for unknown speed, want false")
	}
}

func TestLinkDailyUtil_CounterResetIgnored(t *testing.T) {
	// A backwards counter (reset) contributes a zero delta, not a spike.
	samples := []CounterSample{
		{At: day0, RxBytes: 1_000_000_000},
		{At: day0.Add(time.Second), RxBytes: 0},
	}
	avg, max, ok := LinkDailyUtil(samples, 1000)
	if !ok || avg != 0 || max != 0 {
		t.Errorf("reset handling: avg=%.2f max=%.2f ok=%v, want 0/0/true", avg, max, ok)
	}
}

func TestPoolUtil(t *testing.T) {
	if got := PoolUtil(64, 256); !approx(got, 25, 0.001) {
		t.Errorf("PoolUtil(64,256) = %.4f, want 25", got)
	}
	if got := PoolUtil(5, 0); got != 0 {
		t.Errorf("PoolUtil with zero total = %.4f, want 0", got)
	}
}

func approx(a, b, tol float64) bool {
	d := a - b
	return d < tol && d > -tol
}

// fakeSink records upserts keyed by (ref, kind, bucket) so a rollup test can
// assert idempotency (no duplicate keys) without a real store.
type fakeSink struct {
	rows map[string]Aggregate
}

func newFakeSink() *fakeSink { return &fakeSink{rows: map[string]Aggregate{}} }

func (s *fakeSink) Upsert(_ context.Context, a Aggregate) error {
	s.rows[string(a.Kind)+"|"+a.Ref+"|"+a.BucketAt.Format(time.RFC3339)] = a
	return nil
}
