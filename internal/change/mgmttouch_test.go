package change_test

// T-703 AC5: touchesMgmtPath is computed server-side for *any* changeset
// touching a node's resolved management path (hand-built drafts included,
// wizard or not) and false otherwise — a table test over change.TouchesMgmtPath
// against a fixed management-path set.

import (
	"encoding/json"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// mgmtPathsPve1 models three-node-vlan's pve1 management path: vmbr0 carries
// the mgmt IP, resolved through bond0 -> eno1/eno2.
func mgmtPathsPve1() map[string][]topology.MgmtPath {
	ref := func(kind inventory.Kind, id string) inventory.Ref {
		return inventory.Ref{Kind: kind, Node: "pve1", ID: id}
	}
	return map[string][]topology.MgmtPath{
		"pve1": {{
			Ref:   ref(inventory.KindBridge, "vmbr0"),
			Roles: []topology.MgmtRole{topology.MgmtRoleMgmt},
			Path: []inventory.Ref{
				ref(inventory.KindBond, "bond0"),
				ref(inventory.KindPhysNic, "eno1"),
				ref(inventory.KindPhysNic, "eno2"),
			},
			Redundant: true,
		}},
	}
}

func opsFromJSON(t *testing.T, s string) []change.Op {
	t.Helper()
	var ops []change.Op
	if err := json.Unmarshal([]byte(s), &ops); err != nil {
		t.Fatalf("decoding ops: %v", err)
	}
	return ops
}

func TestTouchesMgmtPath(t *testing.T) {
	paths := mgmtPathsPve1()

	tests := []struct {
		name string
		ops  string
		want bool
	}{
		{
			name: "bond.update on the mgmt-path bond",
			ops:  `[{"op":"bond.update","target":"bond:pve1:bond0","params":{"slaves":["eno1","eno2","eno3"]}}]`,
			want: true,
		},
		{
			name: "bridge.port.remove on the mgmt carrier",
			ops:  `[{"op":"bridge.port.remove","target":"bridge:pve1:vmbr0","params":{"port":"eno1"}}]`,
			want: true,
		},
		{
			name: "iface.update on a path member NIC",
			ops:  `[{"op":"iface.update","target":"physnic:pve1:eno1","params":{"mtu":9000}}]`,
			want: true,
		},
		{
			name: "bond.create naming a path NIC as a slave",
			ops:  `[{"op":"bond.create","target":"bond:pve1:bondX","params":{"mode":"active-backup","slaves":["eno1","eno9"]}}]`,
			want: true,
		},
		{
			name: "bridge.port.add adding the mgmt bond back",
			ops:  `[{"op":"bridge.port.add","target":"bridge:pve1:vmbr0","params":{"port":"bond0"}}]`,
			want: true,
		},
		{
			name: "unrelated bridge on the same node",
			ops:  `[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"ports":["eno3"]}}]`,
			want: false,
		},
		{
			name: "same interface name on a different node",
			ops:  `[{"op":"bond.update","target":"bond:pve2:bond0","params":{"slaves":["eno1"]}}]`,
			want: false,
		},
		{
			name: "cluster-scoped sdn op never touches a node path",
			ops:  `[{"op":"sdn.zone.create","target":"sdn-zone::z1","params":{"type":"simple"}}]`,
			want: false,
		},
		{
			name: "empty ops",
			ops:  `[]`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := change.TouchesMgmtPath(paths, opsFromJSON(t, tt.ops))
			if got != tt.want {
				t.Errorf("TouchesMgmtPath = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTouchesMgmtPath_NoPaths(t *testing.T) {
	ops := opsFromJSON(t, `[{"op":"bond.update","target":"bond:pve1:bond0","params":{"slaves":["eno1"]}}]`)
	if change.TouchesMgmtPath(nil, ops) {
		t.Error("TouchesMgmtPath with no resolved paths should be false")
	}
}

func TestTouchesMgmtPath_RawReplaceOnNodeWithPath(t *testing.T) {
	// iface.raw.replace targets a node ref; it rewrites the whole file, so a
	// node that has any management path is touched.
	ops := opsFromJSON(t, `[{"op":"iface.raw.replace","target":"node:pve1:pve1","params":{"content":"auto lo\n"}}]`)
	if !change.TouchesMgmtPath(mgmtPathsPve1(), ops) {
		t.Error("iface.raw.replace on a node with a management path should touch it")
	}
	// ... but not a node without one.
	ops2 := opsFromJSON(t, `[{"op":"iface.raw.replace","target":"node:pve2:pve2","params":{"content":"auto lo\n"}}]`)
	if change.TouchesMgmtPath(mgmtPathsPve1(), ops2) {
		t.Error("iface.raw.replace on a node with no management path should not touch it")
	}
}

// TestTouchesMgmtPath_WireGuard is T-1401's mgmt-path interlock-coverage
// safety-analysis test: a wg.* op on a tunnel whose carrier interface is part
// of a node's resolved management/corosync path is touchesMgmtPath (inheriting
// T-703's typed-ack / 180s-floor ceremony with no override), and one whose
// carrier is off the path is not.
func TestTouchesMgmtPath_WireGuard(t *testing.T) {
	paths := mgmtPathsPve1() // pve1: vmbr0 (mgmt) via bond0 -> eno1/eno2
	tests := []struct {
		name string
		ops  string
		want bool
	}{
		{
			name: "wg.tunnel.create carried on the mgmt bridge",
			ops:  `[{"op":"wg.tunnel.create","target":"wg-tunnel:pve1:tun1","params":{"ifName":"wg0","carrier":"vmbr0"}}]`,
			want: true,
		},
		{
			name: "wg.tunnel.create carried on a mgmt-path bond slave",
			ops:  `[{"op":"wg.tunnel.create","target":"wg-tunnel:pve1:tun1","params":{"ifName":"wg0","carrier":"eno1"}}]`,
			want: true,
		},
		{
			name: "wg.tunnel.create carried on an unrelated interface",
			ops:  `[{"op":"wg.tunnel.create","target":"wg-tunnel:pve1:tun1","params":{"ifName":"wg0","carrier":"vmbr9"}}]`,
			want: false,
		},
		{
			name: "wg.tunnel.update moving carrier onto the mgmt bridge",
			ops:  `[{"op":"wg.tunnel.update","target":"wg-tunnel:pve1:tun1","params":{"carrier":"vmbr0"}}]`,
			want: true,
		},
		{
			name: "wg.tunnel.create carried on another node's iface named like the path",
			ops:  `[{"op":"wg.tunnel.create","target":"wg-tunnel:pve2:tun1","params":{"ifName":"wg0","carrier":"vmbr0"}}]`,
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := opsFromJSON(t, tc.ops)
			if got := change.TouchesMgmtPath(paths, ops); got != tc.want {
				t.Errorf("TouchesMgmtPath = %v, want %v", got, tc.want)
			}
		})
	}
}
