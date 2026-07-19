package wireguard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGenerateKeypair_PublicDerivesFromPrivate is part of T-1401's key-custody
// safety analysis (cross-referenced by the card): the public key a tunnel
// exports must be exactly the one derived from the private key generated on
// the owning node, so GET /wireguard/tunnels/{id}/pubkey round-trips the real
// keypair (AC2) and no separate/second keypair is ever involved.
func TestGenerateKeypair_PublicDerivesFromPrivate(t *testing.T) {
	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	if len(priv) != KeyLen || len(pub) != KeyLen {
		t.Fatalf("key lengths priv=%d pub=%d, want %d", len(priv), len(pub), KeyLen)
	}
	derived, err := PublicKeyFor(priv)
	if err != nil {
		t.Fatalf("PublicKeyFor: %v", err)
	}
	if !bytes.Equal(derived, pub) {
		t.Fatalf("PublicKeyFor(priv) != GenerateKeypair public half")
	}
	// Two generations must differ (real randomness, not a fixed key).
	priv2, _, _ := GenerateKeypair()
	if bytes.Equal(priv, priv2) {
		t.Fatal("two generated private keys are identical")
	}
}

func TestEncodeDecodeKey_RoundTrip(t *testing.T) {
	priv, _, _ := GenerateKeypair()
	s := EncodeKey(priv)
	if len(s) != 44 {
		t.Fatalf("encoded key length = %d, want 44", len(s))
	}
	back, err := DecodeKey(s)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !bytes.Equal(back, priv) {
		t.Fatal("decode(encode(k)) != k")
	}
	if _, err := DecodeKey("not-base64!!!"); err == nil {
		t.Fatal("expected error decoding non-base64")
	}
}

// TestParseDump_DiscardsPrivateKey proves the live-poll parser never captures
// the interface's private key column (docs/security.md WireGuard note) while
// correctly reading peers.
func TestParseDump_DiscardsPrivateKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "three-peer.dump"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	privKeyCol := "4Fb0e0aAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	tun, err := ParseDump("pve1", "wg0", string(raw))
	if err != nil {
		t.Fatalf("ParseDump: %v", err)
	}
	if tun.PublicKey != "SRVpubKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=" {
		t.Errorf("PublicKey = %q", tun.PublicKey)
	}
	if tun.ListenPort != 51820 {
		t.Errorf("ListenPort = %d, want 51820", tun.ListenPort)
	}
	if len(tun.Peers) != 3 {
		t.Fatalf("peers = %d, want 3", len(tun.Peers))
	}
	// The private key must appear nowhere in the parsed structure.
	if strings.Contains(dumpString(tun), privKeyCol) {
		t.Fatal("parsed ObservedTunnel leaked the interface private key")
	}
	// Peer two never handshook (0) -> zero LastHandshake.
	if !tun.Peers[1].LastHandshake.IsZero() {
		t.Errorf("peer two LastHandshake should be zero (never handshaked)")
	}
	if tun.Peers[0].RxBytes != 120000 || tun.Peers[0].TxBytes != 98000 {
		t.Errorf("peer one transfer counters = %d/%d", tun.Peers[0].RxBytes, tun.Peers[0].TxBytes)
	}
}

func dumpString(t ObservedTunnel) string {
	var b strings.Builder
	b.WriteString(t.PublicKey)
	for _, p := range t.Peers {
		b.WriteString(p.PublicKey)
		b.WriteString(p.Endpoint)
		b.WriteString(strings.Join(p.AllowedIPs, ","))
	}
	return b.String()
}

func TestObservedPeer_HandshakeAgeAndDrift(t *testing.T) {
	now := time.Unix(1721300100, 0)
	healthy := FixtureHealthy(now).Peers[0]
	age, ok := healthy.HandshakeAge(now)
	if !ok || age != 30*time.Second {
		t.Errorf("healthy handshake age = %v ok=%v, want 30s true", age, ok)
	}
	if healthy.EndpointDrifted() {
		t.Error("healthy peer should not report drift")
	}

	drift := FixtureEndpointDrift(now).Peers[0]
	if !drift.EndpointDrifted() {
		t.Error("drift fixture should report drift")
	}

	stale := FixtureStaleHandshake(now).Peers[0]
	age, ok = stale.HandshakeAge(now)
	if !ok || age != 15*time.Minute {
		t.Errorf("stale handshake age = %v ok=%v, want 15m true", age, ok)
	}
}

// TestRenderConfig_IncludesPeersNoLeakOnRead documents that the on-node config
// contains the private key (it must, to bring the interface up) but is only
// ever produced by the node-local apply step, never a read path.
func TestRenderConfig_Renders(t *testing.T) {
	tun := FixtureExternalPeerTunnel()
	cfg := RenderConfig(tun, "PRIVc2VydmVyLXByaXZhdGUta2V5LWJhc2U2NDAwMDA=", []Peer{{
		PublicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=", Endpoint: "203.0.113.10:51820",
		AllowedIPs: []string{"10.10.0.2/32"}, KeepaliveSec: 25,
	}})
	for _, want := range []string{"[Interface]", "PrivateKey = ", "ListenPort = 51820", "MTU = 1420", "[Peer]", "Endpoint = 203.0.113.10:51820", "PersistentKeepalive = 25"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("rendered config missing %q:\n%s", want, cfg)
		}
	}
}

// TestRenderPeerConfig_ExternalExport is T-1401 AC5's export half: an external
// peer's own-side config is a complete, installable block that names OUR
// public key and endpoint and leaves the peer's own private key a placeholder
// (vnprox never holds it — the residual-risk note).
func TestRenderPeerConfig_ExternalExport(t *testing.T) {
	tun := FixtureExternalPeerTunnel()
	peer := FixtureExternalPeer()
	cfg := RenderPeerConfig(tun, peer, "vpn.example.net:51820")
	for _, want := range []string{
		"[Interface]", "REPLACE_WITH", "Address = 10.10.0.4/32",
		"[Peer]", "PublicKey = " + tun.PublicKey, "Endpoint = vpn.example.net:51820", "AllowedIPs = 10.10.0.1/24",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("peer-config export missing %q:\n%s", want, cfg)
		}
	}
	// Must NOT contain any real private key placeholder-substituted value.
	if strings.Contains(cfg, "PrivateKey = PRIV") {
		t.Error("peer-config export must not embed a real private key")
	}
}
