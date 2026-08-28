// SPDX-License-Identifier: Apache-2.0

package route

import "testing"

// pvecubeRulesV4/V6 are `ip -j rule show`/`ip -j -6 rule show`'s exact
// output on pvecube, captured in
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt.
const pvecubeRulesV4 = `[{"priority":0,"src":"all","table":"local"},{"priority":32766,"src":"all","table":"main"},{"priority":32767,"src":"all","table":"default"}]`
const pvecubeRulesV6 = `[{"priority":0,"src":"all","table":"local"},{"priority":32766,"src":"all","table":"main"}]`

func TestParsePolicyRules_pvecubeEvidence(t *testing.T) {
	v4, err := ParsePolicyRules([]byte(pvecubeRulesV4), AFIv4)
	if err != nil {
		t.Fatalf("ParsePolicyRules(v4): %v", err)
	}
	if len(v4) != 3 {
		t.Fatalf("got %d v4 rules, want 3", len(v4))
	}
	want := []PolicyRule{
		{AFI: AFIv4, Priority: 0, Src: "all", Table: "local"},
		{AFI: AFIv4, Priority: 32766, Src: "all", Table: "main"},
		{AFI: AFIv4, Priority: 32767, Src: "all", Table: "default"},
	}
	for i, w := range want {
		if v4[i] != w {
			t.Errorf("v4[%d] = %+v, want %+v", i, v4[i], w)
		}
	}

	v6, err := ParsePolicyRules([]byte(pvecubeRulesV6), AFIv6)
	if err != nil {
		t.Fatalf("ParsePolicyRules(v6): %v", err)
	}
	// v6 has no stock "default" table rule (upstream kernel default) —
	// the parser must not assume both families always carry 3 rules.
	if len(v6) != 2 {
		t.Fatalf("got %d v6 rules, want 2 (v6 ships no empty 'default' table rule by convention)", len(v6))
	}
}

func TestParsePolicyRules_emptyInput(t *testing.T) {
	rules, err := ParsePolicyRules(nil, AFIv4)
	if err != nil || rules != nil {
		t.Fatalf("ParsePolicyRules(nil) = %v, %v; want nil, nil", rules, err)
	}
}

func TestParsePolicyRules_malformed(t *testing.T) {
	if _, err := ParsePolicyRules([]byte("not json"), AFIv4); err == nil {
		t.Error("ParsePolicyRules(garbage) succeeded, want error")
	}
}

func TestParsePolicyRules_unknownFieldsIgnored(t *testing.T) {
	// A real VRF-lite rule can carry fwmark/iif/suppress_* fields this
	// package's struct doesn't name — encoding/json must silently drop
	// them rather than fail the parse.
	raw := `[{"priority":100,"src":"10.0.0.0/24","table":"200","fwmark":"0x64","iif":"vmbr5"}]`
	rules, err := ParsePolicyRules([]byte(raw), AFIv4)
	if err != nil {
		t.Fatalf("ParsePolicyRules with unknown fields: %v", err)
	}
	if len(rules) != 1 || rules[0].Table != "200" || rules[0].Src != "10.0.0.0/24" {
		t.Errorf("got %+v", rules)
	}
}
