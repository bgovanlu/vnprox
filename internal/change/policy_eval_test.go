package change

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// policyRule is a terse builder for a rule under test.
func policyRule(id string, sev PolicySeverity, match, assert []PolicyCondition, tags ...string) PolicyRule {
	return PolicyRule{ID: id, Description: "description of " + id, Severity: sev, Match: match, Assert: assert, Tags: tags}
}

func cond(field string, op PolicyCondOp, value any) PolicyCondition {
	return PolicyCondition{Field: field, Op: op, Value: value}
}

func mustPolicySet(t *testing.T, rules ...PolicyRule) PolicySet {
	t.Helper()
	set := PolicySet{Version: PolicyFormatVersion, Rules: rules}
	if err := set.Validate("test"); err != nil {
		t.Fatalf("policy set under test is itself invalid: %v", err)
	}
	return set
}

// --- acceptance criterion 1: deny blocks and names id + description --------

func TestValidateWithSafety_DenyRuleBlocksAndNamesRuleAndDescription(t *testing.T) {
	set := mustPolicySet(t, policyRule("no-vlan1", PolicyDeny,
		[]PolicyCondition{cond(policyFieldOpType, PolicyOpEq, "guest.nic.update")},
		[]PolicyCondition{cond("params.vid", PolicyOpNe, 1)}))

	nicRef := testRef(inventory.KindGuestNic, "pve1", "100/net0")
	violating := []Op{mkOp(OpGuestNicUpdate, nicRef, &GuestNicUpdateParams{Vid: intPtr(1)})}
	conforming := []Op{mkOp(OpGuestNicUpdate, nicRef, &GuestNicUpdateParams{Vid: intPtr(20)})}
	// A real snapshot: the policy class runs after the referential class,
	// so an op whose target does not exist never gets that far.
	snap := policyGuestBearingSnapshot()

	got := ValidateWithSafety(violating, snap, SafetyOptions{Policy: set})
	var policyFindings []Finding
	for _, f := range got {
		if f.Code == codePolicyViolation {
			policyFindings = append(policyFindings, f)
		}
	}
	if len(policyFindings) != 1 {
		t.Fatalf("policy findings = %+v, want exactly one", policyFindings)
	}
	f := policyFindings[0]
	if f.Severity != SeverityError {
		t.Errorf("Severity = %s, want error (a deny rule blocks)", f.Severity)
	}
	if !strings.Contains(f.Message, "no-vlan1") {
		t.Errorf("Message = %q, want it to name the rule id", f.Message)
	}
	if !strings.Contains(f.Message, "description of no-vlan1") {
		t.Errorf("Message = %q, want it to name the rule description", f.Message)
	}
	if f.Ref != nicRef.String() {
		t.Errorf("Ref = %q, want the offending op's target %q", f.Ref, nicRef.String())
	}
	if !hasError(got) {
		t.Errorf("a deny rule must produce a blocking finding")
	}

	// ...and a conforming changeset is completely unaffected.
	clean := ValidateWithSafety(conforming, snap, SafetyOptions{Policy: set})
	for _, f := range clean {
		if f.Code == codePolicyViolation {
			t.Errorf("a conforming changeset earned a policy finding: %+v", f)
		}
	}
	if want := ValidateWithSafety(conforming, snap, SafetyOptions{}); !reflect.DeepEqual(clean, want) {
		t.Errorf("findings for a conforming changeset differ with the policy engine on:\n got %+v\nwant %+v", clean, want)
	}
}

// --- acceptance criterion 2 (engine half): warn annotates, never blocks ----

func TestValidateWithSafety_WarnRuleAnnotatesWithoutBlocking(t *testing.T) {
	set := mustPolicySet(t, policyRule("document-bridges", PolicyWarn,
		[]PolicyCondition{cond(policyFieldOpType, PolicyOpEq, "bridge.create")},
		[]PolicyCondition{cond("params.comments", PolicyOpExists, nil)}))

	ops := []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr7"), &BridgeCreateParams{MTU: 1500})}
	got := ValidateWithSafety(ops, buildSnapshot(), SafetyOptions{Policy: set})

	var found bool
	for _, f := range got {
		if f.Code != codePolicyViolation {
			continue
		}
		found = true
		if f.Severity != SeverityWarning {
			t.Errorf("Severity = %s, want warning (a warn rule never blocks)", f.Severity)
		}
	}
	if !found {
		t.Fatalf("no policy finding produced; got %+v", got)
	}
	if hasError(got) {
		t.Errorf("a warn rule must not block: %+v", got)
	}
}

