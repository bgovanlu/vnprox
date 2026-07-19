package ceph

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// The three classic Ceph-networking footguns this task's card names
// (docs/features/monitoring.md §5's health-check vocabulary — internal/
// findings' health_ceph.go wraps each Footgun below into a standard
// findings.Finding with hysteresis, exactly mirroring every other
// health_*.go check's own producer-plus-wrapper split).
const (
	CheckCorosyncSharedLink = "ceph_corosync_shared_link"
	CheckClusterMTUMismatch = "ceph_cluster_mtu_mismatch"
	CheckSingleNIC          = "ceph_single_nic"
)

// Footgun is one detected condition — neutral with respect to severity/id
// scheme, which internal/findings' health_ceph.go decides (the same
// "neutral Divergence, caller decides presentation" split
// internal/xnode's Divergence already establishes for the cross-node
// comparison families).
type Footgun struct {
	Check  string
	Detail string
	Nodes  []string
	Refs   []string
}

// CorosyncSharedLink reports every OSD-hosting node whose corosync ring
// address(es) (cor, from corosync.conf — the same static-address substrate
// T-803's corosync_link_degraded check and cmd/vnproxd's flow classifier
// wiring already read) resolve to a carrier sharing at least one terminal
// physical NIC with that node's resolved Ceph cluster-network carrier. A
// nil cor (no corosync.conf readable — a single, not-yet-clustered node)
// or a node with an unresolved cluster-network carrier is silently skipped
// — never a guess.
func CorosyncSharedLink(snap inventory.Snapshot, overlay Overlay, cor *host.CorosyncConfig) []Footgun {
	if cor == nil {
		return nil
	}
	var out []Footgun
	for _, na := range overlay.Nodes {
		if len(na.ClusterNICs) == 0 {
			continue
		}
		cn, ok := cor.NodeByName(na.Node)
		if !ok {
			continue
		}
		var sharedRing string
		for _, ringAddr := range cn.RingAddrs {
			ringRef, ok := carrierForExactAddr(snap, na.Node, ringAddr)
			if !ok {
				continue
			}
			_, ringNICs := topology.ResolvePhysicalPath(snap, ringRef)
			// A ring carrier that IS a terminal PhysNic itself (no bond, no
			// intervening bridge/VLAN with its own further path — e.g. a
			// bare interface unlikely for corosync but handled uniformly)
			// contributes itself.
			if ringRef.Kind == inventory.KindPhysNic {
				ringNICs = append(ringNICs, ringRef)
			}
			if nicSetsOverlap(ringNICs, na.ClusterNICs) {
				sharedRing = ringAddr
				break
			}
		}
		if sharedRing == "" {
			continue
		}
		detail := fmt.Sprintf(
			"node %s: Ceph cluster network (%s) and corosync's ring address %s resolve to the same physical link (%s) — combined replication and quorum-heartbeat traffic risks saturating it",
			na.Node, overlay.ClusterNetwork, sharedRing, refListString(na.ClusterNICs))
		out = append(out, Footgun{
			Check:  CheckCorosyncSharedLink,
			Detail: detail,
			Nodes:  []string{na.Node},
			Refs:   refStrings(append(append([]inventory.Ref(nil), na.ClusterPath...), na.ClusterCarrier)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nodes[0] < out[j].Nodes[0] })
	return out
}

// ClusterMTUMismatch reports a single Footgun when OSD-hosting nodes'
// resolved Ceph cluster-network carrier MTU disagrees — mirroring
// internal/xnode.CrossNodeMTU's majority-vote shape (MajorityInt, exported
// by that package specifically so this check reuses its exact tie-breaking
// rule) rather than re-implementing a second one; unlike CrossNodeMTU this
// compares across a per-node carrier that is frequently a *different*-named
// interface across nodes (Ceph's cluster network need not share one bridge
// name cluster-wide the way CrossNodeMTU's same-named-bridge grouping
// assumes), so it cannot reuse CrossNodeMTU itself, only its majority-vote
// helper. Nodes with an unresolved carrier or unreported MTU are excluded
// from the comparison entirely — never guessed into either bucket. Silent
// (nil) when fewer than two distinct MTU values are observed.
func ClusterMTUMismatch(overlay Overlay) []Footgun {
	votes := map[int]int{}
	mtuByNode := map[string]int{}
	var present []string
	for _, na := range overlay.Nodes {
		if !na.ClusterMTUKnown {
			continue
		}
		mtuByNode[na.Node] = na.ClusterMTU
		votes[na.ClusterMTU]++
		present = append(present, na.Node)
	}
	if len(votes) < 2 {
		return nil
	}
	sort.Strings(present)
	canonical := xnode.MajorityInt(votes)

	var dissent []string
	var refs []string
	var nodes []string
	for _, n := range present {
		nodes = append(nodes, n)
		if mtuByNode[n] != canonical {
			dissent = append(dissent, fmt.Sprintf("%s=%d", n, mtuByNode[n]))
		}
	}
	for _, na := range overlay.Nodes {
		if !na.ClusterCarrier.IsZero() {
			refs = append(refs, na.ClusterCarrier.String())
		}
	}
	detail := fmt.Sprintf(
		"Ceph cluster network (%s) MTU has drifted across OSD-hosting nodes: %s (canonical %d) — replication traffic may fragment or be dropped on the outlier node(s)",
		overlay.ClusterNetwork, joinStrings(dissent), canonical)
	return []Footgun{{Check: CheckClusterMTUMismatch, Detail: detail, Nodes: nodes, Refs: sortedUniqueStrings(refs)}}
}

// SingleNIC reports every OSD-hosting node where Ceph public and cluster
// traffic resolve to the exact same single terminal PhysNic with no bond in
// either path — no redundancy, and a single NIC failure or saturation
// takes down both networks together.
func SingleNIC(overlay Overlay) []Footgun {
	var out []Footgun
	for _, na := range overlay.Nodes {
		if len(na.PublicNICs) != 1 || len(na.ClusterNICs) != 1 {
			continue
		}
		if na.PublicNICs[0] != na.ClusterNICs[0] {
			continue
		}
		if hasBond(na.PublicPath) || hasBond(na.ClusterPath) {
			continue
		}
		detail := fmt.Sprintf(
			"node %s: Ceph public (%s) and cluster (%s) networks both ride the single, unbonded NIC %s — no redundancy for either",
			na.Node, overlay.PublicNetwork, overlay.ClusterNetwork, na.PublicNICs[0].String())
		out = append(out, Footgun{
			Check:  CheckSingleNIC,
			Detail: detail,
			Nodes:  []string{na.Node},
			Refs:   []string{na.PublicNICs[0].String()},
		})
	}
	return out
}

func hasBond(path []inventory.Ref) bool {
	for _, r := range path {
		if r.Kind == inventory.KindBond {
			return true
		}
	}
	return false
}

func nicSetsOverlap(a, b []inventory.Ref) bool {
	set := make(map[inventory.Ref]bool, len(a))
	for _, r := range a {
		set[r] = true
	}
	for _, r := range b {
		if set[r] {
			return true
		}
	}
	return false
}

func refStrings(refs []inventory.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if !r.IsZero() {
			out = append(out, r.String())
		}
	}
	return sortedUniqueStrings(out)
}

func refListString(refs []inventory.Ref) string {
	return joinStrings(refStrings(refs))
}

func sortedUniqueStrings(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
