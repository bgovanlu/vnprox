// health_stpburst.go implements docs/features/monitoring.md §5's "STP
// topology change bursts" check.
//
// Linux does not expose a cumulative STP topology-change counter through
// sysfs or through the netlink attributes vishvananda/netlink surfaces (the
// kernel bridge code tracks a topology_change *flag*, not a counter, and
// clears it once the forwarding-delay timer expires) — there is no ready
// substrate to sample the way the error/drop-rate checks sample
// metrics.Sampler. Rather than invent a fake counter with unverifiable
// semantics, this check uses a defensible functional proxy already available
// from existing collection: a real STP topology change's visible effect on
// a bridge is exactly a change in its forwarding port set (a port stops or
// starts forwarding), which host-netlink already reports every host-loop
// tick as inventory.Bridge.PortNames. Repeated churn in that set within a
// short window — a link flapping, a redundant path re-converging over and
// over — is what "topology change bursts" mean operationally, whether or
// not it happens to line up 1:1 with the kernel's own internal TCN count.
//
// This needs hardware validation: confirming the proxy actually tracks real
// STP TCN events (as opposed to, say, a single NIC hotplug looking like one
// churn) requires a live switched topology this task's fixtures cannot
// reproduce. Flagged in the T-602 completion report.

package findings

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckSTPTopologyBurst = "stp_topology_burst"

const stpTopologyBurstDocsLink = "docs/features/monitoring.md#5-health-checks"

// stpBurstWindow/stpBurstThreshold: a bridge whose forwarding port set has
// changed stpBurstThreshold or more times within stpBurstWindow is reported
// as bursting. A single change (a planned maintenance action, a one-off NIC
// replacement) never fires on its own.
const (
	stpBurstWindow    = 10 * time.Minute
	stpBurstThreshold = 3
)

// stpBridgeState is one bridge's forwarding-port-set change history.
type stpBridgeState struct {
	lastPortKey string
	changeTimes []time.Time
	seen        bool
}

// stpBurstTracker holds every bridge's stpBridgeState across Engine cycles.
// Safe for concurrent use.
type stpBurstTracker struct {
	state map[string]*stpBridgeState
	mu    sync.Mutex
}

func newStpBurstTracker() *stpBurstTracker {
	return &stpBurstTracker{state: map[string]*stpBridgeState{}}
}

// observe records ref's current forwarding-port-set key at now, appending a
// churn event iff it differs from the last-observed key (and a key has
// already been observed once — the very first observation never counts,
// avoiding a false burst on daemon startup), then returns how many churn
// events remain within the trailing window.
func (t *stpBurstTracker) observe(ref, portKey string, now time.Time, window time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.state[ref]
	if st == nil {
		st = &stpBridgeState{}
		t.state[ref] = st
	}
	if st.seen && st.lastPortKey != portKey {
		st.changeTimes = append(st.changeTimes, now)
	}
	st.seen = true
	st.lastPortKey = portKey

	cutoff := now.Add(-window)
	kept := st.changeTimes[:0]
	for _, ct := range st.changeTimes {
		if ct.After(cutoff) {
			kept = append(kept, ct)
		}
	}
	st.changeTimes = kept
	return len(st.changeTimes)
}

func (t *stpBurstTracker) prune(live map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.state {
		if !live[k] {
			delete(t.state, k)
		}
	}
}

// portSetKey renders a bridge's current forwarding-port-set as a stable,
// order-independent string for change comparison.
func portSetKey(names []string) string {
	sorted := sortedUnique(names)
	if len(sorted) == 0 {
		return "(none)"
	}
	return strings.Join(sorted, ",")
}

// checkSTPTopologyBurst is the CheckSTPTopologyBurst family.
func checkSTPTopologyBurst(snap inventory.Snapshot, tracker *stpBurstTracker, now time.Time) []Finding {
	var out []Finding
	live := map[string]bool{}

	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok || len(br.PortNames) == 0 {
			continue
		}
		ref := br.GetRef().String()
		live[ref] = true

		count := tracker.observe(ref, portSetKey(br.PortNames), now, stpBurstWindow)
		if count < stpBurstThreshold {
			continue
		}
		detail := fmt.Sprintf("bridge %s on node %s has changed its forwarding port set %d times in the last %s — possible STP topology instability (a flapping link or a redundant path repeatedly re-converging)",
			br.Name, br.GetRef().Node, count, stpBurstWindow.Round(time.Minute))
		f := newHealthFinding(CheckSTPTopologyBurst, SeverityWarning, detail, []string{br.GetRef().Node}, []string{ref})
		f.DocsLink = stpTopologyBurstDocsLink
		out = append(out, f)
	}

	tracker.prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
