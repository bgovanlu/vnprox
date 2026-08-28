// SPDX-License-Identifier: Apache-2.0

package fw

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ObjectKind names the kind of a firewall object usage tracks.
type ObjectKind string

const (
	ObjectAlias ObjectKind = "alias"
	ObjectIPSet ObjectKind = "ipset"
	ObjectGroup ObjectKind = "group"
)

// RuleRef locates one rule that references a tracked object, for the
// editor's "view" deep-link (docs/features/firewall.md §2: "this alias is
// referenced by 9 rules — view").
type RuleRef struct {
	Scope      inventory.FwScope
	RulesetRef inventory.Ref
	Pos        int
}

// ObjectUsage is one alias/ipset/security-group's reference-count
// summary.
type ObjectUsage struct {
	Kind         ObjectKind
	Scope        inventory.FwScope
	Name         string
	Comment      string
	ReferencedBy []RuleRef
	Count        int
}

// rulesetRules is an internal scanning unit: one ruleset's (or one
// security group's) own rule list, tagged with where it lives.
type rulesetRules struct {
	Scope inventory.FwScope
	Ref   inventory.Ref
	Rules []inventory.FwRule
}

// UsageCounts computes "referenced by N rules" for every alias, ipset,
// and security group visible in snap (docs/features/firewall.md §2).
//
// Visibility mirrors real pve-firewall alias/ipset scoping: a cluster-
// scope alias/ipset is visible from — and so may be referenced by — any
// rule anywhere (cluster rules, every node's rules, every guest's rules,
// and every security group's own rules); a node- or guest-scope alias/
// ipset is only visible within the ruleset that defines it. Security
// groups are cluster-scope-only and referenced from any rule anywhere via
// a "type: group" rule (see ResolvedRule/appendRule).
//
// The returned slice is sorted by (Kind, Scope, Name) for determinism —
// map iteration order (Snapshot.Nodes/Guests) is otherwise unstable.
func UsageCounts(snap Snapshot) []ObjectUsage {
	everywhere := allRulesetRules(snap)

	var out []ObjectUsage
	if snap.Cluster != nil {
		for _, a := range snap.Cluster.Aliases {
			out = append(out, buildUsage(ObjectAlias, inventory.FwScopeCluster, a.Name, a.Comment, everywhere))
		}
		for _, s := range snap.Cluster.IPSets {
			out = append(out, buildUsage(ObjectIPSet, inventory.FwScopeCluster, s.Name, s.Comment, everywhere))
		}
		for _, g := range snap.Cluster.Groups {
			out = append(out, buildUsage(ObjectGroup, inventory.FwScopeCluster, g.Name, g.Comment, everywhere))
		}
	}
	for _, n := range snap.sortedNodeNames() {
		rs := snap.Nodes[n]
		local := []rulesetRules{{Scope: inventory.FwScopeNode, Ref: rs.Ref, Rules: rs.Rules}}
		for _, a := range rs.Aliases {
			out = append(out, buildUsage(ObjectAlias, inventory.FwScopeNode, a.Name, a.Comment, local))
		}
		for _, s := range rs.IPSets {
			out = append(out, buildUsage(ObjectIPSet, inventory.FwScopeNode, s.Name, s.Comment, local))
		}
	}
	for _, g := range snap.sortedGuestRefs() {
		rs := snap.Guests[g]
		local := []rulesetRules{{Scope: inventory.FwScopeGuest, Ref: rs.Ref, Rules: rs.Rules}}
		for _, a := range rs.Aliases {
			out = append(out, buildUsage(ObjectAlias, inventory.FwScopeGuest, a.Name, a.Comment, local))
		}
		for _, s := range rs.IPSets {
			out = append(out, buildUsage(ObjectIPSet, inventory.FwScopeGuest, s.Name, s.Comment, local))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// allRulesetRules returns every rule list in snap that a cluster-scope
// alias/ipset/group could be referenced from: the cluster ruleset itself,
// every security group's own rules, every node ruleset, and every guest
// ruleset — in deterministic order.
func allRulesetRules(snap Snapshot) []rulesetRules {
	var out []rulesetRules
	if snap.Cluster != nil {
		out = append(out, rulesetRules{Scope: inventory.FwScopeCluster, Ref: snap.Cluster.Ref, Rules: snap.Cluster.Rules})
		for _, g := range snap.Cluster.Groups {
			out = append(out, rulesetRules{
				Scope: inventory.FwScopeCluster,
				Ref:   inventory.Ref{Kind: inventory.KindFwRuleset, ID: "group/" + g.Name},
				Rules: g.Rules,
			})
		}
	}
	for _, n := range snap.sortedNodeNames() {
		rs := snap.Nodes[n]
		out = append(out, rulesetRules{Scope: inventory.FwScopeNode, Ref: rs.Ref, Rules: rs.Rules})
	}
	for _, g := range snap.sortedGuestRefs() {
		rs := snap.Guests[g]
		out = append(out, rulesetRules{Scope: inventory.FwScopeGuest, Ref: rs.Ref, Rules: rs.Rules})
	}
	// vnet-scope rulesets (T-3103) have no aliases/ipsets of their own
	// (real PVE exposes no such endpoint under this prefix — see
	// inventory.FwScopeVNet's doc comment), so unlike the node/guest loops
	// above there is no "enumerate rs.Aliases/rs.IPSets" counterpart here —
	// but a vnet rule can still *reference* a cluster-scope alias/ipset/
	// group, so it must still be scanned for that, the same reason group
	// member rules are folded in above.
	for _, v := range snap.sortedVNetRefs() {
		rs := snap.VNets[v]
		out = append(out, rulesetRules{Scope: inventory.FwScopeVNet, Ref: rs.Ref, Rules: rs.Rules})
	}
	return out
}

func buildUsage(kind ObjectKind, scope inventory.FwScope, name, comment string, sources []rulesetRules) ObjectUsage {
	u := ObjectUsage{Kind: kind, Scope: scope, Name: name, Comment: comment}
	for _, src := range sources {
		for _, r := range src.Rules {
			if !ruleReferences(r, kind, name) {
				continue
			}
			u.Count++
			u.ReferencedBy = append(u.ReferencedBy, RuleRef{Scope: src.Scope, RulesetRef: src.Ref, Pos: r.Pos})
		}
	}
	return u
}

// ruleReferences reports whether rule r references the named object.
// Aliases are referenced by bare name in a rule's source/dest field;
// IPSets by the same fields prefixed with "+" (real pve-firewall syntax);
// security groups by a "type: group" rule whose action names the group.
func ruleReferences(r inventory.FwRule, kind ObjectKind, name string) bool {
	switch kind {
	case ObjectAlias:
		return r.Source == name || r.Dest == name
	case ObjectIPSet:
		want := "+" + name
		return r.Source == want || r.Dest == want
	case ObjectGroup:
		return r.Direction == "group" && r.Action == name
	default:
		return false
	}
}
