package certs

import "testing"

func leafFor(t *testing.T, ca *testCA, node string, o leafOpts) Certificate {
	t.Helper()
	pemBytes, _ := issueLeaf(t, ca, o)
	inv, _ := scanFixture(t, ca, map[string][]byte{node: pemBytes})
	cert, ok := inv.LeafFor(node)
	if !ok {
		t.Fatalf("no leaf for %s", node)
	}
	return cert
}

// The motivating case, verbatim from hardware: the certificate carries the
// node name but a stale IP. Dialling by IP must still verify.
func TestResolveVerifyNamePrefersTheNodeNameOverAStaleIP(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	cert := leafFor(t, ca, "pvecube", leafOpts{cn: "pvecube.localdomain.", dnsNames: pvecubeDNSSANs, ipSANs: pvecubeIPSANs})

	name, covered := ResolveVerifyName(cert, "pvecube", pvecubeAddr)
	if !covered {
		t.Fatal("the node name is a SAN; this must resolve")
	}
	if name != "pvecube" {
		t.Errorf("name = %q, want %q", name, "pvecube")
	}
	if !cert.Covers(name) {
		t.Errorf("resolved name %q is not actually covered by the certificate", name)
	}
}

func TestResolveVerifyNameFallsBackToAnFQDNRootedAtTheNodeName(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	// No bare node-name SAN — only the FQDN form, which real deployments do
	// produce when the search domain is set.
	cert := leafFor(t, ca, "pve1", leafOpts{cn: "pve1.example.com", dnsNames: []string{"pve1.example.com"}})

	name, covered := ResolveVerifyName(cert, "pve1", "10.0.0.1")
	if !covered || name != "pve1.example.com" {
		t.Fatalf("ResolveVerifyName = (%q, %v), want (pve1.example.com, true)", name, covered)
	}
}

func TestResolveVerifyNameUsesTheDialAddressWhenTheCertificateCoversIt(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	cert := leafFor(t, ca, "pve1", leafOpts{cn: "pve1", ipSANs: []string{"10.0.0.1"}})

	name, covered := ResolveVerifyName(cert, "pve1", "10.0.0.1")
	if !covered || name != "10.0.0.1" {
		t.Fatalf("ResolveVerifyName = (%q, %v), want (10.0.0.1, true)", name, covered)
	}
}

// Adversarial: node A's certificate must never authenticate node B, even
// though the cluster CA issued both. This is the property that makes deriving
// a ServerName safe at all.
func TestResolveVerifyNameNeverBorrowsAnotherNodesIdentity(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	// A certificate for pve1 that also happens to carry pve2's name — the
	// shape an attacker with a legitimately-issued certificate would want.
	cert := leafFor(t, ca, "pve1", leafOpts{cn: "pve1", dnsNames: []string{"pve1", "pve2", "pve2.example.com"}})

	// Asking for pve2 while holding what pmxcfs says is pve2's certificate is
	// the legitimate path — but the candidate name must come from the node
	// name we were given, so resolving for pve2 yields pve2, which the real
	// pve2 certificate would then have to actually carry.
	name, covered := ResolveVerifyName(cert, "pve2", "10.0.0.2")
	if !covered || name != "pve2" {
		t.Fatalf("ResolveVerifyName = (%q, %v), want (pve2, true)", name, covered)
	}
	// The resolved name is never read out of the certificate's SAN list
	// independently of the requested node: asking for a node the certificate
	// does not name yields no coverage at all, whatever else it carries.
	name, covered = ResolveVerifyName(cert, "pve9", "10.0.0.9")
	if covered {
		t.Errorf("a certificate naming pve1/pve2 must not cover pve9, got %q", name)
	}
	if name != "10.0.0.9" {
		t.Errorf("uncovered resolution should fall back to the dial address, got %q", name)
	}
}

// Adversarial: a wildcard SAN must not originate a candidate name, or one
// node's certificate would authenticate every node in the domain.
func TestResolveVerifyNameDoesNotOriginateFromAWildcard(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	cert := leafFor(t, ca, "pve1", leafOpts{cn: "wild", dnsNames: []string{"*.example.com"}})

	// The wildcard *does* cover pve1.example.com, so rule 1's direct
	// Covers(node) check is what must fail here (node name "pve1" has no dot
	// and cannot match a wildcard), and rule 2 must not manufacture
	// "pve1.example.com" out of the wildcard.
	name, covered := ResolveVerifyName(cert, "pve1", "10.0.0.1")
	if covered {
		t.Errorf("a wildcard must not originate a candidate name, got %q", name)
	}
}

func TestResolveVerifyNameReportsUncoveredSoTheDaemonCanWarnEarly(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	cert := leafFor(t, ca, "pve1", leafOpts{cn: "old", dnsNames: []string{"old-name"}, ipSANs: []string{"192.168.100.99"}})

	name, covered := ResolveVerifyName(cert, "pve1", "10.0.0.1")
	if covered {
		t.Fatal("nothing here covers pve1 or 10.0.0.1")
	}
	// Falling back to the dial address means crypto/tls produces its own
	// specific hostname error rather than this package inventing one.
	if name != "10.0.0.1" {
		t.Errorf("name = %q, want the dial address", name)
	}
}

func TestVerifyNamesBuildsTheDialHostMapping(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	one, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	two, _ := issueLeaf(t, ca, leafOpts{cn: "pve2", ipSANs: []string{"10.0.0.2"}})
	inv, _ := scanFixture(t, ca, map[string][]byte{"pve1": one, "pve2": two})

	got := VerifyNames(inv, ClusterFacts{DialAddrs: map[string]string{
		"pve1": "10.0.0.1",
		"pve2": "10.0.0.2",
		"pve3": "10.0.0.3", // no certificate on record
	}})

	want := map[string]string{
		"10.0.0.1": "pve1",     // name preferred over the address
		"10.0.0.2": "10.0.0.2", // address genuinely covered
		"10.0.0.3": "10.0.0.3", // unknown node: identity is the address
	}
	for addr, wantName := range want {
		if got[addr] != wantName {
			t.Errorf("VerifyNames()[%q] = %q, want %q", addr, got[addr], wantName)
		}
	}
}
