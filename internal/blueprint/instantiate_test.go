// SPDX-License-Identifier: Apache-2.0

package blueprint_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
)

// simpleBridgeBP is a minimal single-entity blueprint used to exercise
// diffEntity's create/skip/update paths in isolation, independent of any
// starter's own shape.
func simpleBridgeBP() *blueprint.Blueprint {
	return &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Params: []blueprint.ParamDef{
			{Name: "bridgeName", Type: blueprint.ParamString, Default: "vmbr0"},
			{Name: "vlans", Type: blueprint.ParamVIDList, Default: []any{10, 20}},
			{Name: "addr", Type: blueprint.ParamCIDR, Default: "192.168.1.10/24"},
		},
		Entities: []blueprint.EntityTemplate{
			{
				Kind: blueprint.KindBridge, IDTemplate: "{{bridgeName}}",
				Fields: map[string]any{
					"vlanAware": true, "vids": "{{vlans}}", "addresses": []any{"{{addr}}"},
				},
			},
		},
	}
}

// AC1: instantiating against a bare fixture (no such bridge yet) produces
// a single bridge.create op.
func TestInstantiate_BareFixture_ProducesCreate(t *testing.T) {
	g := newGraphWithNodes("pve1")
	bp := simpleBridgeBP()

	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeCreate {
		t.Fatalf("got %v, want a single bridge.create", opTypes(ops))
	}
	create, ok := ops[0].Params.(*change.BridgeCreateParams)
	if !ok {
		t.Fatalf("params type = %T, want *BridgeCreateParams", ops[0].Params)
	}
	if !create.VlanAware || len(create.Vids) != 2 || len(create.Addresses) != 1 || create.Addresses[0] != "192.168.1.10/24" {
		t.Fatalf("unexpected create params: %+v", create)
	}
}

// AC1 (the other half of idempotency): instantiating again against a
// fixture that already exactly matches the blueprint's desired state
// produces zero ops.
func TestInstantiate_ConformingFixture_ProducesZeroOps(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{
		vlanAware: true, vids: []int{10, 20}, addresses: []string{"192.168.1.10/24"},
	})
	bp := simpleBridgeBP()

	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %v ops, want zero (already conforming)", opTypes(ops))
	}
}

// AC2: a fixture that diverges in exactly one field (vlanAware false, vids
// and addresses otherwise matching) produces a single update op carrying
// only that field.
func TestInstantiate_DivergentFixture_ProducesUpdateForDivergentFieldOnly(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{
		vlanAware: false, vids: []int{10, 20}, addresses: []string{"192.168.1.10/24"},
	})
	bp := simpleBridgeBP()

	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeUpdate {
		t.Fatalf("got %v, want a single bridge.update", opTypes(ops))
	}
	upd, ok := ops[0].Params.(*change.BridgeUpdateParams)
	if !ok {
		t.Fatalf("params type = %T", ops[0].Params)
	}
	if upd.VlanAware == nil || !*upd.VlanAware {
		t.Fatalf("VlanAware = %v, want pointer to true", upd.VlanAware)
	}
	if upd.Vids != nil {
		t.Fatalf("Vids = %v, want nil (not divergent)", *upd.Vids)
	}
	if upd.Addresses != nil {
		t.Fatalf("Addresses = %v, want nil (not divergent)", *upd.Addresses)
	}
}

// Bridge port membership diverges via bridge.port.add/remove, never
// bridge.update (BridgeUpdateParams has no Ports field).
func TestInstantiate_BridgePorts_DivergeAsAddRemoveOps(t *testing.T) {
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{ports: []string{"eno1", "eno2"}})
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "vmbr0", Fields: map[string]any{
				"ports": []any{"eno1", "eno3"},
			}},
		},
	}
	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	var add, remove int
	for _, op := range ops {
		switch op.Type {
		case change.OpBridgePortAdd:
			add++
			if op.Params.(*change.BridgePortAddParams).Port != "eno3" {
				t.Fatalf("added wrong port: %+v", op.Params)
			}
		case change.OpBridgePortRemove:
			remove++
			if op.Params.(*change.BridgePortRemoveParams).Port != "eno2" {
				t.Fatalf("removed wrong port: %+v", op.Params)
			}
		default:
			t.Fatalf("unexpected op %s", op.Type)
		}
	}
	if add != 1 || remove != 1 {
		t.Fatalf("add=%d remove=%d, want 1 and 1 (ops: %v)", add, remove, opTypes(ops))
	}
}