// --- acceptance criterion 6: an empty policy set changes nothing -----------

// TestValidateWithSafety_EmptyPolicySetChangesNothing runs a representative
// corpus of changesets through the pipeline twice — once with the policy
// engine's inputs entirely absent, once with an explicitly-constructed empty
// policy set and a live report sink — and requires byte-identical findings.
//
// The wider proof of the same criterion is structural: SafetyOptions.Policy's
// zero value IS the empty set, so the ENTIRE pre-existing changeset test
// suite in this package already runs with the policy class installed in the
// pipeline and evaluating an empty rule set on every single call.
func TestValidateWithSafety_EmptyPolicySetChangesNothing(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	snap := buildSnapshot(
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
	)

	corpus := map[string][]Op{
		"empty":            {},
		"bridge create":    {mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"), &BridgeCreateParams{MTU: 1500})},
		"port add":         {mkOp(OpBridgePortAdd, vmbr0, &BridgePortAddParams{Port: "eno1"})},
		"schema error":     {mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbrX"), &BridgeCreateParams{MTU: 70000})},
		"referential fail": {mkOp(OpBridgePortAdd, vmbr0, &BridgePortAddParams{Port: "nope0"})},
		"guest nic":        {mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"), &GuestNicUpdateParams{Vid: intPtr(7)})},
	}

	for name, ops := range corpus {
		t.Run(name, func(t *testing.T) {
			before := ValidateWithSafety(ops, snap, SafetyOptions{})

			var report PolicyResult
			after := ValidateWithSafety(ops, snap, SafetyOptions{Policy: PolicySet{Version: PolicyFormatVersion}, PolicyReport: &report})

			if !reflect.DeepEqual(before, after) {
				t.Errorf("an empty policy set changed the findings:\nwithout: %+v\n   with: %+v", before, after)
			}
			if len(report.Findings) != 0 || len(report.Rules) != 0 {
				t.Errorf("an empty policy set produced a non-empty report: %+v", report)
			}
		})
	}
}

// --- the condition vocabulary ----------------------------------------------

func TestEvaluatePolicy_ConditionOperators(t *testing.T) {
	bridgeRef := testRef(inventory.KindBridge, "pve1", "vmbr4")
	op := mkOp(OpBridgeCreate, bridgeRef, &BridgeCreateParams{MTU: 9000, Ports: []string{"eno1", "eno2"}, Comments: "storage fabric"})
	snap := buildSnapshot()

	cases := []struct {
		name string
		c    PolicyCondition
		want bool
	}{
		{"eq on op type", cond(policyFieldOpType, PolicyOpEq, "bridge.create"), true},
		{"eq mismatch", cond(policyFieldOpType, PolicyOpEq, "bridge.delete"), false},
		{"ne on absent field holds", cond("params.gateway", PolicyOpNe, "10.0.0.1"), true},
		{"eq on absent field fails", cond("params.gateway", PolicyOpEq, "10.0.0.1"), false},
		{"in", cond(policyFieldTargetNode, PolicyOpIn, []any{"pve1", "pve2"}), true},
		{"notIn", cond(policyFieldTargetNode, PolicyOpNotIn, []any{"pve3"}), true},
		{"gt numeric", cond("params.mtu", PolicyOpGt, 1500), true},
		{"lte numeric", cond("params.mtu", PolicyOpLte, 1500), false},
		{"matches glob", cond(policyFieldTargetID, PolicyOpMatches, "vmbr*"), true},
		{"notMatches glob", cond(policyFieldTargetID, PolicyOpNotMatches, "vlan*"), true},
		{"exists", cond("params.comments", PolicyOpExists, nil), true},
		{"notExists", cond("params.gateway", PolicyOpNotExists, nil), true},
		{"contains list member", cond("params.ports", PolicyOpContains, "eno2"), true},
		{"notContains list member", cond("params.ports", PolicyOpNotContains, "eno9"), true},
		{"contains substring", cond("params.comments", PolicyOpContains, "fabric"), true},
		{"glob does not match dots as separators", cond(policyFieldOpType, PolicyOpMatches, "bridge.*"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := mustPolicySet(t, policyRule("r", PolicyDeny, []PolicyCondition{tc.c}, nil))
			res := EvaluatePolicy(PolicyInput{Set: set}, []Op{op}, snap)
			matched := len(res.Rules) == 1 && len(res.Rules[0].MatchedOps) == 1
			if matched != tc.want {
				t.Errorf("condition %+v matched = %v, want %v", tc.c, matched, tc.want)
			}
		})
	}
}

// TestEvaluatePolicy_AssertlessRuleTreatsMatchAsViolation pins the
// "never touch vmbr9 on the storage nodes" shape: there is nothing to
// assert, the op's existence is the violation.
func TestEvaluatePolicy_AssertlessRuleTreatsMatchAsViolation(t *testing.T) {
	set := mustPolicySet(t, policyRule("hands-off", PolicyDeny, []PolicyCondition{
		cond(policyFieldTargetID, PolicyOpEq, "vmbr9"),
		cond(policyFieldTargetNode, PolicyOpMatches, "storage*"),
	}, nil))

	fire := []Op{mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "storage1", "vmbr9"), &BridgeUpdateParams{MTU: intPtr(9000)})}
	pass := []Op{mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr9"), &BridgeUpdateParams{MTU: intPtr(9000)})}

	if res := EvaluatePolicy(PolicyInput{Set: set}, fire, buildSnapshot()); !res.Denied() {
		t.Errorf("an assert-less rule did not treat a match as a violation: %+v", res)
	}
	if res := EvaluatePolicy(PolicyInput{Set: set}, pass, buildSnapshot()); res.Denied() {
		t.Errorf("the rule fired on a node it does not name: %+v", res)
	}
}

// --- derived inventory facts ----------------------------------------------

// policyGuestBearingSnapshot is a two-uplink bridge carrying one guest NIC.
func policyGuestBearingSnapshot() inventory.Snapshot {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	eno1 := testRef(inventory.KindPhysNic, "pve1", "eno1")
	eno2 := testRef(inventory.KindPhysNic, "pve1", "eno2")
	return buildSnapshot(
		&inventory.PhysNic{Ref: eno1, Name: "eno1"},
		&inventory.PhysNic{Ref: eno2, Name: "eno2"},
		&inventory.Bridge{Ref: vmbr2, Name: "vmbr2", VlanAware: true, Ports: []inventory.Ref{eno1, eno2}, PortNames: []string{"eno1", "eno2"}},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "100"), Name: "web01", VMID: 100, Status: "running"},
		&inventory.GuestNic{
			Ref: testRef(inventory.KindGuestNic, "pve1", "100/net0"), Key: "net0",
			Guest: testRef(inventory.KindGuest, "pve1", "100"), BridgeOrVnet: vmbr2,
		},
	)
}

