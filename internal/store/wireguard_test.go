package store

import (
	"context"
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
