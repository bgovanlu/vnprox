// SPDX-License-Identifier: Apache-2.0

package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// policy_examples_test.go is acceptance criterion 4's enforcement: EVERY
// rule in the shipped example policy document must have a fixture that
// makes it FIRE and one that makes it PASS.
//
// The test enumerates the shipped file rather than the fixture table, so a
// rule added to policy_examples.yaml with only a passing fixture — or with
// no fixture at all — fails the build. That direction is the one that
// matters: a guard with only a passing fixture is worse than no guard,
// because it is trusted.

type policyExpectation string

const (
	expectFire policyExpectation = "fire"
	expectPass policyExpectation = "pass"
)

type policyFixture struct {
	snap   inventory.Snapshot
	name   string
	expect policyExpectation
	ops    []Op
}

// examplePolicyFixtures is the fixture corpus, keyed by the rule id in
// policy_examples.yaml.
func examplePolicyFixtures() map[string][]policyFixture {
	guestSnap := policyGuestBearingSnapshot()
	// A snapshot the referential class is happy with, so these fixtures
	// exercise the policy class rather than short-circuiting before it.
	bridgeSnap := buildSnapshot(
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "storage1", "vmbr9"), Name: "vmbr9"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "storage1", "vmbr0"), Name: "vmbr0"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr9"), Name: "vmbr9"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno9"), Name: "eno9"},
	)
	nicRef := testRef(inventory.KindGuestNic, "pve1", "100/net0")
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")

	return map[string][]policyFixture{
		"no-guest-nic-on-vlan1": {
			{
				name: "guest NIC moved onto VLAN 1", expect: expectFire, snap: guestSnap,
				ops: []Op{mkOp(OpGuestNicUpdate, nicRef, &GuestNicUpdateParams{Vid: intPtr(1)})},
			},
			{
				name: "guest NIC moved onto VLAN 20", expect: expectPass, snap: guestSnap,
				ops: []Op{mkOp(OpGuestNicUpdate, nicRef, &GuestNicUpdateParams{Vid: intPtr(20)})},
			},
			{
				name: "guest NIC change that does not touch the VLAN", expect: expectPass, snap: guestSnap,
				ops: []Op{mkOp(OpGuestNicUpdate, nicRef, &GuestNicUpdateParams{RateMbps: intPtr(100)})},
			},
		},
		"guest-bridge-needs-two-uplinks": {
			{
				name: "second uplink removed from a guest-bearing bridge", expect: expectFire, snap: guestSnap,
				ops: []Op{mkOp(OpBridgePortRemove, vmbr2, &BridgePortRemoveParams{Port: "eno2"})},
			},
			{
				name: "guest-bearing bridge keeps both uplinks", expect: expectPass, snap: guestSnap,
				ops: []Op{mkOp(OpBridgeUpdate, vmbr2, &BridgeUpdateParams{MTU: intPtr(9000)})},
			},
			{
				name: "a bridge with no guests may have one uplink", expect: expectPass, snap: guestSnap,
				ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr7"), &BridgeCreateParams{Ports: []string{"eno1"}})},
			},
		},
		"no-changes-to-storage-node-vmbr9": {
			{
				name: "vmbr9 on a storage node", expect: expectFire, snap: bridgeSnap,
				ops: []Op{mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "storage1", "vmbr9"), &BridgeUpdateParams{MTU: intPtr(9000)})},
			},
			{
				name: "vmbr9 on a compute node", expect: expectPass, snap: bridgeSnap,
				ops: []Op{mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr9"), &BridgeUpdateParams{MTU: intPtr(9000)})},
			},
			{
				name: "another bridge on a storage node", expect: expectPass, snap: bridgeSnap,
				ops: []Op{mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "storage1", "vmbr0"), &BridgeUpdateParams{MTU: intPtr(9000)})},
			},
		},
		"new-bridges-should-be-documented": {
			{
				name: "undocumented new bridge", expect: expectFire, snap: bridgeSnap,
				ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr8"), &BridgeCreateParams{MTU: 1500})},
			},
			{
				name: "documented new bridge", expect: expectPass, snap: bridgeSnap,
				ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr8"), &BridgeCreateParams{MTU: 1500, Comments: "guest access, rack 3"})},
			},
			{
				name: "an existing bridge being updated is not a new one", expect: expectPass, snap: bridgeSnap,
				ops: []Op{mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr0"), &BridgeUpdateParams{MTU: intPtr(1500)})},
			},
		},
	}
}