// Bond create/skip/update.
func TestInstantiate_Bond(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBond, IDTemplate: "bond0", Fields: map[string]any{
				"mode": "802.3ad", "slaves": []any{"eno1", "eno2"},
			}},
		},
	}

	t.Run("bare", func(t *testing.T) {
		g := newGraphWithNodes("pve1")
		ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		if len(ops) != 1 || ops[0].Type != change.OpBondCreate {
			t.Fatalf("got %v, want bond.create", opTypes(ops))
		}
	})

	t.Run("conforming", func(t *testing.T) {
		g := newGraphWithNodes("pve1")
		applyBond(g, "pve1", "bond0", bondOpts{mode: "802.3ad", slaves: []string{"eno1", "eno2"}})
		ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		if len(ops) != 0 {
			t.Fatalf("got %v, want zero ops", opTypes(ops))
		}
	})

	t.Run("divergent mode", func(t *testing.T) {
		g := newGraphWithNodes("pve1")
		applyBond(g, "pve1", "bond0", bondOpts{mode: "active-backup", slaves: []string{"eno1", "eno2"}})
		ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve1"}}, g.Snapshot())
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		if len(ops) != 1 || ops[0].Type != change.OpBondUpdate {
			t.Fatalf("got %v, want bond.update", opTypes(ops))
		}
		upd := ops[0].Params.(*change.BondUpdateParams)
		if upd.Mode == nil || *upd.Mode != "802.3ad" {
			t.Fatalf("Mode = %v", upd.Mode)
		}
		if upd.Slaves != nil {
			t.Fatalf("Slaves = %v, want nil (not divergent)", *upd.Slaves)
		}
	})
}

// SDN zone/vnet/subnet create/skip/update, and the __nodes__ builtin.
func TestInstantiate_SdnZoneVnetSubnet(t *testing.T) {
	bp := &blueprint.Blueprint{
		BlueprintVersion: 1, ID: "t", Name: "t",
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectSingle},
		Params: []blueprint.ParamDef{
			{Name: "vni", Type: blueprint.ParamInt, Default: 100},
		},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindSdnZone, IDTemplate: "z1", Fields: map[string]any{
				"type": "vxlan", "nodes": "{{__nodes__}}", "vrfVxlan": "{{vni}}",
			}},
			{Kind: blueprint.KindSdnVnet, IDTemplate: "z1/v1", Fields: map[string]any{"zone": "z1"}},
			{Kind: blueprint.KindSdnSubnet, IDTemplate: "10.1.0.0/24", Fields: map[string]any{
				"vnet": "z1/v1", "cidr": "10.1.0.0/24", "gateway": "10.1.0.1",
			}},
		},
	}

	t.Run("bare", func(t *testing.T) {
		g := newGraphWithNodes("pve1", "pve2")
		ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{}, g.Snapshot())
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		want := []change.OpType{change.OpSdnZoneCreate, change.OpSdnVnetCreate, change.OpSdnSubnetCreate}
		if got := opTypes(ops); !equalOpTypes(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		zoneParams := ops[0].Params.(*change.SdnZoneCreateParams)
		if len(zoneParams.Nodes) != 2 {
			t.Fatalf("zone nodes = %v, want both cluster nodes", zoneParams.Nodes)
		}
	})

	t.Run("conforming", func(t *testing.T) {
		g := newGraphWithNodes("pve1", "pve2")
		applySdnZone(g, "z1", sdnZoneOpts{typ: "vxlan", nodes: []string{"pve1", "pve2"}, vrfVxlan: 100})
		applySdnVnet(g, "z1", "v1", 0, false)
		applySdnSubnet(g, "z1/v1", "10.1.0.0/24", "10.1.0.1", false)
		ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{}, g.Snapshot())
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		if len(ops) != 0 {
			t.Fatalf("got %v, want zero ops", opTypes(ops))
		}
	})

	t.Run("divergent vrfVxlan", func(t *testing.T) {
		g := newGraphWithNodes("pve1", "pve2")
		applySdnZone(g, "z1", sdnZoneOpts{typ: "vxlan", nodes: []string{"pve1", "pve2"}, vrfVxlan: 999})
		applySdnVnet(g, "z1", "v1", 0, false)
		applySdnSubnet(g, "z1/v1", "10.1.0.0/24", "10.1.0.1", false)
		ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{}, g.Snapshot())
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		if len(ops) != 1 || ops[0].Type != change.OpSdnZoneUpdate {
			t.Fatalf("got %v, want a single sdn.zone.update", opTypes(ops))
		}
		upd := ops[0].Params.(*change.SdnZoneUpdateParams)
		if upd.VrfVxlan == nil || *upd.VrfVxlan != 100 {
			t.Fatalf("VrfVxlan = %v, want 100", upd.VrfVxlan)
		}
		if upd.Nodes != nil {
			t.Fatalf("Nodes = %v, want nil (not divergent)", *upd.Nodes)
		}
	})
}

func equalOpTypes(got, want []change.OpType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
