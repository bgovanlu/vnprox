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
//
// Delta-attribution caveat: the before/after snapshots bracket this loop's
// poll step, but the graph is shared. A concurrent loop (or RefreshNow)
// that commits changes inside this window has those changes attributed to
// this batch as well — and its own bracketing diff will typically report
// them a second time. This is deliberate: consumers treat a Delta batch as
// an idempotent "something changed, re-read the snapshot" hint, so
// occasional double-attribution is harmless, whereas exactly-once
// attribution would require serializing every poll cycle behind one global
// lock. (RefreshNow's "exactly one batch per call" guarantee therefore
// holds strictly only when no other loop mutates the graph during the
// call; see refresh.go.)
func (c *Collector) attempt(ctx context.Context, name string, fn pollFunc) {
	before := c.graph.Snapshot()
	start := time.Now()
	err := fn(ctx)
	after := c.graph.Snapshot()

	c.recordResult(name, start, err)
	// T-1903: "host" reports its own per-node OnPoll calls from inside
	// hostPollOnce (host.go) — one call here too would double-report the
	// same cycle at two different granularities, so it's skipped here.
	// "pve" is cluster-wide (node ""); "lldp" polls only the local node.
	if name != "host" {
		c.reportPoll(name, pollScopeNode(name, c), time.Since(start), err)
	}
	c.emitDelta(name, diffSnapshots(before, after))
}

// pollScopeNode returns the node an OnPoll observation for the named
// cluster-wide/local-scoped source should carry: "" for "pve" (cluster-
// wide), the currently-known local node for anything else ("lldp") — the
// same scoping collect.SourceStatus already documents.
func pollScopeNode(name string, c *Collector) string {
	if name == "pve" {
		return ""
	}
	return c.getLocalNode()
}
