// SPDX-License-Identifier: Apache-2.0

// health_ceph.go implements docs/features/monitoring.md §5's three T-1503
// Ceph-networking health checks (extending T-803's pack, same source
// "health" and continuously-computed, hysteresis-gated convention every
// other check in this file group follows): ceph_corosync_shared_link,
// ceph_cluster_mtu_mismatch, ceph_single_nic. The actual detection logic
// lives in internal/ceph (CorosyncSharedLink/ClusterMTUMismatch/SingleNIC,
// pure functions over a live inventory snapshot plus internal/ceph.Overlay)
// — this file only re-derives each check's candidate key set from the same
// Overlay so every candidate (not just currently-breaching ones) gets an
// Evaluate call every cycle, the same "enumerate candidates, debounce each"
// shape checkBondSlaveDown/checkLACPPartnerMismatch already establish
// (mirroring a footgun-list-only producer like this one directly into
// hysteresis would never decay: Evaluate must see an explicit breach=false
// for a key that stops firing while its entity still exists, not just be
// omitted).

package findings

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

const cephDocsLink = "docs/features/monitoring.md#5-health-checks"

// cephRiseCycles/cephFallCycles: the same 2-cycle each-way hysteresis
// window corosync_link_degraded/vxlan_underlay_mtu use for a comparable
// live-topology-derived fact.
const (
	cephRiseCycles = 2
	cephFallCycles = 2
)

// CephProvider is the seam checkCephFootguns needs: T-1503's discovered
// Ceph public/cluster network overlay, plus corosync's configured ring
// addresses (host.CorosyncConfig, from corosync.conf — nil when
// unreadable/not clustered, in which case ceph_corosync_shared_link simply
// has nothing to evaluate this cycle, the same graceful degradation every
// other optional producer input in this package follows). cmd/vnproxd's
// adapter builds both from the same one-time-at-startup ceph.Discover +
// host.ReadCorosyncConf reads setupFlowClassifier/serviceclassify.go
// already perform for T-1504's classifier — see that file's doc comment.
type CephProvider interface {
	CephOverlay() (overlay ceph.Overlay, cor *host.CorosyncConfig, err error)
}

// checkCephFootguns evaluates prov's current overlay against db (Engine's
// per-check debouncer, shared across all three sub-checks below — each
// already namespaces its own keys by check name, so there is no collision
// risk in sharing one debouncer the way serviceTrafficDB's single-debouncer
// convention already establishes for a comparable multi-key check family).
// A nil provider or a computation error yields no findings — detection-only,
// same "quietly absent" degradation every other optional producer input
// follows.
func checkCephFootguns(prov CephProvider, snap inventory.Snapshot, db *debouncer) []Finding {
	if prov == nil {
		return nil
	}
	overlay, cor, err := prov.CephOverlay()
	if err != nil {
		return nil
	}

	corosyncFindings := ceph.CorosyncSharedLink(snap, overlay, cor)
	mtuFindings := ceph.ClusterMTUMismatch(overlay)
	singleNICFindings := ceph.SingleNIC(overlay)

	firingCorosyncNodes := footgunNodeSet(corosyncFindings)
	firingSingleNICNodes := footgunNodeSet(singleNICFindings)

	var out []Finding
	live := map[string]bool{}

	// ceph_corosync_shared_link: one candidate key per node with a
	// resolved cluster-network carrier — only evaluable at all when a
	// corosync.conf was actually read (cor != nil): with no ring addresses
	// known, there is nothing to compare against, so no candidate exists
	// and no state is tracked (never a false "cleared" transition either).
	if cor != nil {
		for _, na := range overlay.Nodes {
			if len(na.ClusterNICs) == 0 {
				continue
			}
			key := ceph.CheckCorosyncSharedLink + "|" + na.Node
			live[key] = true
			active := db.Evaluate(key, firingCorosyncNodes[na.Node], cephRiseCycles, cephFallCycles)
			if !active {
				continue
			}
			if fg, ok := footgunForNode(corosyncFindings, na.Node); ok {
				out = append(out, cephHealthFinding(fg))
			}
		}
	}

	// ceph_cluster_mtu_mismatch: a single cluster-wide candidate key, live
	// whenever at least 2 OSD-hosting nodes report a known cluster-network
	// carrier MTU (the comparison's own minimum input, mirroring
	// ceph.ClusterMTUMismatch's own "fewer than two distinct values" no-op
	// guard one level up).
	if mtuCandidateCount(overlay) >= 2 {
		const key = ceph.CheckClusterMTUMismatch
		live[key] = true
		active := db.Evaluate(key, len(mtuFindings) > 0, cephRiseCycles, cephFallCycles)
		if active && len(mtuFindings) > 0 {
			out = append(out, cephHealthFinding(mtuFindings[0]))
		}
	}

	// ceph_single_nic: one candidate key per OSD-hosting node whose
	// public/cluster carriers both resolve to exactly one terminal NIC
	// each (the only shape ceph.SingleNIC can ever flag) — a node still
	// mid-bonding-transition with an unresolved carrier isn't a candidate
	// at all.
	for _, na := range overlay.Nodes {
		if len(na.PublicNICs) != 1 || len(na.ClusterNICs) != 1 {
			continue
		}
		key := ceph.CheckSingleNIC + "|" + na.Node
		live[key] = true
		active := db.Evaluate(key, firingSingleNICNodes[na.Node], cephRiseCycles, cephFallCycles)
		if !active {
			continue
		}
		if fg, ok := footgunForNode(singleNICFindings, na.Node); ok {
			out = append(out, cephHealthFinding(fg))
		}
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cephHealthFinding(fg ceph.Footgun) Finding {
	f := newHealthFinding(fg.Check, SeverityWarning, fg.Detail, fg.Nodes, fg.Refs)
	f.DocsLink = cephDocsLink
	return f
}

func footgunNodeSet(fgs []ceph.Footgun) map[string]bool {
	out := map[string]bool{}
	for _, fg := range fgs {
		for _, n := range fg.Nodes {
			out[n] = true
		}
	}
	return out
}

func footgunForNode(fgs []ceph.Footgun, node string) (ceph.Footgun, bool) {
	for _, fg := range fgs {
		for _, n := range fg.Nodes {
			if n == node {
				return fg, true
			}
		}
	}
	return ceph.Footgun{}, false
}

func mtuCandidateCount(overlay ceph.Overlay) int {
	n := 0
	for _, na := range overlay.Nodes {
		if na.ClusterMTUKnown {
			n++
		}
	}
	return n
}
