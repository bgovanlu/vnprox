package ifaces

import (
	"encoding/json"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestDecodeOp_RoundTrips(t *testing.T) {
	cases := []struct {
		want Op
		name string
		json string
	}{
		{
			name: "bond.create",
			json: `{"op":"bond.create","target":"bond:pve1:bond0","params":{"mode":"802.3ad","slaves":["eno1","eno2"],"mtu":9000,"autostart":true}}`,
			want: BondCreate{Target: ref(inventory.KindBond, "pve1", "bond0"), Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, MTU: 9000, Autostart: true},
		},
		{
			name: "bridge.port.add",
			json: `{"op":"bridge.port.add","target":"bridge:pve1:vmbr0","params":{"port":"eno3"}}`,
			want: BridgePortAdd{Target: ref(inventory.KindBridge, "pve1", "vmbr0"), Port: "eno3"},
		},
		{
			name: "vlan.delete",
			json: `{"op":"vlan.delete","target":"vlan:pve2:vmbr0.20","params":{}}`,
			want: VlanDelete{Target: ref(inventory.KindVlan, "pve2", "vmbr0.20")},
		},
		{
			name: "iface.update",
			json: `{"op":"iface.update","target":"physnic:pve1:eno1","params":{"mtu":1500,"autostart":false}}`,
			want: IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve1", "eno1"), MTU: intp(1500), Autostart: boolp(false)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeOp(json.RawMessage(tc.json))
			if err != nil {
				t.Fatalf("DecodeOp: %v", err)
			}
			if got.Kind() != tc.want.Kind() || got.Ref() != tc.want.Ref() {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDecodeOps_Array(t *testing.T) {
	raw := json.RawMessage(`[
		{"op":"bond.create","target":"bond:pve1:bond0","params":{"slaves":["eno1"]}},
		{"op":"bond.delete","target":"bond:pve1:bond0","params":{}}
	]`)
	ops, err := DecodeOps(raw)
	if err != nil {
		t.Fatalf("DecodeOps: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("len(ops) = %d, want 2", len(ops))
	}
	if ops[0].Kind() != OpBondCreate || ops[1].Kind() != OpBondDelete {
		t.Errorf("unexpected op kinds: %v, %v", ops[0].Kind(), ops[1].Kind())
	}
}

func TestDecodeOp_UnknownType(t *testing.T) {
	_, err := DecodeOp(json.RawMessage(`{"op":"sdn.zone.create","target":"sdn-zone::z1","params":{}}`))
	if err == nil {
		t.Fatal("expected an error for an unsupported op type")
	}
}

func TestDecodeOp_BadTarget(t *testing.T) {
	_, err := DecodeOp(json.RawMessage(`{"op":"bond.create","target":"not-a-ref","params":{}}`))
	if err == nil {
		t.Fatal("expected an error for a malformed target ref")
	}
}

func intp(v int) *int    { return &v }
func boolp(v bool) *bool { return &v }
