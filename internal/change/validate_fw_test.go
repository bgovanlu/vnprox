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

// vnetFwTarget is the vnet-scope firewall ruleset Ref fw.rule.*/
// fw.options.update ops targeting a vnet's forward chain use (T-3103's "vnet/
// <zone>/<vnet>" ID convention, params_fw.go's doc comment).
func vnetFwTarget() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, ID: "vnet/zone1/vnet1"}
}

func guestFwTarget() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}
}

// TestValidate_FwRuleForward_AcceptedAtClusterNodeAndVNetScope is T-3103
// acceptance criterion 1: "forward" is accepted at every scope real PVE
// accepts it at (cluster/node/vnet — hardware-captured), and item 1's own
// "it looks like a one-line fix and it is not" warning: the resolver must
// actually understand the direction, not merely let it pass schema.
func TestValidate_FwRuleForward_AcceptedAtClusterNodeAndVNetScope(t *testing.T) {
	for _, target := range []inventory.Ref{
		clusterFwTarget(),
		{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "node"},
		vnetFwTarget(),
	} {
		snap := buildSnapshot(&inventory.FwRuleset{Ref: target, Scope: inventory.FwScopeCluster, Enabled: true})
		op := mkOp(OpFwRuleCreate, target, &FwRuleCreateParams{Direction: "forward", Action: "ACCEPT", Pos: 0, Enabled: true})
		findings := Validate([]Op{op}, snap)
		if hasError(findings) {
			t.Errorf("target %s: unexpected error findings for a forward-direction rule: %+v", target, findings)
		}
	}
}

// TestValidate_FwRuleForward_RejectedAtGuestScope is the other half of item
// 1's warning: a "forward" rule admitted at guest scope would resolve down
// the inbound/outbound path in internal/fw's resolver (which never matches
// direction=="forward" against dir=="in"/"out") and silently vanish from
// every verdict — worse than an honest rejection. Real PVE's own capture
// only confirmed cluster/node/vnet scope; guest scope stays rejected until
// that changes (needs hardware validation, per this task's report).
func TestValidate_FwRuleForward_RejectedAtGuestScope(t *testing.T) {
	target := guestFwTarget()
	snap := buildSnapshot(&inventory.FwRuleset{Ref: target, Scope: inventory.FwScopeGuest, Enabled: true})
	op := mkOp(OpFwRuleCreate, target, &FwRuleCreateParams{Direction: "forward", Action: "ACCEPT", Pos: 0, Enabled: true})
	findings := Validate([]Op{op}, snap)
	if !hasErrorCode(findings, codeFwScopeInvalid) {
		t.Fatalf("expected %s for a forward-direction rule at guest scope, got: %+v", codeFwScopeInvalid, findings)
	}
}

// TestValidate_FwRuleUpdateForward_RejectedAtGuestScope is the fw.rule.update
// counterpart — the same guard must fire on the partial-update path, not
// only fw.rule.create.
func TestValidate_FwRuleUpdateForward_RejectedAtGuestScope(t *testing.T) {
	target := guestFwTarget()
	snap := buildSnapshot(&inventory.FwRuleset{
		Ref: target, Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT"}},
	})
	forward := "forward"
	op := mkOp(OpFwRuleUpdate, target, &FwRuleUpdateParams{Direction: &forward, Pos: 0})
	findings := Validate([]Op{op}, snap)
	if !hasErrorCode(findings, codeFwScopeInvalid) {
		t.Fatalf("expected %s for a fw.rule.update setting direction=forward at guest scope, got: %+v", codeFwScopeInvalid, findings)
	}
}

// TestValidate_FwOptionsVNetScope_RejectsDefaultInOut is T-3103's other
// scope-mismatch guard: real PVE's vnet-scope firewall options endpoint has
// no policy_in/policy_out fields at all (hardware-captured) — an
// fw.options.update setting them against a vnet target must be rejected,
// not silently written to a field that doesn't exist on the real API.
func TestValidate_FwOptionsVNetScope_RejectsDefaultInOut(t *testing.T) {
	target := vnetFwTarget()
	snap := buildSnapshot(&inventory.FwRuleset{Ref: target, Scope: inventory.FwScopeVNet, Enabled: true})
	accept := "ACCEPT"
	op := mkOp(OpFwOptionsUpdate, target, &FwOptionsUpdateParams{DefaultIn: &accept, DefaultOut: &accept})
	findings := Validate([]Op{op}, snap)
	if !hasErrorCode(findings, codeFwScopeInvalid) {
		t.Fatalf("expected %s for defaultIn/defaultOut at vnet scope, got: %+v", codeFwScopeInvalid, findings)
	}
}

// TestValidate_FwOptionsVNetScope_AcceptsDefaultForward is the positive
// case: policy_forward is exactly what vnet scope's options endpoint does
// carry (hardware-captured), and REJECT is not a valid forward policy
// (unlike defaultIn/defaultOut's ACCEPT|DROP|REJECT).
func TestValidate_FwOptionsVNetScope_AcceptsDefaultForward(t *testing.T) {
	target := vnetFwTarget()
	snap := buildSnapshot(&inventory.FwRuleset{Ref: target, Scope: inventory.FwScopeVNet, Enabled: true})

	t.Run("ACCEPT is valid", func(t *testing.T) {
		accept := "ACCEPT"
		op := mkOp(OpFwOptionsUpdate, target, &FwOptionsUpdateParams{DefaultForward: &accept})
		findings := Validate([]Op{op}, snap)
		if hasError(findings) {
			t.Errorf("unexpected error findings for defaultForward=ACCEPT at vnet scope: %+v", findings)
		}
	})

	t.Run("REJECT is not a valid forward policy", func(t *testing.T) {
		reject := "REJECT"
		op := mkOp(OpFwOptionsUpdate, target, &FwOptionsUpdateParams{DefaultForward: &reject})
		findings := Validate([]Op{op}, snap)
		if !hasErrorCode(findings, codeFwPolicyInvalid) {
			t.Fatalf("expected %s for defaultForward=REJECT, got: %+v", codeFwPolicyInvalid, findings)
		}
	})
}

// TestValidate_FwOptionsLogLevelForward_OnlyValidAtVNetScope is the
// log_level_forward half of the same guard: hardware-confirmed only at
// vnet scope (see validFwLogLevelsForward's doc comment), so it is rejected
// everywhere else rather than guessed at.
func TestValidate_FwOptionsLogLevelForward_OnlyValidAtVNetScope(t *testing.T) {
	debug := "debug"

	t.Run("accepted at vnet scope", func(t *testing.T) {
		target := vnetFwTarget()
		snap := buildSnapshot(&inventory.FwRuleset{Ref: target, Scope: inventory.FwScopeVNet, Enabled: true})
		op := mkOp(OpFwOptionsUpdate, target, &FwOptionsUpdateParams{LogLevelForward: &debug})
		findings := Validate([]Op{op}, snap)
		if hasError(findings) {
			t.Errorf("unexpected error findings for logLevelForward at vnet scope: %+v", findings)
		}
	})

	t.Run("rejected at cluster scope", func(t *testing.T) {
		target := clusterFwTarget()
		snap := buildSnapshot(&inventory.FwRuleset{Ref: target, Scope: inventory.FwScopeCluster, Enabled: true})
		op := mkOp(OpFwOptionsUpdate, target, &FwOptionsUpdateParams{LogLevelForward: &debug})
		findings := Validate([]Op{op}, snap)
		if !hasErrorCode(findings, codeFwScopeInvalid) {
			t.Fatalf("expected %s for logLevelForward at cluster scope, got: %+v", codeFwScopeInvalid, findings)
		}
	})
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
