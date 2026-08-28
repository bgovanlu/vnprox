// SPDX-License-Identifier: Apache-2.0

package spec_test

// delta_test.go asserts the property T-2702's proposal path rests on:
//
//	Import(ApplyOps(Export(live), ops), live) == ops
//
// i.e. rendering a changeset into the document and reading the document back
// out produces the changeset again. That is the round trip; everything else
// here is about the cases where it CANNOT hold, which must be refusals rather
// than silent approximations.

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

func bridgeRef(node, name string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// firstBridge returns a bridge ref that exists in the fixture, so update ops
// in the table below target something real.
func firstBridge(t *testing.T, s spec.Spec) inventory.Ref {
	t.Helper()
	for _, n := range s.Nodes {
		if len(n.Bridges) > 0 {
			return bridgeRef(n.Name, n.Bridges[0].Name)
		}
	}
	t.Fatal("the fixture has no node with a bridge")
	return inventory.Ref{}
}

// TestApplyOps_RoundTripsThroughImport is the central property, run over one
// op of every shape the document can express.
func TestApplyOps_RoundTripsThroughImport(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	live := g.Snapshot()
	base := spec.Export(live)
	target := firstBridge(t, base)
	node := target.Node

	tests := []struct {
		name string
		ops  []change.Op
	}{
		{
			name: "update a bridge's mtu",
			ops: []change.Op{{
				Type: change.OpBridgeUpdate, Target: target,
				Params: &change.BridgeUpdateParams{MTU: intPtr(1400)},
			}},
		},
		{
			name: "update several of a bridge's fields at once",
			ops: []change.Op{{
				Type: change.OpBridgeUpdate, Target: target,
				Params: &change.BridgeUpdateParams{
					MTU: intPtr(9000), Comments: strPtr("storage fabric"), Gateway: strPtr("10.9.0.1"),
				},
			}},
		},
		{
			name: "create a bridge that does not exist",
			ops: []change.Op{{
				Type: change.OpBridgeCreate, Target: bridgeRef(node, "vmbr77"),
				Params: &change.BridgeCreateParams{
					MTU: 1500, Addresses: []string{"10.77.0.1/24"}, VlanAware: true,
					Vids: []change.VidRange{{Low: 2, High: 4094}},
				},
			}},
		},
		{
			name: "add a port to a bridge",
			ops: []change.Op{{
				Type: change.OpBridgePortAdd, Target: target,
				Params: &change.BridgePortAddParams{Port: "eth9"},
			}},
		},
		{
			name: "create a vlan sub-interface",
			ops: []change.Op{{
				Type:   change.OpVlanCreate,
				Target: inventory.Ref{Kind: inventory.KindVlan, Node: node, ID: target.ID + ".77"},
				Params: &change.VlanCreateParams{Parent: target.ID, Vid: 77, Addresses: []string{"10.77.0.2/24"}},
			}},
		},
		{
			name: "two ops on different entities",
			ops: []change.Op{
				{Type: change.OpBridgeUpdate, Target: target, Params: &change.BridgeUpdateParams{MTU: intPtr(1400)}},
				{
					Type: change.OpBridgeCreate, Target: bridgeRef(node, "vmbr78"),
					Params: &change.BridgeCreateParams{MTU: 1500},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The base document describes live exactly, so it plans to
			// nothing: whatever the proposed document plans to IS the delta.
			basePlan, _, err := spec.Import(base, live)
			if err != nil {
				t.Fatalf("importing the base document: %v", err)
			}
			if len(basePlan) != 0 {
				t.Fatalf("control failed: the exported base document plans to %d op(s) against the live state it was exported from; "+
					"the delta below would not be attributable to the ops under test", len(basePlan))
			}

			proposed, err := spec.ApplyOps(base, tc.ops)
			if err != nil {
				t.Fatalf("ApplyOps: %v", err)
			}
			got, _, err := spec.Import(proposed, live)
			if err != nil {
				t.Fatalf("importing the proposed document: %v", err)
			}
			if !reflect.DeepEqual(opSignatures(got), opSignatures(tc.ops)) {
				t.Errorf("the document does not round-trip.\n got: %v\nwant: %v", opSignatures(got), opSignatures(tc.ops))
			}

			// The base document must be untouched: the proposer holds both
			// side by side to render the diff between them.
			if !reflect.DeepEqual(base, spec.Export(live)) {
				t.Error("ApplyOps mutated the document it was given")
			}
		})
	}
}

// opSignatures renders ops as comparable (type, target, params) triples,
// which is what "the same op set" means — the change engine's own op ids are
// not part of it.
func opSignatures(ops []change.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op.Type)+" "+op.Target.String()+" "+paramsString(op))
	}
	return out
}

