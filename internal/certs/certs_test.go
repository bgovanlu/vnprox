// SPDX-License-Identifier: Apache-2.0

package certs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanReadsClusterCAAndNodeLeaves(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leafA, _ := issueLeaf(t, ca, leafOpts{cn: "pve1.example.com", dnsNames: []string{"pve1", "pve1.example.com"}})
	leafB, _ := issueLeaf(t, ca, leafOpts{cn: "pve2.example.com", dnsNames: []string{"pve2"}, ipSANs: []string{"10.0.0.2"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": leafA, "pve2": leafB})

	inv, err := Scan(Options{Root: root, Now: func() time.Time { return time.Unix(0, 0) }})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(inv.Errors) != 0 {
		t.Fatalf("unexpected scan errors: %+v", inv.Errors)
	}
	if inv.ClusterCA == nil {
		t.Fatal("cluster CA not found")
	}
	if !inv.ClusterCA.IsCA || !inv.ClusterCA.SelfSigned {
		t.Errorf("cluster CA: IsCA=%v SelfSigned=%v, want both true", inv.ClusterCA.IsCA, inv.ClusterCA.SelfSigned)
	}
	if got := inv.Nodes(); len(got) != 2 || got[0] != "pve1" || got[1] != "pve2" {
		t.Errorf("Nodes() = %v, want [pve1 pve2]", got)
	}

	leaf, ok := inv.LeafFor("pve2")
	if !ok {
		t.Fatal("no leaf for pve2")
	}
	if leaf.Kind != KindNodeLeaf {
		t.Errorf("kind = %q, want %q", leaf.Kind, KindNodeLeaf)
	}
	if leaf.KeyAlgorithm != "RSA" || leaf.KeyBits != 2048 {
		t.Errorf("key = %s/%d, want RSA/2048", leaf.KeyAlgorithm, leaf.KeyBits)
	}
	if len(leaf.Fingerprint) != 64 {
		t.Errorf("fingerprint = %q, want 64 hex chars", leaf.Fingerprint)
	}
	if got := leaf.DNSNames(); len(got) != 1 || got[0] != "pve2" {
		t.Errorf("DNSNames() = %v", got)
	}
	if got := leaf.IPAddresses(); len(got) != 1 || got[0] != "10.0.0.2" {
		t.Errorf("IPAddresses() = %v", got)
	}
}

// The whole safety argument of this package: keys live next to certificates in
// pmxcfs, and nothing here may touch them.
func TestScanNeverReadsPrivateKeys(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, keyPEM := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": leaf})

	// Plant the real key exactly where PVE puts it, plus a recognisable
	// marker inside a second key file, so a leak would be findable by content
	// as well as by filename.
	const marker = "SUPER-SECRET-KEY-MATERIAL-DO-NOT-LEAK"
	keyPath := filepath.Join(root, nodesDir, "pve1", "pve-ssl.key")
	writeFile(t, keyPath, keyPEM)
	writeFile(t, filepath.Join(root, nodesDir, "pve1", "pveproxy-ssl.key"), []byte(marker))
	writeFile(t, filepath.Join(root, "pve-www.key"), []byte(marker))

	// Control: prove this test could detect a read at all. If the scanner
	// were to open the key file, its atime would be a weak signal on many
	// filesystems — so instead assert on content, and first assert the marker
	// is genuinely present on disk, so a "not found" below means something.
	planted, err := os.ReadFile(filepath.Join(root, nodesDir, "pve1", "pveproxy-ssl.key"))
	if err != nil || !strings.Contains(string(planted), marker) {
		t.Fatalf("control failed: marker not present on disk (err=%v)", err)
	}

	inv, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	blob := renderInventory(t, inv)
	if strings.Contains(blob, marker) {
		t.Error("scan output contains planted key marker")
	}
	if strings.Contains(blob, "PRIVATE KEY") {
		t.Error("scan output contains a PRIVATE KEY block")
	}
	for _, c := range inv.Certificates {
		if strings.HasSuffix(c.Path, ".key") {
			t.Errorf("scan produced a record for a key file: %s", c.Path)
		}
	}
	for _, e := range inv.Errors {
		if strings.HasSuffix(e.Path, ".key") {
			t.Errorf("scan reported an error for a key file (so it opened one): %s", e.Path)
		}
	}
}

// renderInventory flattens every string an Inventory can carry, so a leak in
// any field is caught rather than only the ones a test thought to check.
func renderInventory(t *testing.T, inv Inventory) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range inv.Certificates {
		sb.WriteString(c.Subject + "\n" + c.Issuer + "\n" + c.Path + "\n" + c.Serial + "\n")
		sb.WriteString(c.Fingerprint + "\n" + c.KeyAlgorithm + "\n" + c.SignatureAlgorithm + "\n")
		for _, s := range c.SANs {
			sb.WriteString(s.Type + ":" + s.Value + "\n")
		}
	}
	for _, e := range inv.Errors {
		sb.WriteString(e.Path + "\n" + e.Error + "\n" + e.Node + "\n")
	}
	return sb.String()
}

func TestScanIsReadOnly(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, keyPEM := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": leaf})
	writeFile(t, filepath.Join(root, nodesDir, "pve1", "pve-ssl.key"), keyPEM)

	before := snapshotTree(t, root)
	if _, err := Scan(Options{Root: root}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	after := snapshotTree(t, root)

	if len(before) != len(after) {
		t.Fatalf("file count changed: %d -> %d", len(before), len(after))
	}
	for path, size := range before {
		if after[path] != size {
			t.Errorf("%s changed size: %d -> %d", path, size, after[path])
		}
	}
}

func snapshotTree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			out[p] = info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}

func TestScanReportsMalformedPEMWithoutFailingTheWholeScan(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	good, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": good, "pve2": []byte("-----BEGIN CERTIFICATE-----\ntruncated\n")})

	inv, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan should not fail outright: %v", err)
	}
	if _, ok := inv.LeafFor("pve1"); !ok {
		t.Error("the good node's certificate should still be present")
	}
	if _, ok := inv.LeafFor("pve2"); ok {
		t.Error("a malformed file must not produce a partial record")
	}
	if len(inv.Errors) == 0 {
		t.Fatal("expected a per-file error for the malformed certificate")
	}
	var found bool
	for _, e := range inv.Errors {
		if strings.Contains(e.Path, "pve2") {
			found = true
		}
	}
	if !found {
		t.Errorf("errors do not mention pve2: %+v", inv.Errors)
	}
}

func TestScanTolerantOfMissingNodesDirectory(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	root := t.TempDir()
	writeFile(t, filepath.Join(root, clusterCAName), ca.pem)

	inv, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if inv.ClusterCA == nil {
		t.Error("cluster CA should still be reported without a nodes/ directory")
	}
}

func TestCoversMatchesCryptoTLSSemantics(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	// The trailing dot is not a typo — real pve-ssl.pem carries
	// DNS:pvecube.localdomain. with a root dot.
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "pvecube.localdomain.", dnsNames: pvecubeDNSSANs, ipSANs: pvecubeIPSANs})
	root := writeTree(t, ca, map[string][]byte{"pvecube": leaf})
	inv, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	cert, ok := inv.LeafFor("pvecube")
	if !ok {
		t.Fatal("no leaf")
	}

	tests := []struct {
		name string
		want bool
	}{
		{"pvecube", true},
		{"PVECUBE", true},                // case-insensitive
		{"pvecube.localdomain", true},    // trailing dot stripped from the SAN
		{"pvecube.localdomain.", true},   // and from the query
		{"192.168.100.99", true},         // stale, but genuinely present
		{"127.0.0.1", true},              //
		{"::1", true},                    //
		{"0:0:0:0:0:0:0:1", true},        // same address, different literal
		{pvecubeAddr, false},             // the node's ACTUAL address — absent
		{"pvecube.example.com", false},   //
		{"other", false},                 //
		{"", false},                      //
		{"localhost.localdomain", false}, // not a SAN
	}
	for _, tc := range tests {
		if got := cert.Covers(tc.name); got != tc.want {
			t.Errorf("Covers(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCoversDoesNotMatchAnIPAgainstADNSSAN(t *testing.T) {
	// A DNS SAN that looks like an address must not authenticate that address
	// — crypto/x509 does not conflate them, and neither may this.
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "n", dnsNames: []string{"10.0.0.5"}})
	root := writeTree(t, ca, map[string][]byte{"n": leaf})
	inv, _ := Scan(Options{Root: root})
	cert, _ := inv.LeafFor("n")

	if cert.Covers("10.0.0.5") {
		t.Error("an IP literal must not match a DNS SAN of the same text")
	}
}

