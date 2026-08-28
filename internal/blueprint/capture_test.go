// SPDX-License-Identifier: Apache-2.0

package blueprint_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
)

// T-603 AC3: capturing one node's network and re-instantiating the result
// on a bare node produces an equivalent network at the inventory level —
// i.e. instantiating the captured blueprint against a bare fixture (a
// different node, nothing yet configured) produces create ops whose
// fields exactly mirror the captured node's own declared config, modulo
// only the node identity itself.
func TestCapture_RoundTrip_EquivalentNetwork(t *testing.T) {
	src := newGraphWithNodes("pve1", "pve2", "pve3")
	applyBond(src, "pve1", "bond0", bondOpts{mode: "802.3ad", slaves: []string{"eno1", "eno2"}})
	applyBridge(src, "pve1", "vmbr0", bridgeOpts{
		ports: []string{"bond0"}, addresses: []string{"192.168.1.11/24"}, comments: "mgmt",
	})
	applyVlan(src, "pve1", "bond0.30", "bond0", 30, []string{"10.30.0.11/24"}, 0)

	bp, err := blueprint.Capture(src.Snapshot(), "pve1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// Capture leaves ID/BlueprintVersion unset — Service.Save is what
	// assigns those (a ULID id, the current format version) before its
	// own Validate call; mirror that here before validating standalone.
	bp.ID = "captured-test"
	bp.BlueprintVersion = blueprint.CurrentBlueprintVersion
	if validateErr := blueprint.Validate(bp); validateErr != nil {
		t.Fatalf("captured blueprint failed Validate: %v", validateErr)
	}

	// Re-instantiate targeting "pve2" — a different, unconfigured node in
	// the same cluster graph — using the captured blueprint's own
	// defaults (the captured literal addresses) unchanged. Reusing src's
	// own snapshot is deliberate: Instantiate's Nodes restricts which
	// nodes SelectAll *expands onto*, not which nodes' existing state it
	// diffs against, so pve2 (bare) is exactly the "bare node" AC3 asks
	// for even though pve1's already-captured entities remain in the same
	// snapshot.
	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: []string{"pve2"}}, src.Snapshot())
	if err != nil {
		t.Fatalf("Instantiate (round-trip): %v", err)
	}

	byType := map[change.OpType][]change.Op{}
	for _, op := range ops {
		byType[op.Type] = append(byType[op.Type], op)
	}
	if len(byType[change.OpBondCreate]) != 1 {
		t.Fatalf("got %d bond.create ops, want 1 (ops: %v)", len(byType[change.OpBondCreate]), opTypes(ops))
	}
	if len(byType[change.OpBridgeCreate]) != 1 {
		t.Fatalf("got %d bridge.create ops, want 1 (ops: %v)", len(byType[change.OpBridgeCreate]), opTypes(ops))
	}
	if len(byType[change.OpVlanCreate]) != 1 {
		t.Fatalf("got %d vlan.create ops, want 1 (ops: %v)", len(byType[change.OpVlanCreate]), opTypes(ops))
	}

	bondOp := byType[change.OpBondCreate][0]
	if bondOp.Target.Node != "pve2" || bondOp.Target.ID != "bond0" {
		t.Fatalf("bond target = %s, want bond:pve2:bond0", bondOp.Target)
	}
	bondParams := bondOp.Params.(*change.BondCreateParams)
	if bondParams.Mode != "802.3ad" || !sameStringSet(bondParams.Slaves, []string{"eno1", "eno2"}) {
		t.Fatalf("unexpected bond params: %+v", bondParams)
	}

	bridgeOp := byType[change.OpBridgeCreate][0]
	if bridgeOp.Target.Node != "pve2" || bridgeOp.Target.ID != "vmbr0" {
		t.Fatalf("bridge target = %s, want bridge:pve2:vmbr0", bridgeOp.Target)
	}
	bridgeParams := bridgeOp.Params.(*change.BridgeCreateParams)
	if !sameStringSet(bridgeParams.Ports, []string{"bond0"}) || bridgeParams.Comments != "mgmt" {
		t.Fatalf("unexpected bridge params: %+v", bridgeParams)
	}
	if len(bridgeParams.Addresses) != 1 || bridgeParams.Addresses[0] != "192.168.1.11/24" {
		t.Fatalf("bridge addresses = %v, want the captured address preserved by default", bridgeParams.Addresses)
	}

	vlanOp := byType[change.OpVlanCreate][0]
	if vlanOp.Target.Node != "pve2" || vlanOp.Target.ID != "bond0.30" {
		t.Fatalf("vlan target = %s, want vlan:pve2:bond0.30", vlanOp.Target)
	}
	vlanParams := vlanOp.Params.(*change.VlanCreateParams)
	if vlanParams.Parent != "bond0" || vlanParams.Vid != 30 {
		t.Fatalf("unexpected vlan params: %+v", vlanParams)
	}
	if len(vlanParams.Addresses) != 1 || vlanParams.Addresses[0] != "10.30.0.11/24" {
		t.Fatalf("vlan addresses = %v, want the captured address preserved by default", vlanParams.Addresses)
	}
}

// sameStringSet is a small unordered-set equality check for this external
// test package (fieldutil.go's setEqual is unexported inside package
// blueprint, not reachable from blueprint_test).
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[string]int{}
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
	}
	for _, c := range count {
		if c != 0 {
			return false
		}
	}
	return true
}

func TestCapture_NoEntities_ReturnsNotFound(t *testing.T) {
	g := newGraphWithNodes("pve1")
	_, err := blueprint.Capture(g.Snapshot(), "pve1")
	if err == nil {
		t.Fatal("expected an error capturing a node with no entities")
	}
}
