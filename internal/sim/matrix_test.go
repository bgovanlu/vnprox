package sim

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// simCase is one row of the verdict matrix (AC1). Each case builds its own
// world so scenarios stay independent and self-documenting.
type simCase struct {
	build func() Input
	req   func(Input) Request
	// expectations:
	name            string
	category        string
	want            Verdict
	missingCode     string   // unreachable: expected Missing.Code
	missingContains string   // unreachable: substring of Missing.Message (exact-message checks)
	blockingPoint   string   // deny: expected BlockingRule.EnforcementPoint
	blockingOrigin  string   // deny: expected BlockingRule.Origin
	blockingAction  string   // deny: expected BlockingRule.Action
	wantCaveats     []string // caveat codes that must be present
}

func (c simCase) run(t *testing.T) {
	t.Helper()
	in := c.build()
	res := Simulate(in, c.req(in))
	if res.Verdict != c.want {
		t.Fatalf("[%s] verdict = %q, want %q\n  missing=%+v blocking=%+v\n  caveats=%s",
			c.name, res.Verdict, c.want, res.Missing, res.BlockingRule, caveatCodes(res))
	}
	// Every result must carry the standing honesty caveats (AC3).
	if !hasCaveat(res, CodeSimulated) {
		t.Errorf("[%s] result missing the standing 'simulated' caveat", c.name)
	}
	if len(res.Caveats) == 0 {
		t.Errorf("[%s] result carries no caveats (AC3 requires a non-empty list)", c.name)
	}
	switch c.want {
	case VerdictUnreachable:
		if res.Missing == nil {
			t.Fatalf("[%s] unreachable but Missing is nil", c.name)
		}
		if c.missingCode != "" && res.Missing.Code != c.missingCode {
			t.Errorf("[%s] Missing.Code = %q, want %q (msg=%q)", c.name, res.Missing.Code, c.missingCode, res.Missing.Message)
		}
		if c.missingContains != "" && !strings.Contains(res.Missing.Message, c.missingContains) {
			t.Errorf("[%s] Missing.Message = %q, want to contain %q", c.name, res.Missing.Message, c.missingContains)
		}
	case VerdictDeny:
		if res.BlockingRule == nil {
			t.Fatalf("[%s] deny but BlockingRule is nil", c.name)
		}
		br := res.BlockingRule
		if c.blockingPoint != "" && br.EnforcementPoint != c.blockingPoint {
			t.Errorf("[%s] blocking point = %q, want %q", c.name, br.EnforcementPoint, c.blockingPoint)
		}
		if c.blockingOrigin != "" && br.Origin != c.blockingOrigin {
			t.Errorf("[%s] blocking origin = %q, want %q", c.name, br.Origin, c.blockingOrigin)
		}
		if c.blockingAction != "" && br.Action != c.blockingAction {
			t.Errorf("[%s] blocking action = %q, want %q", c.name, br.Action, c.blockingAction)
		}
	}
	for _, code := range c.wantCaveats {
		if !hasCaveat(res, code) {
			t.Errorf("[%s] missing expected caveat %q; have %s", c.name, code, caveatCodes(res))
		}
	}
}

func hasCaveat(res Result, code string) bool {
	for _, c := range res.Caveats {
		if c.Code == code {
			return true
		}
	}
	return false
}

func caveatCodes(res Result) string {
	var cs []string
	for _, c := range res.Caveats {
		cs = append(cs, string(c.Severity)+":"+c.Code)
	}
	return strings.Join(cs, ", ")
}

// TestVerdictMatrix is AC1: ≥80 cases spanning every listed scenario.
func TestVerdictMatrix(t *testing.T) {
	var cases []simCase
	cases = append(cases, sameL2Cases()...)
	cases = append(cases, vlanTrunkCases()...)
	cases = append(cases, zoneRoutingCases()...)
	cases = append(cases, firewallEnforcementCases()...)
	cases = append(cases, macroCases()...)
	cases = append(cases, objectCases()...)
	cases = append(cases, defaultPolicyCases()...)
	cases = append(cases, disabledScopeCases()...)
	cases = append(cases, externalCases()...)
	cases = append(cases, honestyCases()...)

	if len(cases) < 80 {
		t.Fatalf("verdict matrix has %d cases, AC1 requires >= 80", len(cases))
	}
	t.Logf("verdict matrix: %d cases", len(cases))

	byCat := map[string]int{}
	for _, c := range cases {
		byCat[c.category]++
	}
	for cat, n := range byCat {
		t.Logf("  category %-22s %d cases", cat, n)
	}

	for _, c := range cases {
		c := c
		t.Run(c.category+"/"+c.name, func(t *testing.T) { c.run(t) })
	}
}

// --- reusable worlds -------------------------------------------------------

// twoGuestBridge builds one or two nodes with a shared VLAN-aware bridge
// vmbr0 (uplink bond0) and two guests attached, with firewall toggles and a
// permissive-or-specified cluster firewall. It is the workhorse for L2 and
// firewall cases.
func twoGuestBridge(sameNode bool, vidA, vidB int, fwOn bool) *world {
	w := newWorld()
	w.bond("pve1", "bond0", "eno1", "eno2")
	w.bridge("pve1", "vmbr0", true, nil, "10.0.0.1", "bond0")
	w.guest("pve1", "100", "app01")
	nicA := w.nic("pve1", "100", "net0", "vmbr0", vidA, fwOn)
	w.ip(nicA, "10.0.0.10", IPSourceIPAM)

	nodeB := "pve1"
	if !sameNode {
		nodeB = "pve2"
		w.bond("pve2", "bond0", "eno1", "eno2")
		w.bridge("pve2", "vmbr0", true, nil, "10.0.0.2", "bond0")
	}
	w.guest(nodeB, "101", "app02")
	nicB := w.nic(nodeB, "101", "net0", "vmbr0", vidB, fwOn)
	w.ip(nicB, "10.0.0.11", IPSourceIPAM)
	return w
}

func nicRef(node, vmid string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: vmid + "/net0"}
}