// paramsString renders an op's params as JSON — their actual VALUES, not
// their type name: an assertion that compared only types would pass for a
// document that round-tripped the right kind of op with the wrong contents.
func paramsString(op change.Op) string {
	if op.Params == nil {
		return "<nil>"
	}
	b, err := json.Marshal(op.Params)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(b)
}

// TestApplyOps_RefusesWhatTheDocumentCannotSay: every op outside the spec's
// vocabulary is a named refusal, never a silent approximation.
func TestApplyOps_RefusesWhatTheDocumentCannotSay(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	base := spec.Export(g.Snapshot())
	target := firstBridge(t, base)

	//nolint:govet // fieldalignment: test table; field order documents each case.
	tests := []struct {
		name string
		op   change.Op
		want error
	}{
		{
			name: "a bridge delete",
			op:   change.Op{Type: change.OpBridgeDelete, Target: target, Params: &change.BridgeDeleteParams{}},
			want: spec.ErrOpNotExpressible,
		},
		{
			name: "a bond delete",
			op: change.Op{
				Type:   change.OpBondDelete,
				Target: inventory.Ref{Kind: inventory.KindBond, Node: target.Node, ID: "bond0"},
				Params: &change.BondDeleteParams{},
			},
			want: spec.ErrOpNotExpressible,
		},
		{
			name: "a firewall rule",
			op: change.Op{
				Type:   change.OpFwRuleCreate,
				Target: inventory.Ref{Kind: inventory.KindNode, ID: target.Node},
				Params: &change.FwRuleCreateParams{},
			},
			want: spec.ErrOpNotExpressible,
		},
		{
			name: "a raw interfaces-file replacement",
			op: change.Op{
				Type:   change.OpIfaceRawReplace,
				Target: inventory.Ref{Kind: inventory.KindNode, ID: target.Node},
				Params: &change.IfaceRawReplaceParams{},
			},
			want: spec.ErrOpNotExpressible,
		},
		{
			name: "an update to a bridge the document does not declare",
			op: change.Op{
				Type:   change.OpBridgeUpdate,
				Target: bridgeRef(target.Node, "vmbr-not-in-the-spec"),
				Params: &change.BridgeUpdateParams{MTU: intPtr(1400)},
			},
			want: spec.ErrTargetNotInSpec,
		},
		{
			name: "a create that would duplicate a declared bridge",
			op: change.Op{
				Type: change.OpBridgeCreate, Target: target,
				Params: &change.BridgeCreateParams{MTU: 1500},
			},
			want: nil, // a plain error, not a sentinel — asserted below
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := spec.ApplyOps(base, []change.Op{tc.op})
			if err == nil {
				t.Fatalf("ApplyOps accepted %s", tc.op.Type)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("ApplyOps = %v, want %v", err, tc.want)
			}
			// A refusal that does not say WHICH op is unusable in a UI.
			if tc.op.Target.ID != "" && !strings.Contains(err.Error(), tc.op.Target.ID) {
				t.Errorf("the refusal does not name the target: %v", err)
			}
		})
	}

	// --- control: the same call with an expressible op succeeds -----------
	if _, err := spec.ApplyOps(base, []change.Op{{
		Type: change.OpBridgeUpdate, Target: target, Params: &change.BridgeUpdateParams{MTU: intPtr(1400)},
	}}); err != nil {
		t.Fatalf("control failed: ApplyOps refuses even an expressible op (%v), so the refusals above prove nothing", err)
	}
}
