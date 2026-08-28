// SPDX-License-Identifier: Apache-2.0

package fw

import (
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Snapshot is the firewall-relevant slice of inventory state the resolver
// operates over: the three ruleset scopes plus the cluster-scope security
// groups every scope's rules can reference. It is a plain data snapshot,
// not the live *inventory.Graph — build one with BuildSnapshot from an
// inventory.Snapshot's entities.
type Snapshot struct {
	// Cluster is the datacenter-wide ruleset. Real PVE always has exactly
	// one; nil models "the collector has not observed it yet" (e.g. an
	// in-progress first poll), not "the datacenter firewall is off" —
	// callers should treat a nil Cluster as data-not-ready. Use
	// Cluster.Enabled (false) to represent the actual, observed
	// datacenter-off state.
	Cluster *inventory.FwRuleset
	// Nodes is keyed by node name.
	Nodes map[string]*inventory.FwRuleset
	// Guests is keyed by the guest's own inventory.Ref (Kind ==
	// inventory.KindGuest) — the natural identity callers (the API layer)
	// address a guest's resolved view by, e.g. via docs/api.md's Ref
	// triplet convention. BuildSnapshot derives this key from each guest
	// ruleset's "guest/<kind>/<vmid>" ID by joining on the ruleset's Node.
	Guests map[inventory.Ref]*inventory.FwRuleset
	// VNets (T-3103) is keyed by the vnet's own inventory.Ref (Kind ==
	// inventory.KindSDNVnet, ID "<zone>/<vnet>" — the same convention
	// internal/sdn already uses), mirroring Guests. BuildSnapshot derives
	// this key from each vnet ruleset's "vnet/<zone>/<vnet>" ID
	// (internal/collect's pollFirewall). A vnet ruleset has no resolved
	// (cluster+group cascade) view of its own — unlike guest scope, this
	// package has no hardware-confirmed model of how a vnet's forward chain
	// composes with cluster rules, so it is deliberately not invented here
	// (see internal/api/firewall.go's handleFirewallRulesets scope=vnet
	// case, which serves the raw ruleset only, same as node scope).
	VNets map[inventory.Ref]*inventory.FwRuleset
}

// BuildSnapshot assembles a Snapshot from a flat entity list (typically
// inventory.Snapshot.All()). Entities that are not *inventory.FwRuleset
// are ignored; a guest ruleset whose ID does not match the collector's
// documented "guest/<kind>/<vmid>" convention (internal/collect's
// pollFirewall) is skipped rather than guessed at.
func BuildSnapshot(entities []inventory.Entity) Snapshot {
	snap := Snapshot{
		Nodes:  map[string]*inventory.FwRuleset{},
		Guests: map[inventory.Ref]*inventory.FwRuleset{},
		VNets:  map[inventory.Ref]*inventory.FwRuleset{},
	}
	for _, e := range entities {
		rs, ok := e.(*inventory.FwRuleset)
		if !ok {
			continue
		}
		cp := cloneRuleset(rs)
		switch rs.Scope {
		case inventory.FwScopeCluster:
			snap.Cluster = cp
		case inventory.FwScopeNode:
			snap.Nodes[rs.Node] = cp
		case inventory.FwScopeGuest:
			if ref, ok := guestRefFromRulesetID(rs.Ref); ok {
				snap.Guests[ref] = cp
			}
		case inventory.FwScopeVNet:
			if ref, ok := vnetRefFromRulesetID(rs.Ref); ok {
				snap.VNets[ref] = cp
			}
		}
	}
	return snap
}

// cloneRuleset returns a shallow-safe copy so a Snapshot never shares
// mutable slice backing with whatever produced the input entity list
// (mirrors inventory.Entity's own clone-on-ingest discipline).
func cloneRuleset(rs *inventory.FwRuleset) *inventory.FwRuleset {
	cp := *rs
	cp.Rules = append([]inventory.FwRule(nil), rs.Rules...)
	cp.Aliases = append([]inventory.FwAlias(nil), rs.Aliases...)
	cp.IPSets = append([]inventory.FwIPSet(nil), rs.IPSets...)
	cp.Groups = append([]inventory.FwGroup(nil), rs.Groups...)
	return &cp
}

// guestRefFromRulesetID recovers the guest's own inventory.Ref
// (Kind==KindGuest) from a guest-scope firewall ruleset's Ref, whose ID is
// "guest/<kind>/<vmid>" (internal/collect's pollFirewall) and whose Node
// matches the guest's own node.
func guestRefFromRulesetID(rsRef inventory.Ref) (inventory.Ref, bool) {
	parts := strings.SplitN(rsRef.ID, "/", 3)
	if len(parts) != 3 || parts[0] != "guest" {
		return inventory.Ref{}, false
	}
	return inventory.Ref{Kind: inventory.KindGuest, Node: rsRef.Node, ID: parts[2]}, true
}

// vnetRefFromRulesetID recovers the vnet's own inventory.Ref
// (Kind==KindSDNVnet, ID "<zone>/<vnet>", internal/sdn's existing
// convention) from a vnet-scope firewall ruleset's Ref, whose ID is
// "vnet/<zone>/<vnet>" (internal/collect's pollFirewall). A vnet ruleset is
// cluster-scoped like the SDN vnet it belongs to, so Node is always empty
// on both sides.
func vnetRefFromRulesetID(rsRef inventory.Ref) (inventory.Ref, bool) {
	parts := strings.SplitN(rsRef.ID, "/", 3)
	if len(parts) != 3 || parts[0] != "vnet" {
		return inventory.Ref{}, false
	}
	return inventory.Ref{Kind: inventory.KindSDNVnet, ID: parts[1] + "/" + parts[2]}, true
}

// sortedNodeNames returns snap.Nodes' keys sorted, for deterministic
// iteration.
func (s Snapshot) sortedNodeNames() []string {
	out := make([]string, 0, len(s.Nodes))
	for n := range s.Nodes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// sortedGuestRefs returns snap.Guests' keys sorted, for deterministic
// iteration.
func (s Snapshot) sortedGuestRefs() []inventory.Ref {
	out := make([]inventory.Ref, 0, len(s.Guests))
	for g := range s.Guests {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// sortedVNetRefs returns snap.VNets' keys sorted, for deterministic
// iteration.
func (s Snapshot) sortedVNetRefs() []inventory.Ref {
	out := make([]inventory.Ref, 0, len(s.VNets))
	for v := range s.VNets {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Group looks up a cluster-scope security group by name (security groups
// are cluster-only, see inventory.FwGroup's doc comment).
func (s Snapshot) Group(name string) (inventory.FwGroup, bool) {
	if s.Cluster == nil {
		return inventory.FwGroup{}, false
	}
	for _, g := range s.Cluster.Groups {
		if g.Name == name {
			return g, true
		}
	}
	return inventory.FwGroup{}, false
}
