package main

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

func wgTestGateway(t *testing.T) (*hostWGGateway, *store.WireGuardRepo, *store.SessionCipher) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key := make([]byte, store.KeySize)
	if _, randErr := rand.Read(key); randErr != nil {
		t.Fatalf("rand: %v", randErr)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	repo := store.NewWireGuardRepo(db)
	gw := newHostWGGateway(repo, cipher, func() string { return "pve1" }, testLogger())
	// Capture the written config in memory instead of touching /etc/wireguard,
	// and make the wg-quick/wg exec no-ops (no live kernel module in tests).
	written := map[string]string{}
	gw.confDir = t.TempDir()
	gw.writeFile = func(path, content string) error { written[path] = content; return nil }
	gw.removeFile = func(path string) error { delete(written, path); return nil }
	gw.syncTunnel = func(context.Context, string, string) error { return nil }
	gw.downTunnel = func(context.Context, string, string) error { return nil }
	t.Cleanup(func() { _ = written })
	return gw, repo, cipher
}

func wgCreateOp(id, node, ifName, carrier string) change.Op {
	return change.Op{
		Type:   change.OpWgTunnelCreate,
		Target: inventory.Ref{Kind: inventory.KindWgTunnel, Node: node, ID: id},
		Params: &change.WgTunnelCreateParams{IfName: ifName, ListenPort: 51820, Addresses: []string{"10.10.0.1/24"}, MTU: 1420, Carrier: carrier},
	}
}

