package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

func TestWireGuardRepo_TunnelLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repo := NewWireGuardRepo(db)

	sealed := []byte{0x01, 0x02, 0x03, 0x04}
	tun := WireGuardTunnel{
		ID: NewULID(), Node: "pve1", IfName: "wg0",
		PrivateKeyEnc: sealed, PublicKey: "PUBkey000000000000000000000000000000000000=",
		ListenPort: 51820, Addresses: []string{"10.10.0.1/24"}, MTU: 1420, Carrier: "vmbr0",
		CreatedBy: "root@pam", CreatedAt: 100,
	}
	if err := repo.InsertTunnel(ctx, tun); err != nil {
		t.Fatalf("InsertTunnel: %v", err)
	}

	got, err := repo.GetTunnel(ctx, tun.ID)
	if err != nil {
		t.Fatalf("GetTunnel: %v", err)
	}
	if got.PublicKey != tun.PublicKey || got.ListenPort != 51820 || got.Carrier != "vmbr0" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if string(got.PrivateKeyEnc) != string(sealed) {
		t.Errorf("private key ciphertext not round-tripped verbatim")
	}
	if len(got.Addresses) != 1 || got.Addresses[0] != "10.10.0.1/24" {
		t.Errorf("addresses = %v", got.Addresses)
	}

	list, err := repo.ListTunnels(ctx, "pve1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTunnels = %v, %v", list, err)
	}
	if none, _ := repo.ListTunnels(ctx, "pve2"); len(none) != 0 {
		t.Errorf("ListTunnels(pve2) should be empty, got %v", none)
	}

	// Peers, including an external one.
	peers := []WireGuardPeer{
		{TunnelID: tun.ID, PublicKey: "PEER1", Endpoint: "203.0.113.10:51820", AllowedIPs: []string{"10.10.0.2/32"}, KeepaliveSec: 25},
		{TunnelID: tun.ID, PublicKey: "PEERext", AllowedIPs: []string{"10.10.0.4/32"}, External: true},
	}
	for _, p := range peers {
		if addErr := repo.AddPeer(ctx, p); addErr != nil {
			t.Fatalf("AddPeer: %v", addErr)
		}
	}
	gotPeers, err := repo.ListPeers(ctx, tun.ID)
	if err != nil || len(gotPeers) != 2 {
		t.Fatalf("ListPeers = %v, %v", gotPeers, err)
	}
	if !gotPeers[0].External && !gotPeers[1].External {
		t.Error("expected one external peer")
	}

	// Remove one peer.
	if err := repo.RemovePeer(ctx, tun.ID, "PEER1"); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if gotPeers, _ = repo.ListPeers(ctx, tun.ID); len(gotPeers) != 1 {
		t.Fatalf("after RemovePeer, peers = %d, want 1", len(gotPeers))
	}

	// Delete tunnel cascades peers; delete is idempotent.
	if err := repo.DeleteTunnel(ctx, tun.ID); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	if _, err := repo.GetTunnel(ctx, tun.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTunnel after delete = %v, want ErrNotFound", err)
	}
	if gotPeers, _ = repo.ListPeers(ctx, tun.ID); len(gotPeers) != 0 {
		t.Errorf("peers not cascade-deleted: %v", gotPeers)
	}
	if err := repo.DeleteTunnel(ctx, tun.ID); err != nil {
		t.Errorf("second DeleteTunnel should be a no-op, got %v", err)
	}
}

// TestWireGuardRepo_PrivateKeyEncryptedAtRest is the T-1707 v3.0 security-pass
// targeted encrypted-at-rest test for the WireGuard tunnel private key (a
// Phase-14 credential class, docs/security.md "WireGuard tunnel keys"): a
// tunnel's X25519 private key, sealed with the production AES-256-GCM
// SessionCipher (the same primitive sessions.pve_ticket_enc /
// clusters.credential_enc / switches.credentials_enc use — not a second cipher
// or key pair), must never appear as plaintext in the stored bytes and must
// round-trip back only via the cipher. Mirrors TestSwitchRepo_CredentialsEncryptedAtRest.
func TestWireGuardRepo_PrivateKeyEncryptedAtRest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repo := NewWireGuardRepo(db)

	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	cipher, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}

	// A plausible base64-shaped WireGuard private key (the on-node keypair is
	// generated via crypto/ecdh X25519 and sealed once before it ever hits the store).
	const privKey = "cL8f7SECRETwgPRIVATEkey0000000000000000000ab="
	enc, err := cipher.Encrypt([]byte(privKey))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tun := WireGuardTunnel{
		ID: NewULID(), Node: "pve1", IfName: "wg0",
		PrivateKeyEnc: enc, PublicKey: "PUBkey000000000000000000000000000000000000=",
		ListenPort: 51820, Addresses: []string{"10.10.0.1/24"}, MTU: 1420, Carrier: "vmbr0",
		CreatedBy: "root@pam", CreatedAt: 100,
	}
	if err = repo.InsertTunnel(ctx, tun); err != nil {
		t.Fatalf("InsertTunnel: %v", err)
	}

	got, err := repo.GetTunnel(ctx, tun.ID)
	if err != nil {
		t.Fatalf("GetTunnel: %v", err)
	}
	// The stored ciphertext must not leak the plaintext key (or a fragment of it).
	if bytes.Contains(got.PrivateKeyEnc, []byte(privKey)) {
		t.Fatal("stored private_key_enc contains the plaintext private key!")
	}
	if bytes.Contains(got.PrivateKeyEnc, []byte("SECRETwgPRIVATEkey")) {
		t.Fatal("stored private_key_enc contains a plaintext fragment of the private key")
	}
	// Only the cipher recovers it.
	dec, err := cipher.Decrypt(got.PrivateKeyEnc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec) != privKey {
		t.Errorf("Decrypt = %q, want the original private key", dec)
	}
}
