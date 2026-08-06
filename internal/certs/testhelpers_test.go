package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA is a throwaway CA for building fixture trees. Keys are 2048-bit
// because these tests generate several per run and anything larger makes the
// suite noticeably slow for no added coverage — the weak-key check is
// exercised with an explicitly small key where it matters.
type testCA struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, cn string) *testCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"PVE Cluster Manager CA"}},
		NotBefore:             time.Date(2023, 10, 24, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2033, 10, 21, 0, 0, 0, 0, time.UTC),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing CA cert: %v", err)
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

type leafOpts struct {
	notBefore time.Time
	notAfter  time.Time
	cn        string
	dnsNames  []string
	ipSANs    []string
	keyBits   int
}

// issueLeaf signs a leaf with ca and returns its PEM plus the private key PEM.
// The key is returned so tests can plant it next to the certificate and assert
// the scanner never touches it.
func issueLeaf(t *testing.T, ca *testCA, o leafOpts) (certPEM, keyPEM []byte) {
	t.Helper()
	bits := o.keyBits
	if bits == 0 {
		bits = 2048
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	nb, na := o.notBefore, o.notAfter
	if nb.IsZero() {
		nb = time.Date(2025, 10, 9, 0, 0, 0, 0, time.UTC)
	}
	if na.IsZero() {
		na = time.Date(2027, 10, 9, 0, 0, 0, 0, time.UTC)
	}
	var ips []net.IP
	for _, s := range o.ipSANs {
		ips = append(ips, net.ParseIP(s))
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: o.cn, Organization: []string{"Proxmox Virtual Environment"}},
		NotBefore:    nb,
		NotAfter:     na,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     o.dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("creating leaf cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// pvecubeSANs is the SAN set read off the real hardware this phase was
// designed against (pve-manager/9.2.4), recorded in planning/tasks/phase-23.md.
// The node's actual address is 192.168.1.9; 192.168.100.99 is a stale address
// it no longer has. Held verbatim so the cert_san_mismatch check has a
// regression test against the exact case that motivated it, rather than a
// tidied-up approximation of it.
var (
	pvecubeDNSSANs = []string{"localhost", "pvecube", "pvecube.localdomain."}
	pvecubeIPSANs  = []string{"127.0.0.1", "::1", "192.168.100.99"}
	pvecubeAddr    = "192.168.1.9"
)

// writeTree lays out a pmxcfs-shaped fixture root.
func writeTree(t *testing.T, ca *testCA, nodes map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	if ca != nil {
		writeFile(t, filepath.Join(root, clusterCAName), ca.pem)
	}
	for node, certPEM := range nodes {
		dir := filepath.Join(root, nodesDir, node)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if certPEM != nil {
			writeFile(t, filepath.Join(dir, nodeLeafName), certPEM)
		}
	}
	return root
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
