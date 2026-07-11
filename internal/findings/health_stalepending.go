// health_stalepending.go implements docs/features/monitoring.md §5's
// "interfaces.new pending >1h" check.
//
// This is deliberately a *different* check from internal/drift's
// CheckPendingInterfaces (drift.go's pending.go): that check flags any
// staged-but-unapplied edit immediately, every cycle it's still pending —
// useful as a standing "you have an uncommitted staged edit" reminder, but
// too noisy for an alerting-oriented health check (an operator routinely
// stages, reviews, then applies within minutes; flagging that as an
// "incident" the instant it appears would be spam). This check instead
// tracks *how long* a ref has continuously reported pending (first-seen
// timestamp, held on Engine across cycles) and only fires once that streak
// exceeds one hour — docs/features/monitoring.md §5's literal threshold —
// clearing the moment the ref stops reporting pending (reload ran, or the
// staged edit was discarded).

package findings

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckStalePendingInterfaces = "stale_pending_interfaces"

const stalePendingDocsLink = "docs/features/monitoring.md#5-health-checks"

// staleInterfacesThreshold is docs/features/monitoring.md §5's literal
// "pending >1h" threshold.
const staleInterfacesThreshold = time.Hour

// pendingTracker remembers, per ref, when it was first observed pending
// (Pending != "") without interruption. Safe for concurrent use.
type pendingTracker struct {
	firstSeen map[string]time.Time
	mu        sync.Mutex
}

func newPendingTracker() *pendingTracker {
	return &pendingTracker{firstSeen: map[string]time.Time{}}
}

// observe records ref as currently pending at now (seeding firstSeen on the
// ref's first pending observation, leaving it untouched on every subsequent
// one) and returns how long it has been continuously pending.
func (t *pendingTracker) observe(ref string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	first, ok := t.firstSeen[ref]
	if !ok {
		t.firstSeen[ref] = now
		return 0
	}
	return now.Sub(first)
}

// clear drops ref's tracked first-seen time (it is no longer pending).
func (t *pendingTracker) clear(ref string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.firstSeen, ref)
}

// prune drops tracked state for any ref not in liveePending (an entity that
// vanished from inventory entirely while still mid-tracked, e.g. a NIC
// removed from the node).
func (t *pendingTracker) prune(livePending map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.firstSeen {
		if !livePending[k] {
			delete(t.firstSeen, k)
		}
	}
}

// checkStalePendingInterfaces is the CheckStalePendingInterfaces family.
func checkStalePendingInterfaces(snap inventory.Snapshot, tracker *pendingTracker, now time.Time) []Finding {
	var out []Finding
	live := map[string]bool{}

	for _, e := range snap.All() {
		pending, ref, ok := pendingEntityOf(e)
		if !ok {
			continue
		}
		key := ref.String()
		if pending == "" {
			tracker.clear(key)
			continue
		}
		live[key] = true
		age := tracker.observe(key, now)
		if age < staleInterfacesThreshold {
			continue
		}
		detail := fmt.Sprintf("interface %s on node %s has had an unapplied staged change (pending=%s) for over %s — the staged interfaces.new edit was never applied via reload",
			ref.ID, ref.Node, pending, age.Round(time.Minute))
		f := newHealthFinding(CheckStalePendingInterfaces, SeverityWarning, detail, []string{ref.Node}, []string{key})
		f.DocsLink = stalePendingDocsLink
		out = append(out, f)
	}

	tracker.prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// pendingEntityOf mirrors internal/drift/pending.go's pendingOf (duplicated
// rather than imported: it's four lines of a private type switch, and
// importing internal/drift here purely for this helper would be a stranger
// dependency than just repeating it).
func pendingEntityOf(e inventory.Entity) (pending string, ref inventory.Ref, ok bool) {
	switch v := e.(type) {
	case *inventory.PhysNic:
		return v.Pending, v.GetRef(), true
	case *inventory.Bond:
		return v.Pending, v.GetRef(), true
	case *inventory.Bridge:
		return v.Pending, v.GetRef(), true
	case *inventory.VlanIface:
		return v.Pending, v.GetRef(), true
	default:
		return "", inventory.Ref{}, false
	}
}
