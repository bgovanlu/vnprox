// SPDX-License-Identifier: Apache-2.0

package explain

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// --- the coverage gate -------------------------------------------------------

// TestExplainOp_EveryKnownOpTypeIsCovered is the diff-explainer's version of
// findings_test.go's TestCoverage_AllCatalogChecksHaveExplainerOrExemption:
// every op type change.KnownOpTypes() can decode today must decompose into a
// verb+noun ExplainOp can render. Mirrors internal/change/preview.go's own
// TestPreview_EveryOpTypeIsProjectedOrDisclosed for the same vocabulary.
func TestExplainOp_EveryKnownOpTypeIsCovered(t *testing.T) {
	for _, opType := range change.KnownOpTypes() {
		_, _, ok := decomposeOpType(opType)
		if !ok {
			t.Errorf("op type %s does not decompose into a known verb+noun — add its verb to opVerbs and/or its object prefix to opNouns", opType)
		}
	}
}

// TestDecomposeOpType_UnregisteredTypeFailsTheGate is the task card's "a
// [type] with no template must fail the gate" case, applied to the op
// vocabulary: an op type this package has never heard of must not silently
// decompose into something plausible-looking.
func TestDecomposeOpType_UnregisteredTypeFailsTheGate(t *testing.T) {
	cases := []change.OpType{
		"totally.unregistered",
		"bond.frobnicate",      // known noun, unknown verb
		"unknownobject.create", // known verb, unknown noun
		"noverbatall",
	}
	for _, opType := range cases {
		if _, _, ok := decomposeOpType(opType); ok {
			t.Errorf("decomposeOpType(%s) = ok, want a failure for an unregistered op type", opType)
		}
	}
}

// --- rendering ----------------------------------------------------------------

// TestExplainOp_EveryKnownOpTypeRendersWithoutPanickingOnAbsentParams renders
// every known op type with a zero-value Target and nil Params — the "absent
// optional fields" case the task card calls out — and requires a non-empty
// Summary with no panic.
func TestExplainOp_EveryKnownOpTypeRendersWithoutPanickingOnAbsentParams(t *testing.T) {
	for _, opType := range change.KnownOpTypes() {
		t.Run(string(opType), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ExplainOp panicked on %s with nil Params: %v", opType, r)
				}
			}()
			got := ExplainOp(change.Op{Type: opType})
			if got.Summary == "" {
				t.Error("Summary is empty")
			}
			if got.OpType != string(opType) {
				t.Errorf("OpType = %q, want %q", got.OpType, opType)
			}
		})
	}
}

func TestExplainOp_TargetIsRenderedAndNamed(t *testing.T) {
	op := change.Op{
		ID:     "op-1",
		Type:   change.OpBridgeUpdate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
	}
	got := ExplainOp(op)
	if got.OpID != "op-1" {
		t.Errorf("OpID = %q, want op-1", got.OpID)
	}
	if got.Target != "bridge:pve1:vmbr0" {
		t.Errorf("Target = %q, want bridge:pve1:vmbr0", got.Target)
	}
	if !strings.Contains(got.Summary, "vmbr0") {
		t.Errorf("Summary %q does not name the target's id", got.Summary)
	}
	if !strings.Contains(got.Summary, "pve1") {
		t.Errorf("Summary %q does not name the target's node", got.Summary)
	}
	if !strings.HasPrefix(got.Summary, "Updates") {
		t.Errorf("Summary %q does not open with the update verb", got.Summary)
	}
}

// TestExplainOp_NoTargetOpRendersWithoutATarget covers sdn.apply, the sole
// op with no natural target entity (op.go's noTargetOps) — Target must be
// empty, and the summary must still render.
func TestExplainOp_NoTargetOpRendersWithoutATarget(t *testing.T) {
	got := ExplainOp(change.Op{Type: change.OpSdnApply})
	if got.Target != "" {
		t.Errorf("Target = %q, want empty for a no-target op", got.Target)
	}
	if got.Summary == "" {
		t.Error("Summary is empty")
	}
	if !strings.HasPrefix(got.Summary, "Applies") {
		t.Errorf("Summary %q does not open with the apply verb", got.Summary)
	}
}

func TestExplainOp_UnregisteredOpTypeRendersHonestFallback(t *testing.T) {
	got := ExplainOp(change.Op{Type: "totally.unregistered"})
	if !strings.Contains(got.Summary, "totally.unregistered") {
		t.Errorf("Summary %q does not name the unregistered op type", got.Summary)
	}
}

// --- enrichment from typed Params, not from a rendered string --------------

func TestExplainOp_EnrichesFromTypedParams(t *testing.T) {
	cases := []struct {
		name   string
		op     change.Op
		wantIn []string
	}{
		{
			name: "iface rename",
			op: change.Op{
				Type:   change.OpIfaceRename,
				Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
				Params: &change.IfaceRenameParams{NewName: "vmbr9"},
			},
			wantIn: []string{"vmbr0", "vmbr9"},
		},
		{
			name: "bridge port add",
			op: change.Op{
				Type:   change.OpBridgePortAdd,
				Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
				Params: &change.BridgePortAddParams{Port: "eth1"},
			},
			wantIn: []string{"vmbr0", "eth1"},
		},
		{
			name: "fw rule move",
			op: change.Op{
				Type:   change.OpFwRuleMove,
				Target: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"},
				Params: &change.FwRuleMoveParams{FromPos: 3, ToPos: 0},
			},
			wantIn: []string{"3", "0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplainOp(tc.op)
			for _, want := range tc.wantIn {
				if !strings.Contains(got.Summary, want) {
					t.Errorf("Summary %q does not contain %q", got.Summary, want)
				}
			}
		})
	}
}

// --- ExplainChangeset ---------------------------------------------------------

func TestExplainChangeset_PreservesOrderAndLength(t *testing.T) {
	ops := []change.Op{
		{ID: "1", Type: change.OpBondCreate, Target: inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}},
		{ID: "2", Type: change.OpBridgeDelete, Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}},
	}
	got := ExplainChangeset(ops)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].OpID != "1" || got[1].OpID != "2" {
		t.Errorf("order not preserved: got %q, %q", got[0].OpID, got[1].OpID)
	}
}

func TestExplainChangeset_EmptyChangesetRendersNoOps(t *testing.T) {
	got := ExplainChangeset(nil)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
