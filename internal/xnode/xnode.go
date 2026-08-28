// SPDX-License-Identifier: Apache-2.0

// Package xnode holds the pure cross-node comparison families that both
// internal/drift (run against live inventory state) and internal/change
// (run against a changeset's *projected* state, as validator class 4 —
// docs/features/change-management.md §2) share. It exists to resolve a hard
// import constraint: internal/drift already imports internal/change (for the
// change.Op fix-patch types it attaches to fixable findings), so
// internal/change cannot import internal/drift back to reuse those
// comparisons. Factoring the three comparison families down into this
// lower-level package — which imports only internal/inventory — lets both
// callers reach one implementation without a cycle (the "same problem under
// two names" failure T-801 exists to prevent).
//
// The three families mirror docs/features/topology.md §6's first three drift
// check families exactly:
//
//   - BridgeDivergences: same-named bridge presence, VLAN-awareness, and
//     VID-set divergence across nodes (bridge.go).
//   - CrossNodeMTU: the same-named bridge's MTU differing across cluster
//     nodes — the cross-node half of drift's CheckMTUConsistency (mtu.go).
//   - SDNRealizationGaps: an SDN zone's node-membership vs. actual bridge
//     realization (sdn.go).
//
// Each returns a []Divergence — a neutral description of the divergence (its
// human detail, affected nodes/refs, and, where a safe correction is
// computable, one BridgeFix per dissenting node). Neither the finding wire
// type (drift.Finding vs. change.Finding), the severity, nor the concrete
// change.Op fix patch is decided here: those are each caller's own concern
// (drift keeps its warning/warning/error severities; change raises all three
// to blocking errors), built from the same Divergence data so the comparison
// itself is genuinely shared, not merely equivalent-looking.
package xnode

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Source is the read surface the comparison families need: every resolved
// entity, and a point lookup by Ref. inventory.Snapshot satisfies it
// directly (drift's live view); internal/change supplies its own projected
// implementation folding a changeset's ops over a base snapshot.
type Source interface {
	All() []inventory.Entity
	Get(inventory.Ref) (inventory.Entity, bool)
}

// Family identifiers — deliberately the same wire strings as drift's
// Check* constants (docs/features/topology.md §6), so a drift adapter can
// use a Divergence.Family as its Finding.Check verbatim.
const (
	FamilyBridgeDivergence = "bridge_divergence"
	FamilyMTUConsistency   = "mtu_consistency"
	FamilySDNRealization   = "sdn_realization"
)

// Divergence is one cross-node comparison result, neutral with respect to
// how a caller renders it. Refs/Nodes are already sorted+deduped so both
// callers derive stable ids from them.
type Divergence struct {
	// Family is one of the Family* constants above.
	Family string
	// Subject is the bridge name the divergence is about (for the SDN
	// realization family, the zone's realizing bridge name). internal/change
	// uses it to scope findings to the same-named bridge groups a changeset
	// actually touches; drift ignores it (it reports every divergence).
	Subject string
	// Detail is the human-readable description (identical wording to what
	// drift historically produced, so drift's substring assertions hold).
	Detail string
	// FixTitle is a neutral one-line summary of the correction (no "drift:"
	// prefix — a caller adds its own). Empty when Fixes is empty.
	FixTitle string
	Nodes    []string
	Refs     []string
	// Fixes is one BridgeFix per dissenting node, in the deterministic node
	// order the caller should apply them. Empty/nil ⇒ detection-only (no
	// computable fix — presence and SDN-realization divergences).
	Fixes []BridgeFix
}

// BridgeFix is a single dissenting node's correction: a bridge.update on
// Target aligning exactly one field to the cluster's majority value. Exactly
// one of VlanAware / MTU / Vids is non-nil per fix. Kept in inventory/
// primitive terms (not change.Op) so this package stays change-free; each
// caller turns it into its own change.Op via the one shared builder in
// internal/change (change.CrossNodeFixOps), so op construction is shared too.
type BridgeFix struct {
	VlanAware *bool
	MTU       *int
	Vids      *[]inventory.VidRange
	Target    inventory.Ref
}

// BridgesByName groups every Bridge/OVSBridge entity in src by its Name,
// mapping to a further map keyed by node — the shape every cross-node bridge
// comparison (bridge.go, mtu.go) starts from.
func BridgesByName(src Source) map[string]map[string]*inventory.Bridge {
	out := map[string]map[string]*inventory.Bridge{}
	for _, e := range src.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok {
			continue
		}
		byNode := out[br.Name]
		if byNode == nil {
			byNode = map[string]*inventory.Bridge{}
			out[br.Name] = byNode
		}
		byNode[br.GetRef().Node] = br
	}
	return out
}

// SortedBridgeNames returns the names of m in stable order, for deterministic
// iteration.
func SortedBridgeNames(m map[string]map[string]*inventory.Bridge) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ClusterNodes returns every cluster member's node name known to src (every
// inventory.Node entity — docs/data-model.md §1), sorted. Used by the
// presence and SDN-realization comparisons to reason about "which nodes
// should have this bridge" beyond just the nodes that already report it.
func ClusterNodes(src Source) []string {
	var out []string
	for _, e := range src.All() {
		if n, ok := e.(*inventory.Node); ok {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}

// PortMTU resolves a bridge port Ref (PhysNic, Bond, or VlanIface) to its
// runtime MTU, falling back to declared MTU when runtime is unreported
// (zero). ok is false if ref does not resolve or has no MTU information.
// Used by drift's within-node path check (which stays in internal/drift);
// exported here so that helper is shared rather than re-copied.
func PortMTU(src Source, ref inventory.Ref) (mtu int, ok bool) {
	e, found := src.Get(ref)
	if !found {
		return 0, false
	}
	switch v := e.(type) {
	case *inventory.PhysNic:
		return EffectiveMTU(v.MTU, v.MTUDeclared)
	case *inventory.Bond:
		return EffectiveMTU(v.MTU, v.MTUDeclared)
	case *inventory.VlanIface:
		return EffectiveMTU(v.MTU, v.MTUDeclared)
	default:
		return 0, false
	}
}

// EffectiveMTU picks the runtime MTU when set, else the declared one; ok is
// false when neither is reported.
func EffectiveMTU(runtime, declared int) (int, bool) {
	if runtime != 0 {
		return runtime, true
	}
	if declared != 0 {
		return declared, true
	}
	return 0, false
}

// sortedUnique returns a sorted copy of ss with duplicates and empty strings
// removed — the same canonicalization drift's newFinding applies, done here
// so a Divergence's Nodes/Refs are already stable for both callers.
func sortedUnique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
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