func TestEvaluatePolicy_DerivedFactsAreNetEffect(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	snap := policyGuestBearingSnapshot()

	set := mustPolicySet(t, policyRule("two-uplinks", PolicyDeny,
		[]PolicyCondition{
			cond(policyFieldOpType, PolicyOpMatches, "bridge.*"),
			cond(policyFieldTargetGuestCount, PolicyOpGt, 0),
		},
		[]PolicyCondition{cond(policyFieldTargetUplinks, PolicyOpGte, 2)}))

	// Removing one of the two uplinks leaves one: the rule fires, even
	// though the op itself says nothing about guests.
	fire := []Op{mkOp(OpBridgePortRemove, vmbr2, &BridgePortRemoveParams{Port: "eno2"})}
	if res := EvaluatePolicy(PolicyInput{Set: set}, fire, snap); !res.Denied() {
		t.Errorf("removing the second uplink from a guest-bearing bridge did not fire the rule: %+v", res)
	}

	// Touching the bridge without dropping below two uplinks passes.
	pass := []Op{mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{MTU: intPtr(9000)})}
	if res := EvaluatePolicy(PolicyInput{Set: set}, pass, snap); res.Denied() {
		t.Errorf("a bridge that keeps both uplinks fired the rule: %+v", res)
	}
}

