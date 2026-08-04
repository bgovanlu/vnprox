package peer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testCA is a throwaway certificate authority built in-process. Nothing here
// assumes anything about a *real* PVE chain's shape beyond what
// docs/architecture.md §9 documents (peers serve the node's PVE certificate,
// issued by the cluster's own root CA) — see planning/reports/T-1906.md for
// what that leaves needing hardware validation.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, commonName string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}
	return &testCA{
		cert: cert,
		key:  key,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// issue mints a server certificate for hosts (IPs or DNS names), signed by ca.
func (ca *testCA) issue(t *testing.T, hosts ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(t),
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("creating leaf certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key}
}

// writePEM writes ca's certificate to a file under dir and returns the path.
func (ca *testCA) writePEM(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, ca.pem, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generating serial: %v", err)
	}
	return n
}

// tlsPeerServer is a stand-in peer daemon: an HTTPS listener on 127.0.0.1
// serving a given certificate, counting every request that actually reached
// the handler. The count is what proves a rejected peer was rejected *before*
// anything was sent, not after.
type tlsPeerServer struct {
	srv  *httptest.Server
	hits atomic.Int64
}

func newTLSPeerServer(t *testing.T, cert tls.Certificate) *tlsPeerServer {
	t.Helper()
	s := &tlsPeerServer{}
	s.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"protocolVersion":2}`))
	}))
	s.srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	// Rejected handshakes are the *expected* outcome in most of these tests;
	// letting net/http log each one to stderr buries the real test output.
	s.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.srv.StartTLS()
	t.Cleanup(s.srv.Close)
	return s
}

func (s *tlsPeerServer) addr() string { return s.srv.Listener.Addr().String() }

func (s *tlsPeerServer) peer(node string) Peer { return Peer{Node: node, Addr: s.addr()} }

// newTLSClient builds a peer.Client that really dials https, with trust as its
// anchor.
func newTLSClient(t *testing.T, trust *Trust) *Client {
	t.Helper()
	return NewClient(ClientOptions{
		Secrets:                 newStaticSecretStore(testSecret),
		Trust:                   trust,
		Logger:                  discardLogger(),
		Scheme:                  "https",
		RequestTimeout:          5 * time.Second,
		BreakerFailureThreshold: 100,
	})
}

// logCapture is a slog handler that records every emitted record, so a test
// can assert on the *presence and level* of the escape-hatch warning rather
// than on a side effect of it.
type logCapture struct {
	records []slog.Record
	mu      sync.Mutex
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) logger() *slog.Logger { return slog.New(c) }

// at returns every captured record at exactly level.
func (c *logCapture) at(level slog.Level) []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []slog.Record
	for _, r := range c.records {
		if r.Level == level {
			out = append(out, r)
		}
	}
	return out
}
