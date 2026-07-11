package change_test

// T-407 acceptance criterion 2: Create OVSBridge + OVSBond + a tagged
// OVSIntPort in one changeset -> golden file output -> pvemock apply ->
// committed, driven through the real change engine's stage -> validate ->
// diff -> apply -> confirm -> commit pipeline (never bypassing it, per
// CLAUDE.md's core safety guarantee). The exact rendered-file assertions
// mirror internal/change/ifaces's own golden tests (vlan-create-ovs-04,
// bridge-create-ovs-01, bond-create-ovs-04) — this test's job is proving
// the *whole* multi-op changeset reaches "committed" against a real
// pvemock server, not re-deriving the byte-level stanza format again.
import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestApply_EndToEnd_OVSBridgeBondIntPort(t *testing.T) {
	const node = "pve1"
	bridgeRef := inventory.Ref{Kind: inventory.KindOVSBridge, Node: node, ID: "vmbr9"}
	bondRef := inventory.Ref{Kind: inventory.KindOVSBond, Node: node, ID: "bond9"}
	vlanRef := inventory.Ref{Kind: inventory.KindVlan, Node: node, ID: "vlan99"}

	// The new OVS bond's slave (eno2) must already exist in the inventory
	// snapshot the service validates against — physnics are hardware, never
	// op-created — so this harness needs Inventory wired (see newHarness's
	// and withInventory's doc comments), unlike every other apply_e2e test
	// in this package, whose ops only ever reference freshly-created
	// entities.
	h := newHarness(t, fixtureOVSLab, withInventory(
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: "eno2"}, Name: "eno2"},
	))
	ctx := context.Background()

	// Ordering matters: each op's referential checks run against the
	// changeset's net effect *as of that point* (T-202 AC2), so the bridge
	// must exist before the bond/int-port that attach to it, and both must
	// exist before the bridge.port.add ops that enslave them.
	ops := []change.Op{
		{
			Type: change.OpBridgeCreate, Target: bridgeRef,
			Params: &change.BridgeCreateParams{Comments: "ovs e2e bridge"},
		},
		{
			Type: change.OpBondCreate, Target: bondRef,
			Params: &change.BondCreateParams{Mode: "active-backup", Slaves: []string{"eno2"}, Bridge: "vmbr9"},
		},
		{
			Type: change.OpVlanCreate, Target: vlanRef,
			Params: &change.VlanCreateParams{Parent: "vmbr9", OVS: true, Vid: 99, Addresses: []string{"10.20.99.5/24"}},
		},
		{
			Type: change.OpBridgePortAdd, Target: bridgeRef,
			Params: &change.BridgePortAddParams{Port: "bond9"},
		},
		{
			Type: change.OpBridgePortAdd, Target: bridgeRef,
			Params: &change.BridgePortAddParams{Port: "vlan99"},
		},
	}

	cs := h.mustCreate(t, "root@pam", "add ovs bridge+bond+tagged int port", ops)

	validated, err := h.svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != change.StatusValidated {
		t.Fatalf("status after validate = %s, want validated (findings: %+v)", validated.Status, validated.Findings)
	}

	diff, err := h.svc.Diff(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed {
		t.Fatalf("diff did not show the ovs stack being added: %+v", diff.Files)
	}
	for _, want := range []string{"ovs_type OVSBridge", "ovs_type OVSBond", "ovs_type OVSIntPort", "ovs_options tag=99"} {
		if !strings.Contains(diff.Files[0].Unified, want) {
			t.Errorf("diff missing %q:\n%s", want, diff.Files[0].Unified)
		}
	}

	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}

	log := h.applyLog(t, cs.ID)
	for _, s := range log.Steps {
		if s.Status != change.StepOK {
			t.Fatalf("step %d status = %s, want ok (err: %s)", s.Index, s.Status, s.Error)
		}
	}

	committedFile := h.agent.committedFile(node)
	for _, want := range []string{
		"iface vmbr9 inet manual",
		"ovs_type OVSBridge",
		"ovs_ports bond9 vlan99",
		"iface bond9 inet manual",
		"ovs_bonds eno2",
		"ovs_type OVSBond",
		"ovs_bridge vmbr9",
		"iface vlan99 inet static",
		"address 10.20.99.5/24",
		"ovs_type OVSIntPort",
		"ovs_options tag=99",
	} {
		if !strings.Contains(committedFile, want) {
			t.Errorf("committed file missing %q:\n%s", want, committedFile)
		}
	}

	committed, err := h.svc.Confirm(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if committed.Status != change.StatusCommitted {
		t.Fatalf("status after confirm = %s, want committed", committed.Status)
	}

	got := h.ws.statuses(cs.ID)
	want := []string{"draft", "validated", "applying", "awaiting_confirm", "committed"}
	if !equalStrings(got, want) {
		t.Fatalf("status stream = %v, want %v", got, want)
	}
}