func TestCoversWildcardMatchesOneLabelOnly(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "wild", dnsNames: []string{"*.example.com"}})
	root := writeTree(t, ca, map[string][]byte{"wild": leaf})
	inv, _ := Scan(Options{Root: root})
	cert, _ := inv.LeafFor("wild")

	for name, want := range map[string]bool{
		"a.example.com":   true,
		"example.com":     false,
		"a.b.example.com": false,
	} {
		if got := cert.Covers(name); got != want {
			t.Errorf("Covers(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestVerifyChainAcceptsOwnCAAndRejectsAnother(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	other := newTestCA(t, "Someone Else")
	mine, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	theirs, _ := issueLeaf(t, other, leafOpts{cn: "pve2", dnsNames: []string{"pve2"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": mine, "pve2": theirs})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := VerifyChain(root, filepath.Join(root, nodesDir, "pve1", nodeLeafName), now); err != nil {
		t.Errorf("own CA should verify: %v", err)
	}
	if err := VerifyChain(root, filepath.Join(root, nodesDir, "pve2", nodeLeafName), now); err == nil {
		t.Error("a leaf from a different CA must not verify")
	}
}

func TestDaemonCertNotDuplicatedWhenItIsTheNodeLeaf(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": leaf})
	leafPath := filepath.Join(root, nodesDir, "pve1", nodeLeafName)

	inv, err := Scan(Options{Root: root, DaemonCertPath: leafPath, LocalNode: "pve1"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var daemon int
	for _, c := range inv.Certificates {
		if c.Kind == KindDaemon {
			daemon++
		}
	}
	if daemon != 0 {
		t.Errorf("daemon cert reported %d times; it is the node leaf and should not be duplicated", daemon)
	}
}

func TestDaemonCertReportedWhenItIsAnOverride(t *testing.T) {
	ca := newTestCA(t, "Proxmox Virtual Environment")
	leaf, _ := issueLeaf(t, ca, leafOpts{cn: "pve1", dnsNames: []string{"pve1"}})
	root := writeTree(t, ca, map[string][]byte{"pve1": leaf})

	override, _ := issueLeaf(t, ca, leafOpts{cn: "override", dnsNames: []string{"override"}})
	overridePath := filepath.Join(t.TempDir(), "custom.pem")
	writeFile(t, overridePath, override)

	inv, err := Scan(Options{Root: root, DaemonCertPath: overridePath, LocalNode: "pve1"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var found bool
	for _, c := range inv.Certificates {
		if c.Kind == KindDaemon && c.Subject == "override" {
			found = true
		}
	}
	if !found {
		t.Error("an explicitly overridden daemon certificate should be reported")
	}
}
