package blueprint_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Every starter must round-trip through Validate — a typo in starters.go
// fails the test suite, not a user's instantiate call.
func TestStarters_AllValid(t *testing.T) {
	for _, bp := range blueprint.Starters() {
		bp := bp
		t.Run(bp.ID, func(t *testing.T) {
			if !bp.ReadOnly {
				t.Errorf("starter %s: ReadOnly = false, want true", bp.ID)
			}
			if err := blueprint.Validate(bp); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

// TestStarters_AllDistinctIDs guards against a copy-paste ID collision
// between starters (Service.StarterByID's lookup silently returns the
// first match, so a duplicate would be a silent bug, not a crash).
func TestStarters_AllDistinctIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, bp := range blueprint.Starters() {
		if seen[bp.ID] {
			t.Fatalf("duplicate starter id %q", bp.ID)
		}
		seen[bp.ID] = true
	}
}

// T-603 AC1: each starter instantiates against a bare fixture to a golden
// (non-empty) changeset, and against an already-conforming fixture to a
// zero-op changeset (idempotent both ways).
func TestStarters_BareAndConforming(t *testing.T) {
	cases := []struct {
		seed      func(g *inventory.Graph, nodes []string)
		id        string
		nodes     []string
		wantTypes []change.OpType
	}{
		{
			id: blueprint.StarterSingleNICHomelab, nodes: []string{"pve1"},
			wantTypes: []change.OpType{change.OpBridgeCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applyBridge(g, "pve1", "vmbr0", bridgeOpts{
					ports: []string{"eno1"}, vlanAware: true, vids: []int{10, 20, 30},
					addresses: []string{"192.168.1.10/24"}, comments: "vnprox blueprint: single-nic-homelab",
				})
			},
		},
		{
			id: blueprint.StarterDualNICMgmtTrunk, nodes: []string{"pve1"},
			wantTypes: []change.OpType{change.OpBridgeCreate, change.OpBridgeCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applyBridge(g, "pve1", "vmbr0", bridgeOpts{
					ports: []string{"eno1"}, addresses: []string{"192.168.1.10/24"},
					comments: "vnprox blueprint: dual-nic-mgmt-trunk (management)",
				})
				applyBridge(g, "pve1", "vmbr1", bridgeOpts{
					ports: []string{"eno2"}, vlanAware: true, vids: []int{10, 20, 30},
					comments: "vnprox blueprint: dual-nic-mgmt-trunk (guest trunk)",
				})
			},
		},
		{
			id: blueprint.StarterLACPBondStorage, nodes: []string{"pve1"},
			wantTypes: []change.OpType{change.OpBondCreate, change.OpBridgeCreate, change.OpVlanCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applyBond(g, "pve1", "bond0", bondOpts{mode: "802.3ad", slaves: []string{"eno1", "eno2"}})
				applyBridge(g, "pve1", "vmbr0", bridgeOpts{
					ports: []string{"bond0"}, addresses: []string{"192.168.1.10/24"},
					comments: "vnprox blueprint: lacp-bond-storage-vlan (management)",
				})
				applyVlan(g, "pve1", "bond0.30", "bond0", 30, []string{"10.30.0.10/24"}, 0)
			},
		},
		{
			id: blueprint.StarterVXLANOverlay, nodes: []string{"pve1", "pve2", "pve3"},
			wantTypes: []change.OpType{change.OpSdnZoneCreate, change.OpSdnVnetCreate, change.OpSdnSubnetCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applySdnZone(g, "vxzone1", sdnZoneOpts{typ: "vxlan", nodes: nodes, vrfVxlan: 10000})
				applySdnVnet(g, "vxzone1", "vxnet1", 0, false)
				applySdnSubnet(g, "vxzone1/vxnet1", "10.100.0.0/24", "10.100.0.1", false)
			},
		},
		{
			id: blueprint.StarterEVPNDatacenter, nodes: []string{"pve1", "pve2", "pve3"},
			wantTypes: []change.OpType{change.OpSdnZoneCreate, change.OpSdnVnetCreate, change.OpSdnSubnetCreate},
			seed: func(g *inventory.Graph, nodes []string) {
				applySdnZone(g, "evpnzone1", sdnZoneOpts{typ: "evpn", controller: "evpn1", nodes: nodes, vrfVxlan: 20000})
				applySdnVnet(g, "evpnzone1", "evpnnet1", 0, false)
				applySdnSubnet(g, "evpnzone1/evpnnet1", "10.200.0.0/24", "10.200.0.1", true)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			bp, ok := blueprint.StarterByID(tc.id)
			if !ok {
				t.Fatalf("no such starter %q", tc.id)
			}

			t.Run("bare", func(t *testing.T) {
				g := newGraphWithNodes(tc.nodes...)
				ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: tc.nodes}, g.Snapshot())
				if err != nil {
					t.Fatalf("Instantiate: %v", err)
				}
				if got := opTypes(ops); !equalOpTypes(got, tc.wantTypes) {
					t.Fatalf("got %v, want %v", got, tc.wantTypes)
				}
			})

			t.Run("conforming", func(t *testing.T) {
				g := newGraphWithNodes(tc.nodes...)
				tc.seed(g, tc.nodes)
				ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: tc.nodes}, g.Snapshot())
				if err != nil {
					t.Fatalf("Instantiate: %v", err)
				}
				if len(ops) != 0 {
					t.Fatalf("got %v, want zero ops (already conforming)", opTypes(ops))
				}
			})
		})
	}
}

// T-603 AC2: a fixture diverging in exactly one field of the single-NIC
// homelab starter (VLAN-awareness) produces an update op naming only that
// field.
func TestStarters_Divergent_UpdateOnlyDivergentField(t *testing.T) {
	bp, _ := blueprint.StarterByID(blueprint.StarterSingleNICHomelab)
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{
		ports: []string{"eno1"}, vlanAware: false, // divergent: starter wants true
		vids: []int{10, 20, 30}, addresses: []string{"192.168.1.10/24"},
		comments: "vnprox blueprint: single-nic-homelab",
	})

	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeUpdate {
		t.Fatalf("got %v, want a single bridge.update", opTypes(ops))
	}
	upd := ops[0].Params.(*change.BridgeUpdateParams)
	if upd.VlanAware == nil || !*upd.VlanAware {
		t.Fatalf("VlanAware = %v, want pointer-to-true", upd.VlanAware)
	}
	if upd.Vids != nil || upd.Addresses != nil || upd.Comments != nil || upd.MTU != nil || upd.Gateway != nil || upd.STP != nil {
		t.Fatalf("expected only VlanAware set, got %+v", upd)
	}
}
