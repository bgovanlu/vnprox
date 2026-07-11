package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestSdnAffectedZones(t *testing.T) {
	tests := []struct {
		name string
		ops  []Op
		want []string
	}{
		{
			name: "zone create/update/delete all contribute directly",
			ops: []Op{
				mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "z1"), &SdnZoneCreateParams{Type: "simple"}),
				mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "z2"), &SdnZoneUpdateParams{}),
				mkOp(OpSdnZoneDelete, testRef(inventory.KindSDNZone, "", "z3"), &SdnZoneDeleteParams{}),
			},
			want: []string{"z1", "z2", "z3"},
		},
		{
			name: "vnet create resolves its zone via params",
			ops: []Op{
				mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetCreateParams{Zone: "z1"}),
			},
			want: []string{"z1"},
		},
		{
			name: "vnet update/delete resolve zone from the id prefix",
			ops: []Op{
				mkOp(OpSdnVnetUpdate, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetUpdateParams{}),
				mkOp(OpSdnVnetDelete, testRef(inventory.KindSDNVnet, "", "z2/v1"), &SdnVnetDeleteParams{}),
			},
			want: []string{"z1", "z2"},
		},
		{
			name: "subnet create resolves zone via the changeset's own vnet.create",
			ops: []Op{
				mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetCreateParams{Zone: "z1"}),
				mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetCreateParams{Vnet: "z1/v1", CIDR: "10.0.0.0/24"}),
			},
			want: []string{"z1"},
		},
		{
			name: "subnet create resolves zone from the vnet id prefix when not touched in-changeset",
			ops: []Op{
				mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetCreateParams{Vnet: "z9/v1", CIDR: "10.0.0.0/24"}),
			},
			want: []string{"z9"},
		},
		{
			name: "no sdn ops -> no zones",
			ops:  []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr1"), &BridgeCreateParams{})},
			want: nil,
		},
		{
			name: "duplicates collapse to one entry",
			ops: []Op{
				mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "z1"), &SdnZoneCreateParams{Type: "simple"}),
				mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetCreateParams{Zone: "z1"}),
			},
			want: []string{"z1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sdnAffectedZones(tt.ops)
			if !stringSlicesEqual(got, tt.want) {
				t.Errorf("sdnAffectedZones = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveSubnetVnet(t *testing.T) {
	t.Run("resolves from an earlier create op in the same changeset", func(t *testing.T) {
		ops := []Op{
			mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetCreateParams{Vnet: "z1/v1", CIDR: "10.0.0.0/24"}),
			mkOp(OpSdnSubnetUpdate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetUpdateParams{}),
		}
		got := resolveSubnetVnet(ops, 1, buildSnapshot(), "10.0.0.0/24")
		if got != "z1/v1" {
			t.Errorf("resolveSubnetVnet = %q, want z1/v1", got)
		}
	})

	t.Run("resolves from the live inventory snapshot", func(t *testing.T) {
		snap := buildSnapshot(&inventory.SdnSubnet{
			Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "z1/v1",
		})
		got := resolveSubnetVnet(nil, 0, snap, "10.0.0.0/24")
		if got != "z1/v1" {
			t.Errorf("resolveSubnetVnet = %q, want z1/v1", got)
		}
	})

	t.Run("unresolvable returns empty string", func(t *testing.T) {
		got := resolveSubnetVnet(nil, 0, buildSnapshot(), "10.0.0.0/24")
		if got != "" {
			t.Errorf("resolveSubnetVnet = %q, want empty", got)
		}
	})

	t.Run("a create op after uptoIdx is not consulted", func(t *testing.T) {
		ops := []Op{
			mkOp(OpSdnSubnetUpdate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetUpdateParams{}),
			mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetCreateParams{Vnet: "z1/v1", CIDR: "10.0.0.0/24"}),
		}
		got := resolveSubnetVnet(ops, 0, buildSnapshot(), "10.0.0.0/24")
		if got != "" {
			t.Errorf("resolveSubnetVnet = %q, want empty (create is after uptoIdx)", got)
		}
	})
}

