// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"context"
	"errors"
	"net"
	"testing"
)

// T-2303 — resolving a verification name per peer.
//
// The problem, found on real hardware (T-1906-bug-01): peers are dialled by
// IP, and a real PVE node certificate does not reliably carry the node's
// current address as a SAN. Pinned verification against the dial IP therefore
// fails closed on a correctly configured cluster. These tests fix the shape of
// the fix — and, more importantly, fix the three properties that must survive
// it.

// The baseline this fix exists to change: a hostname-only certificate, dialled
// by IP, is refused. Asserted explicitly so the "works now" test below is
// demonstrably testing the fix rather than a condition that never failed.
func TestVerifyName_WithoutAResolverAHostnameOnlyCertIsRefused(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "pve2", "pve2.example.com"))

	trust, err := NewTrust(TrustOptions{CAFile: caPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	err = newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("baseline: a hostname-only certificate dialled by IP should be refused, got %v", err)
	}
}

// The fix: the same server, the same certificate, the same dial address — now
// verified against the resolved node name and accepted.
func TestVerifyName_ResolvedNameLetsAHostnameOnlyCertVerify(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "pve2", "pve2.example.com"))

	trust, err := NewTrust(TrustOptions{
		CAFile:     caPath,
		Logger:     discardLogger(),
		VerifyName: func(string) string { return "pve2" },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := newTLSClient(t, trust)
	if healthErr := client.Health(context.Background(), srv.peer("pve2")); healthErr != nil {
		t.Fatalf("with a resolved name this peer must verify: %v", healthErr)
	}
	v, err := client.Version(context.Background(), srv.peer("pve2"))
	if err != nil {
		t.Fatalf("Version over the named connection: %v", err)
	}
	if v.ProtocolVersion != 2 {
		t.Fatalf("protocolVersion = %d, want 2 (the body really came from the peer)", v.ProtocolVersion)
	}
}

// Adversarial 1: the CA pin must survive. A certificate for exactly the name
// we resolve, from a CA that is not the cluster's, is still refused.
func TestVerifyName_DoesNotWeakenTheCAPin(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")

	rogueCA := newTestCA(t, "rogue CA")
	srv := newTLSPeerServer(t, rogueCA.issue(t, "pve2", "pve2.example.com"))

	trust, err := NewTrust(TrustOptions{
		CAFile:     caPath,
		Logger:     discardLogger(),
		VerifyName: func(string) string { return "pve2" },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	err = newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("a certificate from a non-cluster CA must be refused whatever name it carries, got %v", err)
	}
	if hits := srv.hits.Load(); hits != 0 {
		t.Errorf("the request reached the rogue peer's handler %d times; it must be refused before any bytes are sent", hits)
	}
}

// Adversarial 2: name verification must survive. A certificate the cluster CA
// really did issue, but for a *different* node, is still refused — otherwise
// resolving a name would let any node impersonate any other, which is exactly
// what TestTrust_AC2_StillVerifiesTheHostname exists to prevent.
func TestVerifyName_StillRefusesAnotherNodesCertificate(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	// The server is pve3; we are dialling what we believe is pve2.
	srv := newTLSPeerServer(t, clusterCA.issue(t, "pve3", "pve3.example.com"))

	trust, err := NewTrust(TrustOptions{
		CAFile:     caPath,
		Logger:     discardLogger(),
		VerifyName: func(string) string { return "pve2" },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	err = newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("node pve3's certificate must not authenticate node pve2, got %v", err)
	}
	if hits := srv.hits.Load(); hits != 0 {
		t.Errorf("request reached the wrong node's handler %d times", hits)
	}
}

// An empty resolution must mean "verify against the dial address", never
// "verify against nothing".
func TestVerifyName_EmptyResolutionFallsBackToTheDialAddress(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "pve2"))

	trust, err := NewTrust(TrustOptions{
		CAFile:     caPath,
		Logger:     discardLogger(),
		VerifyName: func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	err = newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("an empty resolution must not disable verification, got %v", err)
	}
}

// The resolver is handed the dial host, not the whole address — a resolver
// keyed by "ip:port" would silently never match.
func TestVerifyName_ResolverSeesTheHostWithoutThePort(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "pve2"))

	wantHost, _, err := net.SplitHostPort(srv.addr())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}

	var seen string
	trust, err := NewTrust(TrustOptions{
		CAFile: caPath,
		Logger: discardLogger(),
		VerifyName: func(host string) string {
			seen = host
			return "pve2"
		},
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	if healthErr := newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2")); healthErr != nil {
		t.Fatalf("Health: %v", healthErr)
	}
	if seen != wantHost {
		t.Errorf("resolver saw %q, want the bare host %q", seen, wantHost)
	}
}

// Replacing the resolver must not leave pooled connections verified under the
// old mapping still serving requests.
func TestVerifyName_ReplacingTheResolverRetiresPooledConnections(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "pve2"))

	trust, err := NewTrust(TrustOptions{
		CAFile:     caPath,
		Logger:     discardLogger(),
		VerifyName: func(string) string { return "pve2" },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	client := newTLSClient(t, trust)
	if healthErr := client.Health(context.Background(), srv.peer("pve2")); healthErr != nil {
		t.Fatalf("first call should succeed: %v", healthErr)
	}

	// Now the inventory says this address should be verified as a name the
	// certificate does not carry. The next call must fail, not ride the
	// connection established under the previous mapping.
	trust.SetVerifyNameResolver(func(string) string { return "pve9" })
	err = client.Health(context.Background(), srv.peer("pve2"))
	if !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("after the mapping changed, the old connection must not be reused: %v", err)
	}
}

// The escape hatches do not resolve names: in system mode the host pool
// decides, and in insecure mode nothing is verified at all, so overriding the
// name would change what is checked without changing whether it is.
func TestVerifyName_IgnoredInUnpinnedModes(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")
	srv := newTLSPeerServer(t, clusterCA.issue(t, "127.0.0.1"))

	trust, err := NewTrust(TrustOptions{
		Mode:       TrustInsecure,
		Ack:        AckInsecure,
		CAFile:     caPath,
		Logger:     discardLogger(),
		VerifyName: func(string) string { return "a-name-the-cert-does-not-have" },
	})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	if healthErr := newTLSClient(t, trust).Health(context.Background(), srv.peer("pve2")); healthErr != nil {
		t.Fatalf("insecure mode verifies nothing, so the name must be irrelevant: %v", healthErr)
	}
}
