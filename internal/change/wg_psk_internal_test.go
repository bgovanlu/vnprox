package change

// Finding 1 (T-1401 adversarial review): a wg.peer.add op's preshared key is a
// WireGuard secret and must never persist in plaintext at rest in
// changesets.ops_json. These white-box tests prove Service.sealOpSecrets seals
// it at stage/create time (the same seam Create/UpdateDraft call) and that the
// serialized store row carries only the sealed form.

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

func newTestSealer(t *testing.T) *store.SessionCipher {
	t.Helper()
	key := make([]byte, store.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("store.NewSessionCipher: %v", err)
	}
	return cipher
}

func wgPeerAddOp(psk string) Op {
	return Op{
		Type:   OpWgPeerAdd,
		Target: inventory.Ref{Kind: inventory.KindWgPeer, Node: "pve1", ID: "tun1/cGVlcg=="},
		Params: &WgPeerAddParams{PublicKey: "cGVlcg==", PresharedKey: psk, AllowedIPs: []string{"10.0.0.2/32"}},
	}
}

// TestSealOpSecrets_NoPlaintextPSKAtRest is Finding 1's regression (b): after
// sealing, the persisted ops_json contains no plaintext PSK, only the sealed
// form, and the sealed bytes round-trip back to the original key.
func TestSealOpSecrets_NoPlaintextPSKAtRest(t *testing.T) {
	cipher := newTestSealer(t)
	s := &Service{sealer: cipher}
	const psk = "UFNLLXBsYWludGV4dC1zZWNyZXQtdmFsdWU=" // distinctive marker

	ops := []Op{wgPeerAddOp(psk)}
	if err := s.sealOpSecrets(ops); err != nil {
		t.Fatalf("sealOpSecrets: %v", err)
	}

	p := ops[0].Params.(*WgPeerAddParams)
	if p.PresharedKey != "" {
		t.Fatalf("plaintext preshared key not cleared after sealing: %q", p.PresharedKey)
	}
	if len(p.PresharedKeyEnc) == 0 {
		t.Fatal("preshared key was not sealed into PresharedKeyEnc")
	}

	row, err := toStoreRow(Changeset{ID: "cs1", Ops: ops})
	if err != nil {
		t.Fatalf("toStoreRow: %v", err)
	}
	if strings.Contains(row.OpsJSON, psk) {
		t.Fatalf("plaintext preshared key leaked into ops_json: %s", row.OpsJSON)
	}
	if !strings.Contains(row.OpsJSON, "presharedKeyEnc") {
		t.Fatalf("sealed preshared key missing from ops_json: %s", row.OpsJSON)
	}

	// The sealed bytes decrypt back to the original PSK, so apply can recover it.
	plain, err := cipher.Decrypt(p.PresharedKeyEnc)
	if err != nil || string(plain) != psk {
		t.Fatalf("sealed PSK did not round-trip: plain=%q err=%v", plain, err)
	}
}

// TestSealOpSecrets_FailClosedWithoutSealer proves a plaintext PSK with no
// configured cipher is rejected rather than silently persisted in the clear;
// a PSK-less peer op needs no sealer.
func TestSealOpSecrets_FailClosedWithoutSealer(t *testing.T) {
	s := &Service{} // no sealer configured

	if err := s.sealOpSecrets([]Op{wgPeerAddOp("plaintext-secret")}); err == nil {
		t.Fatal("expected sealOpSecrets to fail closed with a plaintext PSK and no sealer")
	}

	clean := []Op{{
		Type:   OpWgPeerAdd,
		Target: inventory.Ref{Kind: inventory.KindWgPeer, Node: "pve1", ID: "tun1/cGVlcg=="},
		Params: &WgPeerAddParams{PublicKey: "cGVlcg=="},
	}}
	if err := s.sealOpSecrets(clean); err != nil {
		t.Fatalf("sealOpSecrets on a PSK-less op should not error: %v", err)
	}
}

// TestSealOpSecrets_Idempotent proves a re-submitted op that already carries
// only the sealed form (a GET->PUT round-trip) is left untouched.
func TestSealOpSecrets_Idempotent(t *testing.T) {
	cipher := newTestSealer(t)
	s := &Service{sealer: cipher}
	sealed, err := cipher.Encrypt([]byte("already-sealed"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ops := []Op{{
		Type:   OpWgPeerAdd,
		Target: inventory.Ref{Kind: inventory.KindWgPeer, Node: "pve1", ID: "tun1/cGVlcg=="},
		Params: &WgPeerAddParams{PublicKey: "cGVlcg==", PresharedKeyEnc: sealed},
	}}
	if err := s.sealOpSecrets(ops); err != nil {
		t.Fatalf("sealOpSecrets: %v", err)
	}
	p := ops[0].Params.(*WgPeerAddParams)
	if string(p.PresharedKeyEnc) != string(sealed) {
		t.Fatal("already-sealed PSK should be left untouched")
	}
}
