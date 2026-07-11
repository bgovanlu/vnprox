package fw_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestBuildSnapshot_AssemblesAllThreeScopes(t *testing.T) {
	cluster := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster}
	node := &inventory.FwRuleset{Ref: nodeRulesetRef("pve1"), Scope: inventory.FwScopeNode}
	guest := &inventory.FwRuleset{Ref: guestRulesetRef("pve1", "qemu", "100"), Scope: inventory.FwScopeGuest}
	// A non-firewall entity must be ignored, not misfiled.
	other := &inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}, VMID: 100}

	snap := fw.BuildSnapshot([]inventory.Entity{cluster, node, guest, other})

	if snap.Cluster == nil {
		t.Fatal("Cluster not assembled")
	}
	if snap.Nodes["pve1"] == nil {
		t.Fatal("Nodes[pve1] not assembled")
	}
	want := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}
	if snap.Guests[want] == nil {
		t.Fatalf("Guests[%v] not assembled; have keys %v", want, snap.Guests)
	}
}

func TestBuildSnapshot_MalformedGuestRulesetIDSkipped(t *testing.T) {
	malformed := &inventory.FwRuleset{
		Ref:   inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "not-the-expected-shape"},
		Scope: inventory.FwScopeGuest,
	}
	snap := fw.BuildSnapshot([]inventory.Entity{malformed})
	if len(snap.Guests) != 0 {
		t.Errorf("Guests = %v, want empty (malformed ID must be skipped, not misfiled)", snap.Guests)
	}
}

func TestBuildSnapshot_DoesNotAliasCallerSlices(t *testing.T) {
	rules := []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT"}}
	cluster := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Rules: rules}
	snap := fw.BuildSnapshot([]inventory.Entity{cluster})

	rules[0].Comment = "mutated after snapshot built"
	if snap.Cluster.Rules[0].Comment == "mutated after snapshot built" {
		t.Fatal("Snapshot shares backing storage with the caller's slice")
	}
}
