// SPDX-License-Identifier: Apache-2.0

package fw_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestUsageCounts_ClusterObjectVisibleEverywhere covers acceptance
// criterion 4: alias/ipset/group usage counts must be correct across
// scopes. A cluster-scope alias, ipset, and security group are each
// referenced from a mix of cluster rules, a group's own rules, a node's
// rules, and a guest's rules — every one of those references must count.
func TestUsageCounts_ClusterObjectVisibleEverywhere(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "office_net", CIDR: "192.168.1.0/24"}},
		IPSets:  []inventory.FwIPSet{{Name: "blocklist", Entries: []inventory.FwIPSetEntry{{CIDR: "1.2.3.4/32"}}}},
		Groups:  []inventory.FwGroup{{Name: "webservers"}},
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Source: "office_net", Comment: "cluster ref to alias"},
			{Pos: 1, Enabled: true, Direction: "in", Action: "DROP", Source: "+blocklist", Comment: "cluster ref to ipset"},
			{Pos: 2, Enabled: true, Direction: "group", Action: "webservers"},
		},
	}
	nodeRS := &inventory.FwRuleset{
		Ref: nodeRulesetRef("pve1"), Scope: inventory.FwScopeNode, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Dest: "office_net", Comment: "node ref to alias"},
		},
	}
	guestRS := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve1", "qemu", "100"), Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Source: "+blocklist", Comment: "guest ref to ipset"},
		},
	}
	snap := buildSnapshot(t, cluster, nodeRS, guestRS)

	usage := fw.UsageCounts(snap)

	byName := map[string]fw.ObjectUsage{}
	for _, u := range usage {
		byName[string(u.Kind)+"/"+u.Name] = u
	}

	if got := byName["alias/office_net"]; got.Count != 2 {
		t.Errorf("alias office_net count = %d, want 2 (cluster rule + node rule)", got.Count)
	}
	if got := byName["ipset/blocklist"]; got.Count != 2 {
		t.Errorf("ipset blocklist count = %d, want 2 (cluster rule + guest rule)", got.Count)
	}
	if got := byName["group/webservers"]; got.Count != 1 {
		t.Errorf("group webservers count = %d, want 1", got.Count)
	}
}

// TestUsageCounts_VNetRuleCountsAgainstClusterObject is T-3103's guard
// against exactly the failure mode its task card warns about: a vnet-scope
// rule referencing a cluster-scope alias must count toward that alias's
// usage — a switch site (allRulesetRules) that forgot to add a vnet arm
// would silently under-count here, and checkFwObjectDeletable (validate_
// referential.go) would then let the alias be deleted while a live vnet
// rule still referenced it.
func TestUsageCounts_VNetRuleCountsAgainstClusterObject(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "office_net", CIDR: "192.168.1.0/24"}},
	}
	vnetRS := &inventory.FwRuleset{
		Ref: vnetRulesetRef("zone1", "vnet1"), Scope: inventory.FwScopeVNet, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "forward", Action: "ACCEPT", Source: "office_net", Comment: "vnet ref to alias"},
		},
	}
	snap := buildSnapshot(t, cluster, vnetRS)

	usage := fw.UsageCounts(snap)
	for _, u := range usage {
		if u.Kind == fw.ObjectAlias && u.Name == "office_net" {
			if u.Count != 1 {
				t.Fatalf("alias office_net count = %d, want 1 (the vnet rule's reference)", u.Count)
			}
			if len(u.ReferencedBy) != 1 || u.ReferencedBy[0].Scope != inventory.FwScopeVNet {
				t.Fatalf("ReferencedBy = %+v, want one entry scoped %q", u.ReferencedBy, inventory.FwScopeVNet)
			}
			return
		}
	}
	t.Fatal("alias/office_net not present in UsageCounts output")
}

// TestUsageCounts_LocalScopeObjectOnlyCountsLocalRules covers the other
// half of acceptance criterion 4's scoping model: a guest-defined alias is
// only visible (and so only referenceable) within that same guest's own
// rules — a same-named cluster or sibling-guest reference must not count.
func TestUsageCounts_LocalScopeObjectOnlyCountsLocalRules(t *testing.T) {
	cluster := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true}
	guestRS := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve1", "qemu", "100"), Scope: inventory.FwScopeGuest, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "local_only", CIDR: "10.0.0.5/32"}},
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Source: "local_only"},
		},
	}
	otherGuestRS := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve1", "qemu", "101"), Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			// Same bare name, but this guest never defined "local_only" as
			// its own alias, so it does not resolve to guest 100's object
			// and must not be counted against it.
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Source: "local_only"},
		},
	}
	snap := buildSnapshot(t, cluster, guestRS, otherGuestRS)

	usage := fw.UsageCounts(snap)
	var found fw.ObjectUsage
	for _, u := range usage {
		if u.Name == "local_only" {
			found = u
		}
	}
	if found.Count != 1 {
		t.Errorf("local_only count = %d, want 1 (only its own guest's rule)", found.Count)
	}
	if found.Scope != inventory.FwScopeGuest {
		t.Errorf("local_only Scope = %q, want guest", found.Scope)
	}
}

func TestUsageCounts_DeterministicOrder(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "zzz"}, {Name: "aaa"}},
	}
	snap := buildSnapshot(t, cluster)
	for i := 0; i < 5; i++ {
		usage := fw.UsageCounts(snap)
		if usage[0].Name != "aaa" || usage[1].Name != "zzz" {
			t.Fatalf("run %d: usage order = %+v, want sorted by name", i, usage)
		}
	}
}

func TestMacroExpansion_KnownAndUnknown(t *testing.T) {
	m, ok := fw.MacroExpansion("HTTP")
	if !ok {
		t.Fatal("MacroExpansion(HTTP): want ok=true")
	}
	if len(m.Ports) != 1 || m.Ports[0].Proto != "tcp" || m.Ports[0].Dport != "80" {
		t.Errorf("MacroExpansion(HTTP) = %+v, want tcp/80", m)
	}

	if _, ok := fw.MacroExpansion("NotARealMacro"); ok {
		t.Error("MacroExpansion(NotARealMacro): want ok=false")
	}
}

func TestKnownMacros_SortedAndNonEmpty(t *testing.T) {
	macros := fw.KnownMacros()
	if len(macros) == 0 {
		t.Fatal("KnownMacros returned nothing")
	}
	for i := 1; i < len(macros); i++ {
		if macros[i-1].Name >= macros[i].Name {
			t.Fatalf("KnownMacros not sorted at index %d: %q >= %q", i, macros[i-1].Name, macros[i].Name)
		}
	}
}
