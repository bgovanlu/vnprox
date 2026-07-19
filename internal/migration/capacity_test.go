package migration

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func capRef(kind inventory.Kind, node, id string) inventory.Ref {
	return inventory.Ref{Kind: kind, Node: node, ID: id}
}

// TestResolveLinkCapacityMbps_Bond: a bonded pair of 1000Mbps NICs on each
// side sums to 2000Mbps per node; the pair's capacity is the lesser side.
func TestResolveLinkCapacityMbps_Bond(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.PhysNic{Ref: capRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1", SpeedMbps: 1000, LinkUp: true},
		&inventory.PhysNic{Ref: capRef(inventory.KindPhysNic, "pve1", "eno2"), Name: "eno2", SpeedMbps: 1000, LinkUp: true},
		&inventory.PhysNic{Ref: capRef(inventory.KindPhysNic, "pve2", "eno1"), Name: "eno1", SpeedMbps: 1000, LinkUp: true},
		&inventory.Bond{Ref: capRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0", Slaves: []string{"eno1", "eno2"}},
	})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{}, []inventory.Entity{
		&inventory.Bridge{
			Ref: capRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux,
			DeclaredPortNames: []string{"bond0"},
		},
		&inventory.Bridge{
			Ref: capRef(inventory.KindBridge, "pve2", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux,
			DeclaredPortNames: []string{"eno1"},
		},
	})

	cap, ok := resolveLinkCapacityMbps(g.Snapshot(), "pve1", "pve2")
	if !ok {
		t.Fatal("expected a resolvable capacity")
	}
	if cap.Mbps != 1000 {
		t.Errorf("Mbps = %v, want 1000 (pve1 has 2000 via bond0, pve2 has 1000 via eno1 — bottleneck is pve2)", cap.Mbps)
	}
	if cap.BridgeName != "vmbr0" {
		t.Errorf("BridgeName = %q, want vmbr0", cap.BridgeName)
	}
}

// TestResolveLinkCapacityMbps_NoSharedBridge: two nodes with no
// same-named bridge resolve ok=false.
func TestResolveLinkCapacityMbps_NoSharedBridge(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.PhysNic{Ref: capRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1", SpeedMbps: 1000, LinkUp: true},
	})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{}, []inventory.Entity{
		&inventory.Bridge{
			Ref: capRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux,
			DeclaredPortNames: []string{"eno1"},
		},
	})

	if _, ok := resolveLinkCapacityMbps(g.Snapshot(), "pve1", "pve2"); ok {
		t.Error("expected ok=false when pve2 has no bridges at all")
	}
}