func TestEvaluatePolicy_TargetFacts(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	snap := policyGuestBearingSnapshot()
	protected := ProtectedSet{"pve1": {vmbr2}}

	cases := []struct {
		name string
		c    PolicyCondition
		op   Op
		in   PolicyInput
		want bool
	}{
		{
			name: "target.exists on a known bridge",
			c:    cond(policyFieldTargetExists, PolicyOpEq, true),
			op:   mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{MTU: intPtr(1500)}),
			want: true,
		},
		{
			name: "target.exists is false once the changeset deletes it",
			c:    cond(policyFieldTargetExists, PolicyOpEq, false),
			op:   mkOp(OpBridgeDelete, vmbr2, &BridgeDeleteParams{}),
			want: true,
		},
		{
			name: "target.protected reads the onboarding-confirmed set",
			c:    cond(policyFieldTargetProtected, PolicyOpEq, true),
			in:   PolicyInput{Protected: protected},
			op:   mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{MTU: intPtr(1500)}),
			want: true,
		},
		{
			name: "target.protected is false with no protected set",
			c:    cond(policyFieldTargetProtected, PolicyOpEq, true),
			op:   mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{MTU: intPtr(1500)}),
			want: false,
		},
		{
			name: "target.vlanAware reflects the changeset's own net effect",
			c:    cond(policyFieldTargetVlanAware, PolicyOpEq, false),
			op:   mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{VlanAware: boolPtr(false)}),
			want: true,
		},
		{
			name: "target.guestCount counts attached NICs",
			c:    cond(policyFieldTargetGuestCount, PolicyOpEq, 1),
			op:   mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{MTU: intPtr(1500)}),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Set = mustPolicySet(t, policyRule("r", PolicyDeny, []PolicyCondition{tc.c}, nil))
			res := EvaluatePolicy(in, []Op{tc.op}, snap)
			matched := len(res.Rules) == 1 && len(res.Rules[0].MatchedOps) == 1
			if matched != tc.want {
				t.Errorf("condition %+v matched = %v, want %v", tc.c, matched, tc.want)
			}
		})
	}
}

// --- the interface T-2604 consumes ----------------------------------------

// TestPolicyResult_TaggedOps pins the shape T-2604's "anything a T-2601
// policy tags" op class reads: tag -> matched op indices, keyed on MATCHED
// ops (a class is a class whether or not its assertion held).
func TestPolicyResult_TaggedOps(t *testing.T) {
	set := mustPolicySet(t,
		policyRule("fw-changes", PolicyWarn, []PolicyCondition{cond(policyFieldOpType, PolicyOpMatches, "fw.*")}, nil, "two-person", "firewall"),
		policyRule("bridge-changes", PolicyWarn, []PolicyCondition{cond(policyFieldOpType, PolicyOpMatches, "bridge.*")}, nil, "two-person"),
	)
	ops := []Op{
		mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr8"), &BridgeCreateParams{MTU: 1500}),
		mkOp(OpFwOptionsUpdate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwOptionsUpdateParams{}),
	}

	res := EvaluatePolicy(PolicyInput{Set: set}, ops, buildSnapshot())
	tagged := res.TaggedOps()
	if got := tagged["two-person"]; !reflect.DeepEqual(got, []int{0, 1}) {
		t.Errorf("TaggedOps[two-person] = %v, want [0 1]", got)
	}
	if got := tagged["firewall"]; !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("TaggedOps[firewall] = %v, want [1]", got)
	}
	counts := res.MatchCounts()
	if counts["fw-changes"] != 1 || counts["bridge-changes"] != 1 {
		t.Errorf("MatchCounts = %v, want one match each", counts)
	}
}

// TestEvaluatePolicy_UnmatchedRuleIsReportedNotSilent pins the per-rule
// bookkeeping half of "a policy that matches nothing is an error, not a
// silent pass": a rule that matched nothing is reported as such in the
// result, rather than vanishing.
func TestEvaluatePolicy_UnmatchedRuleIsReportedNotSilent(t *testing.T) {
	set := mustPolicySet(t, policyRule("never-fires", PolicyDeny,
		[]PolicyCondition{cond(policyFieldOpType, PolicyOpEq, "sdn.zone.delete")}, nil))
	ops := []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr8"), &BridgeCreateParams{MTU: 1500})}

	res := EvaluatePolicy(PolicyInput{Set: set}, ops, buildSnapshot())
	if len(res.Rules) != 1 {
		t.Fatalf("Rules = %+v, want the unmatched rule reported", res.Rules)
	}
	if got := res.MatchCounts()["never-fires"]; got != 0 {
		t.Errorf("MatchCounts[never-fires] = %d, want 0", got)
	}
	if res.Denied() {
		t.Errorf("an unmatched rule must not deny anything")
	}
}
