// SPDX-License-Identifier: Apache-2.0

package host

import "testing"

func TestParseNftRuleset_Empty(t *testing.T) {
	for name, raw := range map[string][]byte{
		"nil input":       nil,
		"empty string":    []byte(""),
		"whitespace only": []byte("  \n\t"),
		"metainfo-only (real, evidence file §2 — a disabled node)": []byte(
			`{"nftables": [{"metainfo": {"version": "1.1.3", "release_name": "Commodore Bullmoose #4", "json_schema_version": 1}}]}`,
		),
	} {
		t.Run(name, func(t *testing.T) {
			rs, err := ParseNftRuleset(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rs.Tables) != 0 || len(rs.Chains) != 0 || len(rs.Rules) != 0 {
				t.Fatalf("expected empty ruleset, got %+v", rs)
			}
		})
	}
}

func TestParseNftRuleset_Malformed(t *testing.T) {
	for name, raw := range []string{
		`not json at all`,
		`{"nftables": "not an array"}`,
		`{"nftables": [123, "garbage", {"table": {"family": "inet"}}]}`,
	} {
		_ = name
		if _, err := ParseNftRuleset([]byte(raw)); raw == `not json at all` && err == nil {
			t.Fatalf("expected error for invalid top-level JSON")
		}
	}
	// A malformed individual item must not sink the whole parse.
	rs, err := ParseNftRuleset([]byte(`{"nftables": [123, "garbage", {"table": {"family": "inet", "name": "proxmox-firewall"}}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rs.Tables) != 1 || rs.Tables[0].Family != "inet" || rs.Tables[0].Name != "proxmox-firewall" {
		t.Fatalf("expected one recovered table, got %+v", rs.Tables)
	}
}

// pveFixtureRuleset is a hand-authored nftables JSON document built from
// the table/chain vocabulary confirmed in
// planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt
// §4 (read from the installed proxmox-firewall 1.2.3 binary's own `add
// table`/`add chain` command strings) — NOT observed live (see that
// file's "what was not observed" section: no populated ruleset could be
// captured without turning on a live production host's firewall). The
// individual rule/expr shapes below follow nft's own generic upstream
// JSON schema (libnftables-json(5)), used defensively/tolerantly by this
// parser rather than assumed authoritative for proxmox-firewall
// specifically.
const pveFixtureRuleset = `{
  "nftables": [
    {"metainfo": {"version": "1.1.3", "release_name": "x", "json_schema_version": 1}},
    {"table": {"family": "inet", "name": "proxmox-firewall", "handle": 1}},
    {"table": {"family": "bridge", "name": "proxmox-firewall-guests", "handle": 2}},
    {"table": {"family": "ip", "name": "some-other-table", "handle": 3}},
    {"chain": {"family": "inet", "table": "proxmox-firewall", "name": "input", "handle": 1, "type": "filter", "hook": "input", "prio": "filter", "policy": "drop"}},
    {"chain": {"family": "inet", "table": "proxmox-firewall", "name": "accept-management", "handle": 2}},
    {"chain": {"family": "bridge", "table": "proxmox-firewall-guests", "name": "vm-in", "handle": 3, "type": "filter", "hook": "postrouting", "prio": 0, "policy": "accept"}},
    {"rule": {"family": "inet", "table": "proxmox-firewall", "chain": "input", "handle": 10, "comment": "management ssh",
      "expr": [
        {"match": {"left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 22, "op": "=="}},
        {"match": {"left": {"payload": {"protocol": "ip", "field": "saddr"}}, "right": {"prefix": {"addr": "10.0.0.0", "len": 24}}, "op": "=="}},
        {"accept": null}
      ]}},
    {"rule": {"family": "inet", "table": "proxmox-firewall", "chain": "accept-management", "handle": 11,
      "expr": [
        {"match": {"left": {"meta": {"key": "l4proto"}}, "right": "tcp", "op": "=="}},
        {"jump": {"target": "do-reject"}}
      ]}},
    {"rule": {"family": "bridge", "table": "proxmox-firewall-guests", "chain": "vm-in", "handle": 12,
      "expr": [
        {"match": {"left": {"meta": {"key": "iifname"}}, "right": "fwln101i0", "op": "=="}},
        {"log": {}},
        {"drop": null}
      ]}}
  ]
}`

func TestParseNftRuleset_Fixture(t *testing.T) {
	rs, err := ParseNftRuleset([]byte(pveFixtureRuleset))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rs.Tables) != 3 {
		t.Fatalf("expected 3 tables, got %d: %+v", len(rs.Tables), rs.Tables)
	}
	pve := rs.PVETables()
	if len(pve) != 2 {
		t.Fatalf("expected 2 PVE-authored tables, got %d: %+v", len(pve), pve)
	}
	for _, tbl := range pve {
		if tbl.Name == "some-other-table" {
			t.Fatalf("some-other-table must not be classified as PVE-authored")
		}
	}

	if len(rs.Chains) != 3 {
		t.Fatalf("expected 3 chains, got %d: %+v", len(rs.Chains), rs.Chains)
	}
	var inputChain *NftChain
	for i := range rs.Chains {
		if rs.Chains[i].Name == "input" {
			inputChain = &rs.Chains[i]
		}
	}
	if inputChain == nil {
		t.Fatalf("expected an 'input' chain")
	}
	if inputChain.Hook != "input" || inputChain.Policy != "drop" || inputChain.Priority != "filter" {
		t.Fatalf("input chain base-chain fields wrong: %+v", inputChain)
	}
	if !IsPVEBuiltinChain("input") || !IsPVEBuiltinChain("accept-management") || !IsPVEBuiltinChain("vm-in") {
		t.Fatalf("expected input/accept-management/vm-in to be recognized as PVE built-in chains")
	}
	if IsPVEBuiltinChain("some-random-chain") {
		t.Fatalf("an unrecognized chain name must not be classified as PVE built-in")
	}

	if len(rs.Rules) != 3 {
		t.Fatalf("expected 3 rules, got %d: %+v", len(rs.Rules), rs.Rules)
	}

	var mgmtRule, jumpRule, guestRule *NftRule
	for i := range rs.Rules {
		switch rs.Rules[i].Handle {
		case 10:
			mgmtRule = &rs.Rules[i]
		case 11:
			jumpRule = &rs.Rules[i]
		case 12:
			guestRule = &rs.Rules[i]
		}
	}
	if mgmtRule == nil || jumpRule == nil || guestRule == nil {
		t.Fatalf("expected all three rules to be found by handle, got %+v", rs.Rules)
	}

	if mgmtRule.Comment != "management ssh" {
		t.Errorf("mgmtRule.Comment = %q, want %q", mgmtRule.Comment, "management ssh")
	}
	if mgmtRule.Proto != "tcp" {
		t.Errorf("mgmtRule.Proto = %q, want tcp", mgmtRule.Proto)
	}
	if mgmtRule.DstPort != "22" {
		t.Errorf("mgmtRule.DstPort = %q, want 22", mgmtRule.DstPort)
	}
	if mgmtRule.SrcAddr != "10.0.0.0/24" {
		t.Errorf("mgmtRule.SrcAddr = %q, want 10.0.0.0/24", mgmtRule.SrcAddr)
	}
	if mgmtRule.Verdict != "accept" {
		t.Errorf("mgmtRule.Verdict = %q, want accept", mgmtRule.Verdict)
	}
	if len(mgmtRule.Expr) == 0 {
		t.Errorf("mgmtRule.Expr must retain the raw expression array")
	}

	if jumpRule.Verdict != "jump do-reject" {
		t.Errorf("jumpRule.Verdict = %q, want %q", jumpRule.Verdict, "jump do-reject")
	}
	if jumpRule.Proto != "tcp" {
		t.Errorf("jumpRule.Proto = %q, want tcp (from meta l4proto)", jumpRule.Proto)
	}

	if guestRule.IIfname != "fwln101i0" {
		t.Errorf("guestRule.IIfname = %q, want fwln101i0", guestRule.IIfname)
	}
	if !guestRule.Log {
		t.Errorf("guestRule.Log = false, want true")
	}
	if guestRule.Verdict != "drop" {
		t.Errorf("guestRule.Verdict = %q, want drop", guestRule.Verdict)
	}
}

func TestParseNftRuleset_NeverPanics(t *testing.T) {
	inputs := []string{
		`{`,
		`{"nftables": null}`,
		`{"nftables": [null]}`,
		`{"nftables": [{"rule": {"expr": [1, "x", {}, {"match": {}}, {"match": {"left": {}, "right": null}}]}}]}`,
		`{"nftables": [{"chain": {"prio": {}}}]}`,
		`{"nftables": [{"table": null}]}`,
	}
	for _, in := range inputs {
		if _, err := ParseNftRuleset([]byte(in)); err != nil {
			// An error is fine; a panic is not — the test itself would
			// fail with a panic if one occurred, so reaching here at all
			// (with or without err) is the assertion.
			continue
		}
	}
}
