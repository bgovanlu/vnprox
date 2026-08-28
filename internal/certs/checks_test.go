// SPDX-License-Identifier: Apache-2.0

package certs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func issuesByCheck(issues []Issue, check string) []Issue {
	var out []Issue
	for _, i := range issues {
		if i.Check == check {
			out = append(out, i)
		}
	}
	return out
}

func scanFixture(t *testing.T, ca *testCA, nodes map[string][]byte) (Inventory, string) {
	t.Helper()
	root := writeTree(t, ca, nodes)
	inv, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return inv, root
}

// The check this whole phase exists for. The SAN set is the one read off real
// hardware; 192.168.1.9 is that node's actual address and is absent from it.
func TestSANMismatchFiresOnTheRealPvecubeCase(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "pvecube.localdomain.", dnsNames: pvecubeDNSSANs, ipSANs: pvecubeIPSANs})
	inv, _ := scanFixture(t, ca, map[string][]byte{"pvecube": leaf})

	// The node name IS covered by this certificate, which is what makes the
	// dialling fix (peername.go) work — so to see the mismatch check fire we
	// have to look at a node whose name is *not* covered either. Model the
	// genuinely broken shape: a certificate covering neither identity.
	bare, _ := issueLeaf(t, ca, leafOpts{cn: "old-name", dnsNames: []string{"old-name"}, ipSANs: []string{"192.168.100.99"}})
	invBroken, _ := scanFixture(t, ca, map[string][]byte{"pvecube": bare})

	facts := ClusterFacts{DialAddrs: map[string]string{"pvecube": pvecubeAddr}, Members: []string{"pvecube"}}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// The real certificate covers the node name, so with the dialling fix in
	// place this is NOT a mismatch — and saying it were would be crying wolf.
	if got := issuesByCheck(Evaluate(inv, facts, now, 0, nil), CheckSANMismatch); len(got) != 0 {
		t.Errorf("real pvecube cert covers its node name; expected no mismatch, got %+v", got)
	}

	// A certificate covering neither the address nor the node name is the
	// failure mode: pinned peer TLS has nothing to verify against.
	got := issuesByCheck(Evaluate(invBroken, facts, now, 0, nil), CheckSANMismatch)
	if len(got) != 1 {
		t.Fatalf("expected 1 cert_san_mismatch, got %d: %+v", len(got), got)
	}
	if got[0].Severity != SeverityError {
		t.Errorf("severity = %q, want %q", got[0].Severity, SeverityError)
	}
	for _, want := range []string{pvecubeAddr, "pvecube", "fails closed"} {
		if !strings.Contains(got[0].Detail, want) {
			t.Errorf("detail %q does not mention %q", got[0].Detail, want)
		}
	}
	if !strings.Contains(got[0].Remediation, "pvecm updatecerts") {
		t.Errorf("remediation %q should name the command that fixes it", got[0].Remediation)
	}
}

func TestSANMismatchSilentWhenAddressIsUnknown(t *testing.T) {
	// A single-node cluster has no peer address. Claiming a certificate fails
	// to cover an address we cannot name would be unfalsifiable.
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "solo", dnsNames: []string{"nothing-matching"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"solo": leaf})

	issues := Evaluate(inv, ClusterFacts{Members: []string{"solo"}}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, nil)
	if got := issuesByCheck(issues, CheckSANMismatch); len(got) != 0 {
		t.Errorf("expected silence with no dial address, got %+v", got)
	}
}

func TestExpiryChecks(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		notAfter  time.Time
		wantCheck string
	}{
		{"expired", now.Add(-48 * time.Hour), CheckExpired},
		{"expiring inside the window", now.Add(10 * 24 * time.Hour), CheckExpiring},
		{"healthy", now.Add(200 * 24 * time.Hour), ""},
		{"exactly at the boundary", now.Add(DefaultExpiryWarn), CheckExpiring},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			leaf, _ := issueLeaf(t, ca, leafOpts{
				cn: "n", dnsNames: []string{"n"},
				notBefore: now.Add(-365 * 24 * time.Hour), notAfter: tc.notAfter,
			})
			inv, _ := scanFixture(t, ca, map[string][]byte{"n": leaf})
			issues := Evaluate(inv, ClusterFacts{}, now, 0, nil)

			for _, check := range []string{CheckExpired, CheckExpiring} {
				got := issuesByCheck(issues, check)
				want := 0
				if check == tc.wantCheck {
					want = 1
				}
				if len(got) != want {
					t.Errorf("%s: got %d, want %d (%+v)", check, len(got), want, got)
				}
			}
			if tc.wantCheck != "" {
				got := issuesByCheck(issues, tc.wantCheck)[0]
				if got.Remediation == "" {
					t.Error("every issue must name its remediation")
				}
			}
		})
	}
}

