package findings

import "sync"

// debounceState is one tracked key's consecutive-observation counters (AC3:
// "threshold checks don't flap on noisy fixtures"). A breach must be
// observed riseThreshold times in a row before the check starts reporting
// it as active; once active, a non-breaching observation must repeat
// fallThreshold times in a row before it clears. A single noisy sample in
// either direction never flips the reported state on its own.
type debounceState struct {
	consecutiveOver  int
	consecutiveUnder int
	active           bool
}

// debouncer holds hysteresis state for every key a stateful health check
// tracks across Engine cycles (a bond ref, a bridge ref, a node name, ...).
// Safe for concurrent use; Engine holds one debouncer per stateful check
// family so unrelated checks never share (or contend on) state.
type debouncer struct {
	state map[string]*debounceState
	mu    sync.Mutex
}

func newDebouncer() *debouncer {
	return &debouncer{state: map[string]*debounceState{}}
}

// Evaluate records one cycle's breach/clear observation for key and returns
// whether the check should currently report key as actively firing, per the
// rise/fall hysteresis described on debounceState. rise and fall must both
// be >= 1; a value of 1 degrades to "fire/clear immediately" (no debounce)
// for callers that want that on one side only (e.g. clear-immediately,
// fire-after-3).
func (d *debouncer) Evaluate(key string, breach bool, rise, fall int) bool {
	if rise < 1 {
		rise = 1
	}
	if fall < 1 {
		fall = 1
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.state[key]
	if st == nil {
		st = &debounceState{}
		d.state[key] = st
	}

	if breach {
		st.consecutiveOver++
		st.consecutiveUnder = 0
		if !st.active && st.consecutiveOver >= rise {
			st.active = true
		}
	} else {
		st.consecutiveUnder++
		st.consecutiveOver = 0
		if st.active && st.consecutiveUnder >= fall {
			st.active = false
		}
	}
	return st.active
}

// Prune drops tracked state for any key not present in liveKeys, so a
// vanished entity (e.g. a bond removed from inventory) doesn't leak state
// forever. Called once per Engine cycle after every Evaluate call for that
// cycle's check family.
func (d *debouncer) Prune(liveKeys map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.state {
		if !liveKeys[k] {
			delete(d.state, k)
		}
	}
}