func TestExamplePolicySet_Parses(t *testing.T) {
	set, err := ExamplePolicySet()
	if err != nil {
		t.Fatalf("the shipped example policy document does not load: %v", err)
	}
	if len(set.Rules) == 0 {
		t.Fatalf("the shipped example policy document has no rules")
	}
}

// TestExamplePolicySet_EveryRuleHasAFiringAndAPassingFixture is acceptance
// criterion 4 itself.
func TestExamplePolicySet_EveryRuleHasAFiringAndAPassingFixture(t *testing.T) {
	set, err := ExamplePolicySet()
	if err != nil {
		t.Fatalf("ExamplePolicySet: %v", err)
	}
	fixtures := examplePolicyFixtures()

	for _, rule := range set.Rules {
		t.Run(rule.ID, func(t *testing.T) {
			cases := fixtures[rule.ID]
			if len(cases) == 0 {
				t.Fatalf("rule %q has NO fixtures. Every shipped rule needs one that makes it fire and one that makes it pass (T-2601 AC4).", rule.ID)
			}
			var fired, passed int
			for _, tc := range cases {
				switch tc.expect {
				case expectFire:
					fired++
				case expectPass:
					passed++
				}
			}
			if fired == 0 {
				t.Errorf("rule %q has no fixture that makes it FIRE — a rule with only a passing fixture is untested (T-2601 AC4).", rule.ID)
			}
			if passed == 0 {
				t.Errorf("rule %q has no fixture that makes it PASS — a rule that fires on everything is not a rule (T-2601 AC4).", rule.ID)
			}

			// Each fixture is evaluated against THIS RULE ALONE, so one
			// rule's fixture can never be satisfied by a sibling rule.
			only := PolicySet{Version: set.Version, Rules: []PolicyRule{rule}}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					res := EvaluatePolicy(PolicyInput{Set: only}, tc.ops, tc.snap)
					violated := len(res.Rules) == 1 && len(res.Rules[0].ViolatingOps) > 0
					switch tc.expect {
					case expectFire:
						if !violated {
							t.Errorf("fixture was expected to make %q fire, but it did not: %+v", rule.ID, res)
						}
					case expectPass:
						if violated {
							t.Errorf("fixture was expected to pass %q, but the rule fired: %+v", rule.ID, res)
						}
					}
				})
			}
		})
	}
}

// TestExamplePolicyFixtures_NameOnlyRealRules catches the other direction: a
// fixture left behind for a rule that was renamed or deleted would otherwise
// look like coverage while testing nothing.
func TestExamplePolicyFixtures_NameOnlyRealRules(t *testing.T) {
	set, err := ExamplePolicySet()
	if err != nil {
		t.Fatalf("ExamplePolicySet: %v", err)
	}
	known := map[string]bool{}
	for _, r := range set.Rules {
		known[r.ID] = true
	}
	for id := range examplePolicyFixtures() {
		if !known[id] {
			t.Errorf("fixtures exist for rule %q, which is not in the shipped policy document", id)
		}
	}
}

// TestExamplePolicySet_DenyRulesActuallyBlock ties the example set back to
// the pipeline: a firing deny fixture must produce a blocking finding when
// run through the real validator, not only through the evaluator.
func TestExamplePolicySet_DenyRulesActuallyBlock(t *testing.T) {
	set, err := ExamplePolicySet()
	if err != nil {
		t.Fatalf("ExamplePolicySet: %v", err)
	}
	fixtures := examplePolicyFixtures()
	for _, rule := range set.Rules {
		if rule.Severity != PolicyDeny {
			continue
		}
		for _, tc := range fixtures[rule.ID] {
			if tc.expect != expectFire {
				continue
			}
			only := PolicySet{Version: set.Version, Rules: []PolicyRule{rule}}
			got := ValidateWithSafety(tc.ops, tc.snap, SafetyOptions{Policy: only})
			var blocked bool
			for _, f := range got {
				if f.Code == codePolicyViolation && f.Severity == SeverityError {
					blocked = true
				}
			}
			if !blocked {
				t.Errorf("deny rule %q fixture %q did not block at validate: %+v", rule.ID, tc.name, got)
			}
		}
	}
}
