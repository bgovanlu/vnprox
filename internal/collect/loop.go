package collect

import (
	"context"
	"math/rand/v2"
	"time"
)

// jitterFraction is how much a poll interval is randomized by (±10%),
// per deliverable 1: "jittered ±10% or similar to avoid thundering-herd if
// multiple vnproxd instances existed".
const jitterFraction = 0.10

// withJitter returns base randomized by up to ±jitterFraction.
func withJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	delta := time.Duration(float64(base) * jitterFraction)
	if delta <= 0 {
		return base
	}
	offset := rand.Int64N(2*int64(delta)+1) - int64(delta)
	result := base + time.Duration(offset)
	if result <= 0 {
		return base
	}
	return result
}

// backoffFor computes the next wait for a loop with the given base interval
// and current consecutive-failure count: 0 failures means the normal
// jittered interval; each additional failure doubles the wait, capped at
// maxBackoff. Deliverable 5: "back off (exponential with a cap is fine) and
// keep retrying".
func backoffFor(base time.Duration, failures int) time.Duration {
	if failures <= 0 {
		return withJitter(base)
	}
	d := base
	for i := 0; i < failures; i++ {
		d *= 2
		if d >= maxBackoff {
			d = maxBackoff
			break
		}
	}
	return withJitter(d)
}

// pollFunc is one loop's poll step.
type pollFunc func(ctx context.Context) error

// runLoop drives one named poll loop: it polls immediately, then repeats on
// a jittered interval (with exponential backoff after consecutive
// failures), until ctx is cancelled — at which point it returns nil
// promptly (deliverable 6, "clean shutdown"; this is the signature
// cmd/vnproxd's runGroup expects an actor to have).
//
// Every attempt's effect on the graph is reported as one Delta batch via
// onCycle (diffing a Graph.Snapshot() taken immediately before and after
// the poll step), and every attempt's success/failure updates name's
// staleness/backoff bookkeeping — regardless of whether the poll step
// itself returned an error, since a poll step may have partially applied
// some sub-steps before failing on a later one.
func (c *Collector) runLoop(ctx context.Context, name string, interval time.Duration, fn pollFunc) error {
	c.attempt(ctx, name, fn)
	for {
		wait := backoffFor(interval, c.consecutiveFailures(name))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return nil
		case <-t.C:
			c.attempt(ctx, name, fn)
		}
	}
}

// attempt runs one poll step, recording its staleness/backoff result and
// emitting a merged Delta (via onDelta) if it changed the graph.
func (c *Collector) attempt(ctx context.Context, name string, fn pollFunc) {
	before := c.graph.Snapshot()
	start := time.Now()
	err := fn(ctx)
	after := c.graph.Snapshot()

	c.recordResult(name, start, err)
	c.emitDelta(name, diffSnapshots(before, after))
}
