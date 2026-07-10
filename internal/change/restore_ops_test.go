package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Table-driven coverage of restoreOpsForNode's per-kind update/create/delete
// synthesis — the entity-state diff that both POST /snapshots/{id}/restore
// and rollback-of-committed drafts are built from.
func TestRestoreOpsForNode_Table(t *testing.T) {
	lo := "auto lo\niface lo inet loopback\n\n"

	cases := []struct {
		verify func(t *testing.T, ops []Op)
		name   string
		target string
		live   string
	}{
		{
			name:   "bond field updates (mode, slaves, lacp, hash, mtu)",
			target: lo + "auto bond0\niface bond0 inet manual\n\tbond-slaves eno1 eno2\n\tbond-mode 802.3ad\n\tbond-miimon 100\n\tbond-lacp-rate fast\n\tbond-xmit-hash-policy layer3+4\n\tmtu 9000\n",
			live:   lo + "auto bond0\niface bond0 inet manual\n\tbond-slaves eno1\n\tbond-mode active-backup\n\tbond-miimon 100\n",
			verify: func(t *testing.T, ops []Op) {
				if len(ops) != 1 || ops[0].Type != OpBondUpdate {
					t.Fatalf("ops = %+v, want one bond.update", ops)
				}
				p, ok := ops[0].Params.(*BondUpdateParams)
				if !ok {
					t.Fatalf("params type %T", ops[0].Params)
				}
				if p.Mode == nil || *p.Mode != "802.3ad" {
					t.Fatalf("mode = %v, want 802.3ad", p.Mode)
				}
				if p.Slaves == nil || len(*p.Slaves) != 2 {
					t.Fatalf("slaves = %v, want [eno1 eno2]", p.Slaves)
				}
				if p.LACPRate == nil || *p.LACPRate == "" {
					t.Fatalf("lacpRate = %v, want set", p.LACPRate)
				}
				if p.XmitHashPolicy == nil || *p.XmitHashPolicy != "layer3+4" {
					t.Fatalf("xmitHashPolicy = %v", p.XmitHashPolicy)
				}
				if p.MTU == nil || *p.MTU != 9000 {
					t.Fatalf("mtu = %v, want 9000", p.MTU)
				}
			},
		},
		{
			name:   "bond with no declared differences yields no ops",
			target: lo + "auto bond0\niface bond0 inet manual\n\tbond-slaves eno1\n\tbond-mode active-backup\n",
			live:   lo + "auto bond0\niface bond0 inet manual\n\tbond-slaves eno1\n\tbond-mode active-backup\n\t# a comment the model ignores\n",
			verify: func(t *testing.T, ops []Op) {
				if len(ops) != 0 {
					t.Fatalf("ops = %+v, want none", ops)
				}
			},
		},
		{
			name:   "vlan address and mtu update",
			target: lo + "auto vmbr0.10\niface vmbr0.10 inet static\n\taddress 10.0.10.1/24\n\tvlan-raw-device vmbr0\n\tmtu 1400\n",
			live:   lo + "auto vmbr0.10\niface vmbr0.10 inet manual\n\tvlan-raw-device vmbr0\n",
			verify: func(t *testing.T, ops []Op) {
				if len(ops) != 1 || ops[0].Type != OpVlanUpdate {
					t.Fatalf("ops = %+v, want one vlan.update", ops)
				}
				p := ops[0].Params.(*VlanUpdateParams)
				if p.Addresses == nil || len(*p.Addresses) != 1 || (*p.Addresses)[0] != "10.0.10.1/24" {
					t.Fatalf("addresses = %v", p.Addresses)
				}
				if p.MTU == nil || *p.MTU != 1400 {
					t.Fatalf("mtu = %v, want 1400", p.MTU)
				}
			},
		},
		{
			name:   "vlan create and bond delete",
			target: lo + "auto vmbr0.20\niface vmbr0.20 inet manual\n\tvlan-raw-device vmbr0\n",
			live:   lo + "auto bond1\niface bond1 inet manual\n\tbond-slaves eno3\n\tbond-mode active-backup\n",
			verify: func(t *testing.T, ops []Op) {
				kinds := map[OpType]bool{}
				for _, op := range ops {
					kinds[op.Type] = true
				}
				if !kinds[OpVlanCreate] || !kinds[OpBondDelete] || len(ops) != 2 {
					t.Fatalf("ops = %+v, want vlan.create + bond.delete", ops)
				}
			},
		},
		{
			name:   "physnic declared-MTU update",
			target: lo + "auto eno1\niface eno1 inet manual\n\tmtu 9000\n",
			live:   lo + "auto eno1\niface eno1 inet manual\n",
			verify: func(t *testing.T, ops []Op) {
				if len(ops) != 1 || ops[0].Type != OpIfaceUpdate {
					t.Fatalf("ops = %+v, want one iface.update", ops)
				}
				p := ops[0].Params.(*IfaceUpdateParams)
				if p.MTU == nil || *p.MTU != 9000 {
					t.Fatalf("mtu = %v, want 9000", p.MTU)
				}
			},
		},
		{
			name: "kind change on the same name deletes then recreates",
			// "br0" is a bond in the live state but a bridge in the target.
			target: lo + "auto br0\niface br0 inet manual\n\tbridge-ports eno1\n",
			live:   lo + "auto br0\niface br0 inet manual\n\tbond-slaves eno1\n\tbond-mode active-backup\n",
			verify: func(t *testing.T, ops []Op) {
				if len(ops) != 2 || ops[0].Type != OpBondDelete || ops[1].Type != OpBridgeCreate {
					t.Fatalf("ops = %+v, want [bond.delete, bridge.create]", ops)
				}
			},
		},
		{
			name:   "bridge vlan-aware, vids, gateway, stp, comment update",
			target: lo + "auto vmbr0\niface vmbr0 inet static\n\taddress 10.0.0.2/24\n\tgateway 10.0.0.1\n\tbridge-ports eno1\n\tbridge-vlan-aware yes\n\tbridge-vids 2-100\n\tbridge-stp on\n\t#uplink bridge\n",
			live:   lo + "auto vmbr0\niface vmbr0 inet manual\n\tbridge-ports eno1\n\tbridge-stp off\n\tbridge-fd 0\n",
			verify: func(t *testing.T, ops []Op) {
				if len(ops) != 1 || ops[0].Type != OpBridgeUpdate {
					t.Fatalf("ops = %+v, want one bridge.update", ops)
				}
				p := ops[0].Params.(*BridgeUpdateParams)
				if p.VlanAware == nil || !*p.VlanAware {
					t.Fatalf("vlanAware = %v, want true", p.VlanAware)
				}
				if p.Vids == nil || len(*p.Vids) != 1 || (*p.Vids)[0] != (VidRange{Low: 2, High: 100}) {
					t.Fatalf("vids = %v", p.Vids)
				}
				if p.Gateway == nil || *p.Gateway != "10.0.0.1" {
					t.Fatalf("gateway = %v", p.Gateway)
				}
				if p.STP == nil || !*p.STP {
					t.Fatalf("stp = %v, want true", p.STP)
				}
				if p.Comments == nil || !strings.Contains(*p.Comments, "uplink") {
					t.Fatalf("comments = %v", p.Comments)
				}
				if p.Addresses == nil || len(*p.Addresses) != 1 {
					t.Fatalf("addresses = %v", p.Addresses)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops, err := restoreOpsForNode("pve1", tc.target, tc.live)
			if err != nil {
				t.Fatalf("restoreOpsForNode: %v", err)
			}
			tc.verify(t, ops)
		})
	}
}

func TestRestoreOpsForNode_ParseErrors(t *testing.T) {
	bad := "iface\n" // malformed: iface line with no name
	if _, err := restoreOpsForNode("pve1", bad, "auto lo\niface lo inet loopback\n"); err == nil {
		t.Fatal("expected a parse error for malformed target content")
	}
	if _, err := restoreOpsForNode("pve1", "auto lo\niface lo inet loopback\n", bad); err == nil {
		t.Fatal("expected a parse error for malformed live content")
	}
}

func TestVidsEqualAndHelpers(t *testing.T) {
	a := []inventory.VidRange{{Low: 2, High: 10}, {Low: 20, High: 20}}
	b := []inventory.VidRange{{Low: 20, High: 20}, {Low: 2, High: 10}}
	if !vidsEqual(a, b) {
		t.Fatal("vidsEqual should be order-insensitive")
	}
	if vidsEqual(a, a[:1]) {
		t.Fatal("vidsEqual must compare lengths")
	}
	if vidsEqual(a, []inventory.VidRange{{Low: 2, High: 10}, {Low: 21, High: 21}}) {
		t.Fatal("vidsEqual must compare members")
	}
	if !stringsEqualUnordered([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatal("stringsEqualUnordered should be order-insensitive")
	}
	if stringsEqualUnordered([]string{"a"}, []string{"b"}) {
		t.Fatal("stringsEqualUnordered must compare members")
	}
	if toParamVids(nil) != nil {
		t.Fatal("toParamVids(nil) should be nil")
	}
	got := toParamVids([]inventory.VidRange{{Low: 5, High: 9}})
	if len(got) != 1 || got[0] != (VidRange{Low: 5, High: 9}) {
		t.Fatalf("toParamVids = %v", got)
	}
}
