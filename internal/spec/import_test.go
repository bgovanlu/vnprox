// SPDX-License-Identifier: Apache-2.0

package spec_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

func opsDump(ops []change.Op) string {
	var b strings.Builder
	for _, o := range ops {
		fmt.Fprintf(&b, "  %s %s\n", o.Type, o.Target)
	}
	return b.String()
}

// AC3: a hand-edited spec (a new VLAN bridge added; a diverging MTU on an
// existing bridge) imported against three-node-vlan produces exactly the
// expected create/update ops — never a delete, never an apply. The resulting
// changeset (built by the caller from these ops) is a draft, verified here at
// the op level (the API-layer test in internal/api asserts the draft status
// end to end).
func TestImport_HandEdited_ExpectedOps(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	snap := g.Snapshot()

	s := spec.Export(snap)
	if len(s.Nodes) == 0 {
		t.Fatal("three-node-vlan produced no nodes in spec")
	}

	// Pick a real node with at least one existing bridge to mutate.
	nodeIdx, brIdx := -1, -1
	for ni := range s.Nodes {
		if len(s.Nodes[ni].Bridges) > 0 {
			nodeIdx, brIdx = ni, 0
			break
		}
	}
	if nodeIdx < 0 {
		t.Fatal("no node with an existing bridge to diverge")
	}
	node := s.Nodes[nodeIdx].Name
	existingBridge := s.Nodes[nodeIdx].Bridges[brIdx].Name

	// Edit 1: diverge the MTU of an existing bridge (expect one bridge.update).
	s.Nodes[nodeIdx].Bridges[brIdx].MTU = 1400

	// Edit 2: add a brand-new VLAN-aware bridge (expect one bridge.create).
	const newBridge = "vmbr-spec-new"
	s.Nodes[nodeIdx].Bridges = append(s.Nodes[nodeIdx].Bridges, spec.BridgeSpec{
		Name: newBridge, VlanAware: true, MTU: 1500,
	})

	ops, notInSpec, err := spec.Import(s, snap)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// No prune: the two edits touch only these two bridges; nothing is
	// removed from the spec, so notInSpec stays empty.
	if len(notInSpec) != 0 {
		t.Errorf("notInSpec = %v, want empty", notInSpec)
	}

	// Exactly one update (the MTU divergence) and one create (the new bridge).
	var updates, creates []change.Op
	for _, o := range ops {
		switch o.Type {
		case change.OpBridgeUpdate:
			updates = append(updates, o)
		case change.OpBridgeCreate:
			creates = append(creates, o)
		case change.OpBridgeDelete, change.OpBondDelete, change.OpVlanDelete,
			change.OpSdnZoneDelete, change.OpSdnVnetDelete, change.OpSdnSubnetDelete:
			t.Errorf("import produced a delete op %s (%s) — never allowed", o.Type, o.Target)
		default:
			t.Errorf("unexpected op %s (%s):\n%s", o.Type, o.Target, opsDump(ops))
		}
	}

	if len(updates) != 1 {
		t.Fatalf("got %d bridge.update ops, want 1:\n%s", len(updates), opsDump(ops))
	}
	updRef := inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: existingBridge}
	if updates[0].Target != updRef {
		t.Errorf("update target = %s, want %s", updates[0].Target, updRef)
	}
	up, ok := updates[0].Params.(*change.BridgeUpdateParams)
	if !ok {
		t.Fatalf("update params type = %T", updates[0].Params)
	}
	if up.MTU == nil || *up.MTU != 1400 {
		t.Errorf("update MTU = %v, want 1400", up.MTU)
	}

	if len(creates) != 1 {
		t.Fatalf("got %d bridge.create ops, want 1:\n%s", len(creates), opsDump(ops))
	}
	createRef := inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: newBridge}
	if creates[0].Target != createRef {
		t.Errorf("create target = %s, want %s", creates[0].Target, createRef)
	}
	cp, ok := creates[0].Params.(*change.BridgeCreateParams)
	if !ok {
		t.Fatalf("create params type = %T", creates[0].Params)
	}
	if !cp.VlanAware {
		t.Errorf("new bridge should be vlanAware")
	}
}

// Parse rejects a document whose specVersion this daemon does not understand
// (no silent reconcile against an unknown schema).
func TestParse_RejectsUnknownVersion(t *testing.T) {
	for _, doc := range []string{
		"specVersion: 2\n",
		"nodes: []\n", // no specVersion at all (0)
	} {
		if _, err := spec.Parse([]byte(doc)); err == nil {
			t.Errorf("Parse(%q) = nil error, want rejection", doc)
		}
	}
}

// Import is version-checked too, so an in-process caller that skips Parse
// still can't reconcile against an unknown-version Spec.
func TestImport_RejectsUnknownVersion(t *testing.T) {
	if _, _, err := spec.Import(spec.Spec{SpecVersion: 99}, inventory.NewGraph().Snapshot()); err == nil {
		t.Error("Import with specVersion 99 = nil error, want rejection")
	}
}