// TestHostWGGateway_CreateCustody proves the production gateway's key custody:
// a create generates a real keypair on-node, stores ONLY the sealed private
// key (which decrypts to a key whose public half matches the stored public
// key), and the plaintext key appears in no store column.
func TestHostWGGateway_CreateCustody(t *testing.T) {
	gw, repo, cipher := wgTestGateway(t)
	ctx := context.Background()

	if err := gw.ApplyWgOp(ctx, wgCreateOp("tun1", "pve1", "wg0", "vmbr9")); err != nil {
		t.Fatalf("ApplyWgOp create: %v", err)
	}
	tun, err := repo.GetTunnel(ctx, "tun1")
	if err != nil {
		t.Fatalf("GetTunnel: %v", err)
	}
	plaintext, err := cipher.Decrypt(tun.PrivateKeyEnc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	raw, err := wireguard.DecodeKey(string(plaintext))
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	pub, _ := wireguard.PublicKeyFor(raw)
	if wireguard.EncodeKey(pub) != tun.PublicKey {
		t.Fatal("stored public key not derived from stored private key")
	}
	// The public_key column must not equal the private key; the private key
	// must not be stored anywhere in plaintext.
	if tun.PublicKey == string(plaintext) {
		t.Fatal("public key column holds the private key")
	}
}

// TestHostWGGateway_PeerNodeRejected proves a wg op for a non-local node is
// refused (cluster wg routing is a documented follow-up).
func TestHostWGGateway_PeerNodeRejected(t *testing.T) {
	gw, _, _ := wgTestGateway(t)
	err := gw.ApplyWgOp(context.Background(), wgCreateOp("tun1", "pve2", "wg0", ""))
	if err == nil || !strings.Contains(err.Error(), "peer node") {
		t.Fatalf("expected peer-node rejection, got %v", err)
	}
}

// TestHostWGGateway_SnapshotRestore proves the rollback reconcile: a tunnel
// created after a snapshot is torn down by RestoreWg back to that snapshot,
// leaving no orphaned key material (T-1401 AC6, production gateway).
func TestHostWGGateway_SnapshotRestore(t *testing.T) {
	gw, repo, _ := wgTestGateway(t)
	ctx := context.Background()

	pre, err := gw.SnapshotWg(ctx, "pve1") // empty
	if err != nil {
		t.Fatalf("SnapshotWg: %v", err)
	}
	if err := gw.ApplyWgOp(ctx, wgCreateOp("tun1", "pve1", "wg0", "vmbr9")); err != nil {
		t.Fatalf("ApplyWgOp: %v", err)
	}
	if _, err := repo.GetTunnel(ctx, "tun1"); err != nil {
		t.Fatalf("tunnel should exist: %v", err)
	}
	if err := gw.RestoreWg(ctx, "pve1", pre); err != nil {
		t.Fatalf("RestoreWg: %v", err)
	}
	if tuns, _ := repo.ListTunnels(ctx, "pve1"); len(tuns) != 0 {
		t.Fatalf("tunnel not removed on restore: %d remain (orphaned key material)", len(tuns))
	}
}

// TestWireGuardReadService_NoPrivateKey proves the read service never surfaces
// a private key and derives pubkey/peer-config from the store.
func TestWireGuardReadService_NoPrivateKey(t *testing.T) {
	gw, repo, _ := wgTestGateway(t)
	ctx := context.Background()
	if err := gw.ApplyWgOp(ctx, wgCreateOp("tun1", "pve1", "wg0", "vmbr9")); err != nil {
		t.Fatalf("ApplyWgOp: %v", err)
	}
	rs := newWireGuardReadService(repo, func() string { return "pve1" }, testLogger())
	rs.dump = func(context.Context, string) (string, error) { return "", context.Canceled } // no live wg

	views, err := rs.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(views) != 1 || views[0].PublicKey == "" {
		t.Fatalf("views = %+v", views)
	}
	pub, err := rs.PublicKey(ctx, "tun1")
	if err != nil || pub == "" {
		t.Fatalf("PublicKey = %q, %v", pub, err)
	}
	cfg, err := rs.PeerConfig(ctx, "tun1")
	if err != nil || !strings.Contains(cfg, "[Peer]") {
		t.Fatalf("PeerConfig = %q, %v", cfg, err)
	}
	if strings.Contains(cfg, "PrivateKey = "+pub) {
		t.Fatal("peer config leaked a real key")
	}
}

// TestHostWGGateway_PeerPresharedKeySealed is Finding 1's regression (c): a
// wg.peer.add op whose preshared key rides sealed in the op (the shape the
// change engine produces after Service.sealOpSecrets) still applies, and the
// gateway ends up sealing the real key into wireguard_peers.preshared_key_enc
// exactly as before — the stored column decrypts back to the original PSK.
func TestHostWGGateway_PeerPresharedKeySealed(t *testing.T) {
	gw, repo, cipher := wgTestGateway(t)
	ctx := context.Background()
	if err := gw.ApplyWgOp(ctx, wgCreateOp("tun1", "pve1", "wg0", "vmbr9")); err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	const psk = "cHNrLXNlY3JldC1iYXNlNjQtdmFsdWU="
	const peerPub = "cGVlcnB1YmtleS12YWx1ZQ=="

	// The op carries the PSK sealed in PresharedKeyEnc (not plaintext), as the
	// change engine hands it to apply after sealing at stage time.
	sealedInOp, err := cipher.Encrypt([]byte(psk))
	if err != nil {
		t.Fatalf("seal PSK for op: %v", err)
	}
	addOp := change.Op{
		Type:   change.OpWgPeerAdd,
		Target: inventory.Ref{Kind: inventory.KindWgPeer, Node: "pve1", ID: "tun1/" + peerPub},
		Params: &change.WgPeerAddParams{PublicKey: peerPub, PresharedKeyEnc: sealedInOp, AllowedIPs: []string{"10.0.0.2/32"}},
	}
	if err = gw.ApplyWgOp(ctx, addOp); err != nil {
		t.Fatalf("apply wg.peer.add: %v", err)
	}

	peers, err := repo.ListPeers(ctx, "tun1")
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("want 1 peer, got %d", len(peers))
	}
	if len(peers[0].PresharedKeyEnc) == 0 {
		t.Fatal("stored peer has no sealed preshared key")
	}
	plain, err := cipher.Decrypt(peers[0].PresharedKeyEnc)
	if err != nil || string(plain) != psk {
		t.Fatalf("stored preshared_key_enc did not decrypt to the PSK: plain=%q err=%v", plain, err)
	}

	// A hand-built op carrying the plaintext directly (never through the stage
	// path) is still honored and sealed into the column too.
	const peerPub2 = "cGVlcnB1YmtleS10d28="
	addPlain := change.Op{
		Type:   change.OpWgPeerAdd,
		Target: inventory.Ref{Kind: inventory.KindWgPeer, Node: "pve1", ID: "tun1/" + peerPub2},
		Params: &change.WgPeerAddParams{PublicKey: peerPub2, PresharedKey: psk},
	}
	if err = gw.ApplyWgOp(ctx, addPlain); err != nil {
		t.Fatalf("apply wg.peer.add (plaintext): %v", err)
	}
	peers2, err := repo.ListPeers(ctx, "tun1")
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	var found bool
	for _, p := range peers2 {
		if p.PublicKey != peerPub2 {
			continue
		}
		found = true
		plain2, decErr := cipher.Decrypt(p.PresharedKeyEnc)
		if decErr != nil || string(plain2) != psk {
			t.Fatalf("plaintext-op PSK not sealed correctly: plain=%q err=%v", plain2, decErr)
		}
	}
	if !found {
		t.Fatal("second peer not stored")
	}
}
