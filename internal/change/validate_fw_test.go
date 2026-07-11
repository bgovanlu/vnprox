package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// clusterFwTarget is the cluster-scope firewall ruleset Ref every fw.alias/
// ipset/group op targets (params_fw.go's documented convention).
func clusterFwTarget() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}
}

// nineRulesReferencing builds nine rules that reference name via their
// Source field (an alias reference, per fw.ruleReferences) — acceptance
// criterion 2's literal "referenced by 9 fixture rules".
func nineRulesReferencing(name string) []inventory.FwRule {
	rules := make([]inventory.FwRule, 9)
	for i := range rules {
		rules[i] = inventory.FwRule{Pos: i, Enabled: true, Direction: "in", Action: "ACCEPT", Source: name}
	}
	return rules
}

// TestValidate_FwAliasDelete_BlockedWhenReferenced is T-502 acceptance
// criterion 2: deleting an alias referenced by 9 rules is blocked with the
// exact reference count (the reference list itself, and its UI deep-links,
// are internal/fw.UsageCounts' ReferencedBy — already exhaustively tested
// in internal/fw's own suite; this test proves the *validator* wires that
// data into a blocking Finding).
func TestValidate_FwAliasDelete_BlockedWhenReferenced(t *testing.T) {
	snap := buildSnapshot(&inventory.FwRuleset{
		Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "office", CIDR: "203.0.113.0/24"}},
		Rules:   nineRulesReferencing("office"),
	})
	op := mkOp(OpFwAliasDelete, clusterFwTarget(), &FwAliasDeleteParams{Name: "office"})
	findings := Validate([]Op{op}, snap)
	if !hasError(findings) {
		t.Fatalf("expected a blocking finding, got: %+v", findings)
	}
	found := false
	for _, f := range findings {
		if f.Code == codeFwObjectInUse {
			found = true
			if !containsAll(f.Message, "9", "office") {
				t.Errorf("finding message = %q, want it to mention the alias name and the count 9", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("no %s finding among: %+v", codeFwObjectInUse, findings)
	}
}

// TestValidate_FwAliasDelete_AllowedWhenUnreferenced proves the guard is
// specific to *actual* usage: an alias nobody references deletes clean.
func TestValidate_FwAliasDelete_AllowedWhenUnreferenced(t *testing.T) {
	snap := buildSnapshot(&inventory.FwRuleset{
		Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "unused", CIDR: "203.0.113.0/24"}},
	})
	op := mkOp(OpFwAliasDelete, clusterFwTarget(), &FwAliasDeleteParams{Name: "unused"})
	findings := Validate([]Op{op}, snap)
	if hasError(findings) {
		t.Fatalf("unexpected error findings for deleting an unreferenced alias: %+v", findings)
	}
}

// TestValidate_FwAliasDelete_NotFound proves the guard also closes the
// pre-T-502 "no existence check for delete" gap (validate_referential.go's
// doc comment on the FwAliasUpdateParams/etc. case): deleting a name
// nothing created is itself an error, not a silent no-op.
func TestValidate_FwAliasDelete_NotFound(t *testing.T) {
	snap := buildSnapshot(&inventory.FwRuleset{Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true})
	op := mkOp(OpFwAliasDelete, clusterFwTarget(), &FwAliasDeleteParams{Name: "ghost"})
	findings := Validate([]Op{op}, snap)
	if !hasErrorCode(findings, codeFwObjectNotFound) {
		t.Fatalf("expected %s, got: %+v", codeFwObjectNotFound, findings)
	}
}

func TestValidate_FwIpsetDelete_BlockedWhenReferenced(t *testing.T) {
	rules := make([]inventory.FwRule, 3)
	for i := range rules {
		rules[i] = inventory.FwRule{Pos: i, Enabled: true, Direction: "in", Action: "ACCEPT", Source: "+blocklist"}
	}
	snap := buildSnapshot(&inventory.FwRuleset{
		Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true,
		IPSets: []inventory.FwIPSet{{Name: "blocklist"}},
		Rules:  rules,
	})
	op := mkOp(OpFwIpsetDelete, clusterFwTarget(), &FwIpsetDeleteParams{Name: "blocklist"})
	findings := Validate([]Op{op}, snap)
	if !hasErrorCode(findings, codeFwObjectInUse) {
		t.Fatalf("expected %s, got: %+v", codeFwObjectInUse, findings)
	}
}

func TestValidate_FwGroupDelete_BlockedWhenReferenced(t *testing.T) {
	snap := buildSnapshot(&inventory.FwRuleset{
		Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true,
		Groups: []inventory.FwGroup{{Name: "web"}},
		Rules:  []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "group", Action: "web"}},
	})
	op := mkOp(OpFwGroupDelete, clusterFwTarget(), &FwGroupDeleteParams{Name: "web"})
	findings := Validate([]Op{op}, snap)
	if !hasErrorCode(findings, codeFwObjectInUse) {
		t.Fatalf("expected %s, got: %+v", codeFwObjectInUse, findings)
	}
}

// TestValidate_FwRuleMacro is the task card's "macro existence" validator.
func TestValidate_FwRuleMacro(t *testing.T) {
	snap := buildSnapshot(&inventory.FwRuleset{Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true})

	t.Run("known macro validates clean", func(t *testing.T) {
		op := mkOp(OpFwRuleCreate, clusterFwTarget(), &FwRuleCreateParams{Direction: "in", Action: "ACCEPT", Macro: "HTTP", Pos: 0, Enabled: true})
		findings := Validate([]Op{op}, snap)
		if hasError(findings) {
			t.Fatalf("unexpected error findings for a known macro: %+v", findings)
		}
	})

	t.Run("unknown macro is rejected", func(t *testing.T) {
		op := mkOp(OpFwRuleCreate, clusterFwTarget(), &FwRuleCreateParams{Direction: "in", Action: "ACCEPT", Macro: "NOT-A-REAL-MACRO", Pos: 0, Enabled: true})
		findings := Validate([]Op{op}, snap)
		if !hasErrorCode(findings, codeFwMacroUnknown) {
			t.Fatalf("expected %s, got: %+v", codeFwMacroUnknown, findings)
		}
	})
}

// TestValidate_FwRule_GroupReferenceAction proves a group-reference rule's
// Action (the referenced group's name) is not rejected by the ACCEPT/DROP/
// REJECT enum check that applies to every other rule.
func TestValidate_FwRule_GroupReferenceAction(t *testing.T) {
	snap := buildSnapshot(&inventory.FwRuleset{Ref: clusterFwTarget(), Scope: inventory.FwScopeCluster, Enabled: true})
	op := mkOp(OpFwRuleCreate, clusterFwTarget(), &FwRuleCreateParams{Direction: "group", Action: "base-services", Pos: 0, Enabled: true})
	findings := Validate([]Op{op}, snap)
	if hasError(findings) {
		t.Fatalf("unexpected error findings for a group-reference rule: %+v", findings)
	}
}

func hasErrorCode(findings []Finding, code string) bool {
	for _, f := range findings {
		if f.Severity == SeverityError && f.Code == code {
			return true
		}
	}
	return false
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
