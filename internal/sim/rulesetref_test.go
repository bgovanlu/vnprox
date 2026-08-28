// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"encoding/json"
	"testing"
)

// TestRuleRef_RulesetRefPopulatedForEveryOrigin is T-2002's regression for
// the gap T-504's report originally flagged: RulesetRef used to be left
// unpopulated (empty string) for origin: "guest" — the single most common
// deny case — while cluster/group origin got the cluster ruleset's own
// ref. Fixed in internal/sim/firewall.go's ruleRef: every origin now names
// the ruleset the matched rule is literally defined in.
func TestRuleRef_RulesetRefPopulatedForEveryOrigin(t *testing.T) {
	r22 := req(guestEP(g100), guestEP(g101), "tcp", 22)

	cases := []struct {
		name       string
		build      func() Input
		wantOrigin string
		wantRef    string
	}{
		{
			name:       "guest-origin: the blocked guest's own ruleset ref",
			build:      fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP"))),
			wantOrigin: "guest",
			wantRef:    "fw-ruleset:pve1:guest/qemu/101", // g101 is the dest-guest-in target
		},
		{
			name:       "cluster-origin: the cluster ruleset ref",
			build:      fwBuild("ACCEPT", "ACCEPT", rules(rule(0, "in", "DROP")), nil, nil),
			wantOrigin: "cluster",
			wantRef:    "fw-ruleset::cluster",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Simulate(tc.build(), r22(Input{}))
			if res.Verdict != VerdictDeny || res.BlockingRule == nil {
				t.Fatalf("verdict = %v, BlockingRule = %+v; want a deny with a blocking rule", res.Verdict, res.BlockingRule)
			}
			br := res.BlockingRule
			if br.Origin != tc.wantOrigin {
				t.Fatalf("origin = %q, want %q", br.Origin, tc.wantOrigin)
			}
			if br.RulesetRef == "" {
				t.Errorf("RulesetRef is empty for origin %q — the exact gap T-504/T-2002 fixed", br.Origin)
			}
			if br.RulesetRef != tc.wantRef {
				t.Errorf("RulesetRef = %q, want %q", br.RulesetRef, tc.wantRef)
			}
		})
	}
}

// TestRuleRef_JSONSchema_Stable is a regression guard against the exact
// mistake T-2002 almost shipped: internal/sim.RuleRef (as sim.Result's
// BlockingRule) is not just this package's own return value — it is also
// the frozen `simulate.path` MCP tool's payload verbatim
// (cmd/vnproxd/mcpwire.go's mcpSimulatePath returns sim.Result directly;
// docs/architecture.md §13.1, decision D10: additive-only, no field ever
// removed or renamed without a version bump). A "no consumer reads this
// field" argument based on grepping the Go/TypeScript source tree — sound
// reasoning in every other respect — still misses an external MCP client
// reading the wire JSON, since that client's code never appears in this
// repo. This test golden-checks that every documented field-name in
// RuleRef's JSON shape is still present, byte for byte, so a future
// "nothing reads this" refactor gets caught here before it reaches
// review. See planning/reports/T-2002.md for the full story, and
// planning/tasks/phase-18.md for the follow-up card proposing a more
// general frozen-MCP-payload guard across all nine tools.
func TestRuleRef_JSONSchema_Stable(t *testing.T) {
	rr := RuleRef{
		EnforcementPoint: "dest-guest-in",
		RulesetRef:       "fw-ruleset:pve1:guest/qemu/101",
		Origin:           "guest",
		GroupName:        "",
		Direction:        "in",
		Action:           "DROP",
		Pos:              0,
	}

	got, err := json.Marshal(rr)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// groupName is omitempty and empty here, so it's deliberately excluded
	// from the required set below.
	for _, field := range []string{"enforcementPoint", "rulesetRef", "origin", "direction", "action", "rule", "pos"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("RuleRef JSON missing frozen field %q (got %v)", field, generic)
		}
	}
}