func TestSdnRestoreOps(t *testing.T) {
	t.Run("creates-only current reverts to deletes, deepest first", func(t *testing.T) {
		pre := SDNConfig{}
		current := SDNConfig{
			Zones:   []SDNZoneConfig{{ID: "z1", Type: "vlan", Bridge: "vmbr0"}},
			Vnets:   []SDNVnetConfig{{ID: "z1/v1", Zone: "z1", Tag: 10}},
			Subnets: []SDNSubnetConfig{{ID: "10.0.0.0/24", Vnet: "z1/v1"}},
		}
		ops := sdnRestoreOps(pre, current)
		if len(ops) != 3 {
			t.Fatalf("got %d ops, want 3: %+v", len(ops), ops)
		}
		wantOrder := []OpType{OpSdnSubnetDelete, OpSdnVnetDelete, OpSdnZoneDelete}
		for i, w := range wantOrder {
			if ops[i].op.Type != w {
				t.Errorf("op %d type = %s, want %s", i, ops[i].op.Type, w)
			}
		}
		if ops[0].vnet != "z1/v1" {
			t.Errorf("subnet delete vnet hint = %q, want z1/v1", ops[0].vnet)
		}
	})

	t.Run("pre-only (deleted by the failed apply) reverts to creates, shallowest first", func(t *testing.T) {
		pre := SDNConfig{
			Zones:   []SDNZoneConfig{{ID: "z1", Type: "vlan", Bridge: "vmbr0", Nodes: []string{"pve1"}}},
			Vnets:   []SDNVnetConfig{{ID: "z1/v1", Zone: "z1", Tag: 10}},
			Subnets: []SDNSubnetConfig{{ID: "10.0.0.0/24", Vnet: "z1/v1", Gateway: "10.0.0.1"}},
		}
		current := SDNConfig{}
		ops := sdnRestoreOps(pre, current)
		if len(ops) != 3 {
			t.Fatalf("got %d ops, want 3: %+v", len(ops), ops)
		}
		wantOrder := []OpType{OpSdnZoneCreate, OpSdnVnetCreate, OpSdnSubnetCreate}
		for i, w := range wantOrder {
			if ops[i].op.Type != w {
				t.Errorf("op %d type = %s, want %s", i, ops[i].op.Type, w)
			}
		}
		zp, ok := ops[0].op.Params.(*SdnZoneCreateParams)
		if !ok || zp.Bridge != "vmbr0" || len(zp.Nodes) != 1 {
			t.Errorf("zone create params = %+v, want bridge vmbr0 / 1 node restored", ops[0].op.Params)
		}
		sp, ok := ops[2].op.Params.(*SdnSubnetCreateParams)
		if !ok || sp.CIDR != "10.0.0.0/24" || sp.Gateway != "10.0.0.1" {
			t.Errorf("subnet create params = %+v, want cidr/gateway restored", ops[2].op.Params)
		}
		if ops[2].vnet != "z1/v1" {
			t.Errorf("subnet create vnet hint = %q, want z1/v1", ops[2].vnet)
		}
	})

	t.Run("present in both but changed reverts via update", func(t *testing.T) {
		pre := SDNConfig{Zones: []SDNZoneConfig{{ID: "z1", Type: "vlan", Bridge: "vmbr0", MTU: 1500}}}
		current := SDNConfig{Zones: []SDNZoneConfig{{ID: "z1", Type: "vlan", Bridge: "vmbr0", MTU: 9000}}}
		ops := sdnRestoreOps(pre, current)
		if len(ops) != 1 || ops[0].op.Type != OpSdnZoneUpdate {
			t.Fatalf("ops = %+v, want a single sdn.zone.update", ops)
		}
		p, ok := ops[0].op.Params.(*SdnZoneUpdateParams)
		if !ok || p.MTU == nil || *p.MTU != 1500 {
			t.Errorf("update params = %+v, want mtu restored to 1500", ops[0].op.Params)
		}
	})

	t.Run("identical pre/current produces no ops", func(t *testing.T) {
		cfg := SDNConfig{
			Zones:   []SDNZoneConfig{{ID: "z1", Type: "vlan", Bridge: "vmbr0"}},
			Vnets:   []SDNVnetConfig{{ID: "z1/v1", Zone: "z1"}},
			Subnets: []SDNSubnetConfig{{ID: "10.0.0.0/24", Vnet: "z1/v1"}},
		}
		ops := sdnRestoreOps(cfg, cfg)
		if len(ops) != 0 {
			t.Errorf("ops = %+v, want none", ops)
		}
	})
}