func TestExpiryUsesConfiguredWindow(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "n", dnsNames: []string{"n"}, notBefore: now.Add(-time.Hour), notAfter: now.Add(60 * 24 * time.Hour)})
	inv, _ := scanFixture(t, ca, map[string][]byte{"n": leaf})

	if got := issuesByCheck(Evaluate(inv, ClusterFacts{}, now, 0, nil), CheckExpiring); len(got) != 0 {
		t.Errorf("60 days out is outside the 30-day default: %+v", got)
	}
	if got := issuesByCheck(Evaluate(inv, ClusterFacts{}, now, 90*24*time.Hour, nil), CheckExpiring); len(got) != 1 {
		t.Errorf("60 days out is inside a 90-day window, got %d", len(got))
	}
}

func TestWeakKeyCheck(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	weak, _ := issueLeaf(t, ca, leafOpts{cn: "weak", dnsNames: []string{"weak"}, keyBits: 1024})
	strong, _ := issueLeaf(t, ca, leafOpts{cn: "strong", dnsNames: []string{"strong"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"weak": weak, "strong": strong})

	got := issuesByCheck(Evaluate(inv, ClusterFacts{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, nil), CheckWeakKey)
	if len(got) != 1 {
		t.Fatalf("expected exactly one weak-key issue, got %d: %+v", len(got), got)
	}
	if got[0].Node != "weak" {
		t.Errorf("wrong node flagged: %q", got[0].Node)
	}
	if !strings.Contains(got[0].Detail, "1024") {
		t.Errorf("detail should name the key size: %q", got[0].Detail)
	}
}

func TestCAMismatchIsDistinguishedFromAFailedChain(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	other := newTestCA(t, "Some Other CA")

	// Different issuer name entirely -> cert_ca_mismatch, diagnosable from
	// names alone.
	foreign, _ := issueLeaf(t, other, leafOpts{cn: "pve2", dnsNames: []string{"pve2"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"pve2": foreign})
	issues := Evaluate(inv, ClusterFacts{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, nil)
	if got := issuesByCheck(issues, CheckCAMismatch); len(got) != 1 {
		t.Fatalf("expected cert_ca_mismatch, got %+v", issues)
	}
	if got := issuesByCheck(issues, CheckNotChained); len(got) != 0 {
		t.Errorf("a name mismatch should not also report cert_not_chained: %+v", got)
	}

	// Same issuer name, verification still fails -> cert_not_chained.
	sameName := newTestCA(t, "Proxmox Virtual Environment")
	impostor, _ := issueLeaf(t, sameName, leafOpts{cn: "pve3", dnsNames: []string{"pve3"}})
	inv2, root2 := scanFixture(t, ca, map[string][]byte{"pve3": impostor})
	verify := func(path string) error { return VerifyChain(root2, path, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) }
	issues2 := Evaluate(inv2, ClusterFacts{}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, verify)
	if got := issuesByCheck(issues2, CheckNotChained); len(got) != 1 {
		t.Fatalf("expected cert_not_chained, got %+v", issues2)
	}
	if got := issuesByCheck(issues2, CheckCAMismatch); len(got) != 0 {
		t.Errorf("issuer names match, so cert_ca_mismatch should stay quiet: %+v", got)
	}
}

func TestValidLeafRaisesNothing(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	leaf, _ := issueLeaf(t, ca, leafOpts{
		cn: "pve1.example.com", dnsNames: []string{"pve1", "pve1.example.com"}, ipSANs: []string{"10.0.0.1"},
		notBefore: now.Add(-24 * time.Hour), notAfter: now.Add(365 * 24 * time.Hour),
	})
	inv, root := scanFixture(t, ca, map[string][]byte{"pve1": leaf})
	verify := func(p string) error { return VerifyChain(root, p, now) }

	issues := Evaluate(inv,
		ClusterFacts{DialAddrs: map[string]string{"pve1": "10.0.0.1"}, Members: []string{"pve1"}},
		now, 0, verify)
	if len(issues) != 0 {
		t.Errorf("a healthy cluster should raise nothing, got %+v", issues)
	}
}

func TestMissingCertificateForAClusterMember(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"pve1": leaf})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issues := Evaluate(inv, ClusterFacts{Members: []string{"pve1", "pve2"}}, now, 0, nil)
	got := issuesByCheck(issues, CheckMissing)
	if len(got) != 1 || got[0].Node != "pve2" {
		t.Fatalf("expected cert_missing for pve2, got %+v", got)
	}

	// Without an authoritative member list this check has no standing.
	if got := issuesByCheck(Evaluate(inv, ClusterFacts{}, now, 0, nil), CheckMissing); len(got) != 0 {
		t.Errorf("no member list means no authority to claim absence: %+v", got)
	}
}

func TestCustomCertificateIsNotCheckedAgainstTheClusterCA(t *testing.T) {
	// A pveproxy-ssl.pem from Let's Encrypt is correct, and flagging it would
	// train operators to ignore the check.
	ca := newTestCA(t, "Proxmox Virtual Environment")
	public := newTestCA(t, "Public CA R3")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	custom, _ := issueLeaf(t, public, leafOpts{cn: "pve1.example.com", dnsNames: []string{"pve1.example.com"}})

	root := writeTree(t, ca, map[string][]byte{"pve1": leaf})
	writeFile(t, filepath.Join(root, nodesDir, "pve1", nodeCustomName), custom)

	inv, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	customPath := filepath.Join(root, nodesDir, "pve1", nodeCustomName)

	// The stub verifies for real, and additionally fails the test if it is
	// ever handed the custom certificate — so this asserts the custom cert is
	// never *submitted* for cluster-CA verification, not merely that no issue
	// happened to come out.
	var sawCustom bool
	verify := func(p string) error {
		if p == customPath {
			sawCustom = true
			return errors.New("custom certificate should never be chain-checked against the cluster CA")
		}
		return VerifyChain(root, p, now)
	}
	issues := Evaluate(inv, ClusterFacts{}, now, 0, verify)

	if sawCustom {
		t.Error("the custom certificate was submitted for cluster-CA verification")
	}
	for _, check := range []string{CheckCAMismatch, CheckNotChained} {
		if got := issuesByCheck(issues, check); len(got) != 0 {
			t.Errorf("%s fired on a custom certificate: %+v", check, got)
		}
	}

	// Control: the same stub, pointed at the node leaf, does run a real
	// verification — otherwise the assertion above would pass on a scan that
	// simply found no certificates.
	if _, ok := inv.LeafFor("pve1"); !ok {
		t.Fatal("control failed: the fixture's node leaf was not scanned at all")
	}
	var foundCustom bool
	for _, c := range inv.Certificates {
		if c.Kind == KindCustom {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatal("control failed: the custom certificate was not scanned, so nothing was exempted")
	}
}

func TestEveryIssueNamesARemediation(t *testing.T) {
	// A detection-only check without the command that fixes it leaves the
	// operator exactly where they started.
	ca := newTestCA(t, "Proxmox Virtual Environment")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	expired, _ := issueLeaf(t, ca, leafOpts{cn: "a", dnsNames: []string{"nope"}, keyBits: 1024, notBefore: now.Add(-72 * time.Hour), notAfter: now.Add(-time.Hour)})
	foreign, _ := issueLeaf(t, newTestCA(t, "Other"), leafOpts{cn: "b", dnsNames: []string{"b"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"a": expired, "b": foreign})

	issues := Evaluate(inv,
		ClusterFacts{DialAddrs: map[string]string{"a": "10.0.0.1", "b": "10.0.0.2"}, Members: []string{"a", "b", "c"}},
		now, 0, nil)
	if len(issues) < 4 {
		t.Fatalf("expected several issues from this fixture, got %d: %+v", len(issues), issues)
	}
	for _, i := range issues {
		if strings.TrimSpace(i.Remediation) == "" {
			t.Errorf("%s on %s has no remediation", i.Check, i.Node)
		}
		if strings.TrimSpace(i.Detail) == "" {
			t.Errorf("%s on %s has no detail", i.Check, i.Node)
		}
		if i.Severity != SeverityError && i.Severity != SeverityWarning {
			t.Errorf("%s has severity %q", i.Check, i.Severity)
		}
	}
}

func TestIssuesAreOrderedErrorsFirstAndDeterministically(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	weak, _ := issueLeaf(t, ca, leafOpts{cn: "z", dnsNames: []string{"z"}, keyBits: 1024})
	foreign, _ := issueLeaf(t, newTestCA(t, "Other"), leafOpts{cn: "a", dnsNames: []string{"a"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"z": weak, "a": foreign})

	first := Evaluate(inv, ClusterFacts{}, now, 0, nil)
	if len(first) < 2 {
		t.Fatalf("need at least two issues, got %+v", first)
	}
	if first[0].Severity != SeverityError {
		t.Errorf("errors should sort first, got %q", first[0].Severity)
	}
	for i := 0; i < 5; i++ {
		again := Evaluate(inv, ClusterFacts{}, now, 0, nil)
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("ordering is not deterministic at %d: %+v vs %+v", j, first[j], again[j])
			}
		}
	}
}
