// SPDX-License-Identifier: Apache-2.0

package failsim

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// SPOFEntry is one element of the network whose removal has a nonzero,
// *known* impact — the single-point-of-failure inventory backing the standing
// dashboard tile (GET /failsim/spof-score). Ref names the element; Impact is
// its computed blast radius (Simulate's output).
type SPOFEntry struct {
	Ref    inventory.Ref
	Impact Impact
}

// SPOFScore is the dashboard-tile payload: an overall resilience score plus
// the SPOF inventory that produced it. GeneratedAt is stamped by the caller
// (this package is pure and holds no clock) — the API adapter uses the
// snapshot's own GeneratedAt.
type SPOFScore struct {
	Entries []SPOFEntry
	Score   int
}

// Severity weights subtracted from a perfect 100 per SPOF. A critical SPOF
// (quorum/mgmt/Ceph risk) costs far more than one that merely strands guests.
const (
	weightCritical = 25
	weightWarning  = 8
	weightInfo     = 2
)

// Inventory enumerates every candidate element — nodes, bonds, bridges,
// uplink NICs, LLDP-discovered switches, and (where supplied) WireGuard
// tunnels — simulates each element's removal, and returns those with a
// nonzero known impact, sorted by ref. A purely-redundant element (its
// removal breaks nothing) is excluded, satisfying the "names every element
// whose removal has nonzero impact and excludes every purely-redundant
// element" contract.
func Inventory(in Input) []SPOFEntry {
	var entries []SPOFEntry
	for _, target := range candidates(in) {
		im := Simulate(in, target)
		if im.nonZero() {
			entries = append(entries, SPOFEntry{Ref: target, Impact: im})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Ref.String() < entries[j].Ref.String() })
	return entries
}

// Score rolls the SPOF inventory into an overall 0..100 resilience score:
// 100 minus each SPOF's severity weight, floored at 0. Fewer, lower-impact
// SPOFs score higher — the property T-1607's posture score consumes.
func Score(entries []SPOFEntry) int {
	score := 100
	for _, e := range entries {
		switch e.Impact.Severity {
		case SeverityCritical:
			score -= weightCritical
		case SeverityWarning:
			score -= weightWarning
		default:
			score -= weightInfo
		}
	}
	if score < 0 {
		score = 0
	}
	return score
}

// ScoreInventory is the convenience combiner: Inventory + Score in one call,
// returning the dashboard-tile shape (minus the caller-stamped timestamp).
func ScoreInventory(in Input) SPOFScore {
	entries := Inventory(in)
	return SPOFScore{Score: Score(entries), Entries: entries}
}

// candidates is the closed set of elements the SPOF inventory considers. It
// deliberately over-enumerates (every bond/bridge/NIC, not just ones already
// known to be uplinks) and lets Impact.nonZero filter — a bridge with no
// uplink, or a NIC with a redundant sibling, simply produces a zero impact
// and drops out.
func candidates(in Input) []inventory.Ref {
	seen := map[inventory.Ref]bool{}
	var out []inventory.Ref
	add := func(r inventory.Ref) {
		if r.IsZero() || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	for _, e := range in.Snapshot.All() {
		switch e.GetRef().Kind {
		case inventory.KindNode, inventory.KindBond, inventory.KindOVSBond,
			inventory.KindBridge, inventory.KindOVSBridge, inventory.KindPhysNic:
			add(e.GetRef())
		}
	}
	// LLDP-discovered switches (T-1205's switch model is not required for
	// this: a switch every uplink faces is a real SPOF the moment LLDP names
	// it). Encoded as a cluster-scoped KindSwitchPort ref keyed by chassis.
	for _, r := range switchTargets(in.Snapshot) {
		add(r)
	}
	// WireGuard tunnels, where the model is supplied.
	for _, t := range in.Tunnels {
		add(inventory.Ref{Kind: inventory.KindWgTunnel, Node: t.Node, ID: t.ID})
	}
	return out
}

// switchTargets returns one synthetic KindSwitchPort ref per distinct switch
// LLDP reveals (keyed by chassis id, falling back to chassis name). These are
// the "switch" SPOF candidates; removalClosure maps such a ref back to every
// NIC facing that chassis.
func switchTargets(snap inventory.Snapshot) []inventory.Ref {
	seen := map[string]bool{}
	var out []inventory.Ref
	for _, e := range snap.All() {
		n, ok := e.(*inventory.LldpNeighbor)
		if !ok {
			continue
		}
		id := n.ChassisID
		if id == "" {
			id = n.ChassisName
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, inventory.Ref{Kind: inventory.KindSwitchPort, ID: id})
	}
	return out
}
