package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func rawNodeTarget(node string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindNode, Node: node, ID: node}
}

const rawBaseFile = "auto lo\niface lo inet loopback\n\n" +
	"auto vmbr0\niface vmbr0 inet static\n\taddress 10.10.0.1/24\n\tbridge-ports eno1\n"

func TestExpandRawReplace_ParseError(t *testing.T) {
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), rawBaseFile, "iface eth0 inet static\n\taddress 10.0.0.1\\")
	if ops != nil {
		t.Errorf("ops = %+v, want nil on parse error", ops)
	}
	if len(findings) != 1 || findings[0].Code != codeRawReplaceParseError || findings[0].Severity != SeverityError {
		t.Fatalf("findings = %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "line") {
		t.Errorf("Message = %q, want it to mention a line number", findings[0].Message)
	}
}

func TestExpandRawReplace_BridgeDelete(t *testing.T) {
	after := "auto lo\niface lo inet loopback\n"
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), rawBaseFile, after)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
	if len(ops) != 1 || ops[0].Type != OpBridgeDelete {
		t.Fatalf("ops = %+v, want a single bridge.delete", ops)
	}
	want := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	if ops[0].Target != want {
		t.Errorf("Target = %+v, want %+v", ops[0].Target, want)
	}
}

func TestExpandRawReplace_BridgeCreate(t *testing.T) {
	before := "auto lo\niface lo inet loopback\n"
	after := "auto lo\niface lo inet loopback\n\n" +
		"auto vmbr1\niface vmbr1 inet manual\n\tbridge-ports eno2\n\tbridge-vlan-aware yes\n"
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), before, after)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	if len(ops) != 1 || ops[0].Type != OpBridgeCreate {
		t.Fatalf("ops = %+v, want a single bridge.create", ops)
	}
	p, ok := ops[0].Params.(*BridgeCreateParams)
	if !ok {
		t.Fatalf("Params = %T", ops[0].Params)
	}
	if len(p.Ports) != 1 || p.Ports[0] != "eno2" {
		t.Errorf("Ports = %v, want [eno2]", p.Ports)
	}
	if !p.VlanAware {
		t.Errorf("VlanAware = false, want true")
	}
}

func TestExpandRawReplace_BridgeUpdate_MTUAndPorts(t *testing.T) {
	after := "auto lo\niface lo inet loopback\n\n" +
		"auto vmbr0\niface vmbr0 inet static\n\taddress 10.10.0.1/24\n\tbridge-ports eno1 eno2\n\tmtu 9000\n"
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), rawBaseFile, after)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}

	var gotAdd, gotUpdate bool
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *BridgePortAddParams:
			if p.Port == "eno2" {
				gotAdd = true
			}
		case *BridgeUpdateParams:
			if p.MTU != nil && *p.MTU == 9000 {
				gotUpdate = true
			}
		}
	}
	if !gotAdd {
		t.Errorf("expected a bridge.port.add for eno2, got ops %+v", ops)
	}
	if !gotUpdate {
		t.Errorf("expected a bridge.update with mtu=9000, got ops %+v", ops)
	}
}

func TestExpandRawReplace_VlanCreateAndBondCreate(t *testing.T) {
	before := "auto lo\niface lo inet loopback\n"
	after := "auto lo\niface lo inet loopback\n\n" +
		"auto bond0\niface bond0 inet manual\n\tbond-slaves eno1 eno2\n\tbond-mode 802.3ad\n\n" +
		"auto bond0.20\niface bond0.20 inet static\n\taddress 10.20.0.5/24\n\tvlan-raw-device bond0\n"
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), before, after)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}

	var gotBond, gotVlan bool
	for _, op := range ops {
		switch op.Type {
		case OpBondCreate:
			gotBond = true
			p := op.Params.(*BondCreateParams) //nolint:errcheck // asserted by op.Type
			if p.Mode != "802.3ad" || len(p.Slaves) != 2 {
				t.Errorf("BondCreateParams = %+v", p)
			}
		case OpVlanCreate:
			gotVlan = true
			p := op.Params.(*VlanCreateParams) //nolint:errcheck // asserted by op.Type
			if p.Parent != "bond0" || p.Vid != 20 {
				t.Errorf("VlanCreateParams = %+v", p)
			}
		}
	}
	if !gotBond || !gotVlan {
		t.Fatalf("ops = %+v, want a bond.create and a vlan.create", ops)
	}
}

func TestExpandRawReplace_PhysNicIgnoredUnlessMTUChanges(t *testing.T) {
	before := "auto lo\niface lo inet loopback\n\nauto eno1\niface eno1 inet manual\n"
	after := "auto lo\niface lo inet loopback\n"
	if ops, findings := expandRawReplace(rawNodeTarget("pve1"), before, after); len(ops) != 0 || len(findings) != 0 {
		t.Errorf("removing a bare physnic stanza should synthesize nothing, got ops=%+v findings=%+v", ops, findings)
	}

	after2 := "auto lo\niface lo inet loopback\n\nauto eno1\niface eno1 inet manual\n\tmtu 9000\n"
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), before, after2)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	if len(ops) != 1 || ops[0].Type != OpIfaceUpdate {
		t.Fatalf("ops = %+v, want a single iface.update for the MTU change", ops)
	}
}

func TestExpandRawReplace_NoChangeYieldsNoOps(t *testing.T) {
	ops, findings := expandRawReplace(rawNodeTarget("pve1"), rawBaseFile, rawBaseFile)
	if len(ops) != 0 || len(findings) != 0 {
		t.Errorf("identical before/after should synthesize nothing, got ops=%+v findings=%+v", ops, findings)
	}
}
