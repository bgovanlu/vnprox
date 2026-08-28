// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCertFixture lays out a minimal pmxcfs-shaped tree with a CA and one
// node leaf, plus a private key planted where PVE really puts it.
func writeCertFixture(t *testing.T, notAfter time.Time, dnsNames []string) string {
	t.Helper()
	root := t.TempDir()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Proxmox Virtual Environment"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	writeFixtureFile(t, filepath.Join(root, "pve-root-ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     notAfter,
		DNSNames:     dnsNames,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	nodeDir := filepath.Join(root, "nodes", "pve1")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFixtureFile(t, filepath.Join(nodeDir, "pve-ssl.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	writeFixtureFile(t, filepath.Join(nodeDir, "pve-ssl.key"),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}))
	return root
}

func writeFixtureFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCertsCommandPrintsInventoryAndExitsZeroWhenHealthy(t *testing.T) {
	root := writeCertFixture(t, time.Now().Add(365*24*time.Hour), []string{"pve1", "pve1.example.com"})

	var stdout, stderr bytes.Buffer
	code := runCerts([]string{"--root", root}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitSuccess, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"cluster CA", "Proxmox Virtual Environment", "pve1", "no certificate problems found"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCertsCommandNeverPrintsKeyMaterial(t *testing.T) {
	root := writeCertFixture(t, time.Now().Add(365*24*time.Hour), []string{"pve1"})

	// Control: the key really is on disk next to the certificate, so a
	// negative result below means something.
	keyBytes, err := os.ReadFile(filepath.Join(root, "nodes", "pve1", "pve-ssl.key"))
	if err != nil || !strings.Contains(string(keyBytes), "PRIVATE KEY") {
		t.Fatalf("control failed: no key planted (err=%v)", err)
	}

	for _, args := range [][]string{{"--root", root}, {"--root", root, "--json"}} {
		var stdout, stderr bytes.Buffer
		runCerts(args, &stdout, &stderr)
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, "PRIVATE KEY") {
			t.Errorf("%v: output contains a PRIVATE KEY block", args)
		}
		// A couple of lines of the actual key body, in case a future change
		// emitted base64 without the PEM header.
		body := strings.Split(string(keyBytes), "\n")
		for _, line := range body {
			if len(line) > 40 && strings.Contains(combined, line) {
				t.Errorf("%v: output contains a line of the private key", args)
				break
			}
		}
	}
}

func TestCertsCommandExitsNonZeroOnABlockingProblem(t *testing.T) {
	// An expired certificate is an error-level problem.
	root := writeCertFixture(t, time.Now().Add(-24*time.Hour), []string{"pve1"})

	var stdout, stderr bytes.Buffer
	code := runCerts([]string{"--root", root}, &stdout, &stderr)
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout.String(), "cert_expired") {
		t.Errorf("output should name the failing check:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "fix:") {
		t.Errorf("output should name the remediation:\n%s", stdout.String())
	}
}

func TestCertsCommandExitsZeroForWarningsAlone(t *testing.T) {
	// Expiring soon, but not yet expired: a warning, and warnings must not
	// fail a script — otherwise operators stop running the check.
	root := writeCertFixture(t, time.Now().Add(10*24*time.Hour), []string{"pve1"})

	var stdout, stderr bytes.Buffer
	code := runCerts([]string{"--root", root}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d for a warning-only run", code, ExitSuccess)
	}
	if !strings.Contains(stdout.String(), "cert_expiring") {
		t.Errorf("expected cert_expiring in:\n%s", stdout.String())
	}
}

func TestCertsCommandJSONIsWellFormed(t *testing.T) {
	root := writeCertFixture(t, time.Now().Add(365*24*time.Hour), []string{"pve1"})

	var stdout, stderr bytes.Buffer
	if code := runCerts([]string{"--root", root, "--json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	var got struct {
		Inventory struct {
			Certificates []struct {
				Kind    string `json:"kind"`
				Subject string `json:"subject"`
			} `json:"certificates"`
		} `json:"inventory"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Inventory.Certificates) < 2 {
		t.Errorf("expected the CA and at least one leaf, got %d", len(got.Inventory.Certificates))
	}
	assertDocumentedJSON(t, "certs", stdout.Bytes())
}

// TestCertsCommand_OJSONFlagAlsoWorks pins T-4011's `-o json` retrofit
// alongside the pre-existing `--json` (docs/api.md: either selects JSON).
func TestCertsCommand_OJSONFlagAlsoWorks(t *testing.T) {
	root := writeCertFixture(t, time.Now().Add(365*24*time.Hour), []string{"pve1"})
	var stdout, stderr bytes.Buffer
	if code := runCerts([]string{"--root", root, "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("`-o json` output is not valid JSON:\n%s", stdout.String())
	}
	assertDocumentedJSON(t, "certs", stdout.Bytes())
}

func TestCertsCommandRejectsBadFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCerts([]string{"--nope"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
}
