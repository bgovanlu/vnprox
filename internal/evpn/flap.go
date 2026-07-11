package evpn

import (
	"sync"
	"time"
)

// DefaultFlapWindow and DefaultFlapThreshold are the flap-detection
// defaults (docs/features/sdn.md §3: "Flapping sessions raise a health
// finding"). docs/development.md doesn't pin numeric values for this kind
// of tunable (see docs/architecture.md's peer-client timeout consts for
// the same "chosen conservatively, overridable" precedent this follows):
// more than 3 state transitions inside a 10-minute trailing window is a
// session that is not just recovering once, it is genuinely unstable.
const (
	DefaultFlapWindow    = 10 * time.Minute
	DefaultFlapThreshold = 3
)

// flapKey identifies one peering session for flap tracking: the local
// node observing the session, plus the remote peer address.
type flapKey struct{ node, peerAddr string }

// stateSample is one observed session state at a point in time.
type stateSample struct {
	at    time.Time
	state string
}

// flapTracker is Service's in-memory, mutex-guarded rolling history of
// observed BGP session states, per (node, peerAddr) session. It exists
// because GET /sdn/evpn/status (unlike GET /sdn) is inherently about
// change over time — a single live snapshot can never show flapping, only
// repeated observations across polls can — so the Service instance itself
// (long-lived, one per daemon process) accumulates this history across
// requests, the same "collector with its own short-horizon history"
// pattern docs/architecture.md §2 assigns internal/metrics for interface
// counter rings.
type flapTracker struct {
	history   map[flapKey][]stateSample
	window    time.Duration
	threshold int
	mu        sync.Mutex
}

func newFlapTracker(window time.Duration, threshold int) *flapTracker {
	if window <= 0 {
		window = DefaultFlapWindow
	}
	if threshold <= 0 {
		threshold = DefaultFlapThreshold
	}
	return &flapTracker{
		history:   make(map[flapKey][]stateSample),
		window:    window,
		threshold: threshold,
	}
}

// observe records state for (node, peerAddr) at time at, prunes samples
// older than the tracker's window, and returns the number of state
// *transitions* (adjacent samples whose state differs) remaining in the
// pruned window. Consecutive identical-state observations (the common
// case: the same request re-polling a stable session) never count as a
// transition, so a session polled every few seconds but never changing
// state accumulates history without ever flapping.
func (t *flapTracker) observe(node, peerAddr string, at time.Time, state string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := flapKey{node: node, peerAddr: peerAddr}
	hist := t.history[key]
	if len(hist) == 0 || hist[len(hist)-1].state != state {
		hist = append(hist, stateSample{at: at, state: state})
	} else {
		// Same state observed again: refresh the timestamp of the latest
		// sample rather than appending a duplicate, so the window prune
		// below is based on how recently this state was last confirmed,
		// not how long ago it first started (an old, since-repeated
		// sample must not artificially keep an unrelated older transition
		// inside the window forever).
		hist[len(hist)-1].at = at
	}

	cutoff := at.Add(-t.window)
	i := 0
	for i < len(hist) && hist[i].at.Before(cutoff) {
		i++
	}
	hist = hist[i:]
	t.history[key] = hist

	return countTransitions(hist)
}

func countTransitions(hist []stateSample) int {
	n := 0
	for i := 1; i < len(hist); i++ {
		if hist[i].state != hist[i-1].state {
			n++
		}
	}
	return n
}

// flapping reports whether transitions meets or exceeds the tracker's
// configured threshold.
func (t *flapTracker) flapping(transitions int) bool {
	return transitions >= t.threshold
}

// forget drops all recorded history for (node, peerAddr) — used when a
// poll no longer observes a session at all (e.g. FRR became unavailable,
// or the peer was removed from config), so a stale flap finding does not
// linger forever for a session that no longer exists.
func (t *flapTracker) forget(node, peerAddr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.history, flapKey{node: node, peerAddr: peerAddr})
}
