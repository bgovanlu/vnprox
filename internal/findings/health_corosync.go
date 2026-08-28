// SPDX-License-Identifier: Apache-2.0

// health_corosync.go implements docs/features/monitoring.md §5's
// "corosync_link_degraded" health check (T-803): a corosync ring reporting
// faulty/no-link status right now, distinct from a ring's static configured
// *address* (internal/host.ParseCorosyncConf, which every safety-interlock
// check already reads) — a ring can be correctly configured in
// corosync.conf yet reporting FAULTY at runtime (a bad cable, a switch-port
// misconfiguration, a knet transport hiccup). Detection only: recovering a
// faulty ring is a physical/administrative action outside any changeset op
// vnprox can safely compute, the same "no fix" stance bond_slave_down/
// bridge_no_carrier already take for comparable link-health facts.
//
// Substrate: internal/host.Reader.CorosyncStatus (new in this task, backed
// by `corosync-cfgtool -s`) has no natural slot in inventory.Snapshot (a
// corosync ring is not a map entity), so — like health_service.go's
// systemd-unit-status check — this is fed by an explicit
// CorosyncProvider seam rather than reading the graph directly.

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
)

const CheckCorosyncLinkDegraded = "corosync_link_degraded"

const corosyncLinkDegradedDocsLink = "docs/features/monitoring.md#5-health-checks"

// corosyncRiseCycles/corosyncFallCycles: a ring's faulty status must be
// observed this many consecutive Engine cycles before the finding
// fires/clears — light hysteresis against a single transient read glitch,
// the same window bond_slave_down/bridge_no_carrier already use for a
// comparable link-health fact.
const (
	corosyncRiseCycles = 2
	corosyncFallCycles = 2
)

// CorosyncProvider is the seam checkCorosyncLinkDegraded needs: every known
// cluster node's current corosync ring status, keyed by node name. Returning
// the whole cluster's status in one call (rather than one method per node)
// leaves *how* a concrete implementation fans out across nodes (local-only
// today; cluster-wide fan-out via the peer API is a documented follow-up —
// see this task's completion report) entirely to the adapter, the same
// "caller decides the fan-out" shape MgmtProvider already establishes.
type CorosyncProvider interface {
	CorosyncStatus() (map[string][]host.RingStatus, error)
}

// checkCorosyncLinkDegraded evaluates every node's current ring status
// against db (Engine's per-check debouncer) and returns one finding per
// (node, ring) that has been consistently reported faulty. A nil provider
// (not wired) or a computation error yields no findings — detection-only,
// the same "quietly absent" degradation every other optional producer input
// follows.
func checkCorosyncLinkDegraded(prov CorosyncProvider, db *debouncer) []Finding {
	if prov == nil {
		return nil
	}
	status, err := prov.CorosyncStatus()
	if err != nil {
		return nil
	}

	var out []Finding
	live := map[string]bool{}

	nodes := make([]string, 0, len(status))
	for n := range status {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	for _, node := range nodes {
		rings := append([]host.RingStatus(nil), status[node]...)
		sort.Slice(rings, func(i, j int) bool { return rings[i].RingID < rings[j].RingID })

		for _, ring := range rings {
			key := fmt.Sprintf("%s|ring%d", node, ring.RingID)
			live[key] = true
			active := db.Evaluate(key, ring.Faulty, corosyncRiseCycles, corosyncFallCycles)
			if !active {
				continue
			}
			detail := fmt.Sprintf("node %s's corosync ring %d (%s) is reporting faulty: %s",
				node, ring.RingID, ring.Addr, ring.StatusText)
			// Built directly rather than via newHealthFinding: a corosync
			// ring has no inventory.Ref (it isn't a map entity), so there's
			// nothing to put in Refs, and the (node, ring) pair — not just
			// node — must be part of the id or two degraded rings on the
			// same node would collide onto one id (mirrors
			// checkServiceDown's identical (node, service) reasoning).
			f := Finding{
				ID:       "health:" + CheckCorosyncLinkDegraded + "|" + key,
				Source:   SourceHealth,
				Check:    CheckCorosyncLinkDegraded,
				Severity: SeverityWarning,
				Detail:   detail,
				Nodes:    []string{node},
				DocsLink: corosyncLinkDegradedDocsLink,
			}
			out = append(out, f)
		}
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
