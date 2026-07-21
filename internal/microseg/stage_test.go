package microseg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/change"
)

// TestStage_EmitsOnlyFwRuleCreateOps is AC5 (part 1): Stage emits only
// fw.rule.create ops, each targeting the subject's ruleset, carrying the
// proposal's rules faithfully.
func TestStage_EmitsOnlyFwRuleCreateOps(t *testing.T) {
	corpus := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(corpus, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})
	prop := Propose(subj, corpus, profile, Existing{}, DefaultConfig())

	ops := Stage(prop)
	if len(ops) != len(prop.Rules) {
		t.Fatalf("Stage emitted %d ops for %d rules", len(ops), len(prop.Rules))
	}
	for i, op := range ops {
		if op.Type != change.OpFwRuleCreate {
			t.Errorf("op %d: type %q, want fw.rule.create", i, op.Type)
		}
		if op.Target != subj.RulesetRef {
			t.Errorf("op %d: target %s, want %s", i, op.Target, subj.RulesetRef)
		}
		p, ok := op.Params.(*change.FwRuleCreateParams)
		if !ok {
			t.Fatalf("op %d: params type %T, want *FwRuleCreateParams", i, op.Params)
		}
		r := prop.Rules[i]
		if p.Direction != r.Direction || p.Action != r.Action || p.Dport != r.Dport || p.Source != r.Source || p.Dest != r.Dest || p.Pos != r.Pos || !p.Enabled {
			t.Errorf("op %d params %+v do not match rule %+v", i, p, r)
		}
	}
}

// TestStage_OpsMarshalRoundTrip proves the emitted ops are wire-valid changeset
// ops (they decode back through the strict Op decoder) — i.e. Stage produces a
// draft the ordinary change engine accepts, no special path.
func TestStage_OpsMarshalRoundTrip(t *testing.T) {
	corpus := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(corpus, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})
	ops := Stage(Propose(subj, corpus, profile, Existing{}, DefaultConfig()))
	for i, op := range ops {
		data, err := op.MarshalJSON()
		if err != nil {
			t.Fatalf("op %d marshal: %v", i, err)
		}
		var back change.Op
		if err := back.UnmarshalJSON(data); err != nil {
			t.Fatalf("op %d does not round-trip through the strict Op decoder: %v", i, err)
		}
		if back.Type != change.OpFwRuleCreate {
			t.Errorf("op %d decoded as %q", i, back.Type)
		}
	}
}

// TestPackage_NoApplyOrConfirmReference is AC5 (part 2): a static-import-
// boundary check that internal/microseg never references
// change.Service.Apply/Confirm — the planner PROPOSES, it never enforces. It
// parses every non-test source file and asserts no selector expression names
// Apply/Confirm on anything from the change package, and that the package does
// not depend on a change.Service at all.
func TestPackage_NoApplyOrConfirmReference(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	forbidden := map[string]bool{"Apply": true, "Confirm": true, "Service": true}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbidden[sel.Sel.Name] {
				// Allow only if it is unrelated to the change engine. The change
				// package is imported solely for op-construction types; no
				// Apply/Confirm/Service selector should appear at all.
				t.Errorf("%s references a forbidden change-engine method/type %q — the planner must never apply/confirm, only stage a draft", name, sel.Sel.Name)
			}
			return true
		})
	}
}
