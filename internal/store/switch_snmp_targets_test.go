// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

// TestSwitchSNMPTargetRepo_RoundTrip mirrors TestSwitchRepo_RoundTrip's shape
// for T-4013's new table: CRUD, the enabled-defaults-false-by-omission
// convention, list ordering, and the ChassisID lookup internal/ifcounters'
// TargetStore implementation uses.
func TestSwitchSNMPTargetRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewSwitchSNMPTargetRepo(db)
	ctx := context.Background()

	target := SwitchSNMPTarget{
		ID: NewULID(), ChassisID: "aa:bb:cc:dd:ee:ff", ChassisIDType: "mac-address",
		MgmtAddr: "10.0.0.2", Port: 161, CommunityEnc: []byte{0x01, 0x02},
		Enabled: false, AddedBy: "root@pam", AddedAt: 1700000000,
	}
	if err := repo.Insert(ctx, target); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ChassisID != target.ChassisID || got.MgmtAddr != "10.0.0.2" || got.Port != 161 {
		t.Errorf("Get() = %+v, want chassisId/mgmtAddr/port to match", got)
	}
	if got.Enabled {
		t.Errorf("Get().Enabled = true, want false (T-4013 ships dark by default)")
	}

	byChassis, err := repo.GetByChassisID(ctx, target.ChassisID)
	if err != nil {
		t.Fatalf("GetByChassisID: %v", err)
	}
	if byChassis.ID != target.ID {
		t.Errorf("GetByChassisID() = %+v, want id %s", byChassis, target.ID)
	}

	// A second target; List is ordered by chassis_id.
	target2 := SwitchSNMPTarget{
		ID: NewULID(), ChassisID: "00:11:22:33:44:55", ChassisIDType: "mac-address",
		MgmtAddr: "10.0.0.1", Port: 161, Enabled: true, AddedBy: "root@pam", AddedAt: 1700000100,
	}
	if err = repo.Insert(ctx, target2); err != nil {
		t.Fatalf("Insert #2: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ChassisID != "00:11:22:33:44:55" || list[1].ChassisID != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("List() ordering = %+v, want [00:11:22:33:44:55, aa:bb:cc:dd:ee:ff]", list)
	}

	enabled, err := repo.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != target2.ID {
		t.Fatalf("ListEnabled() = %+v, want only target2 (the only enabled row)", enabled)
	}

	// Update flips enabled and rewrites the community ciphertext.
	target.Enabled = true
	target.CommunityEnc = []byte{0xAA}
	if err = repo.Update(ctx, target); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if !got.Enabled || !bytes.Equal(got.CommunityEnc, []byte{0xAA}) {
		t.Errorf("Get() after Update = %+v, want enabled + new community ciphertext", got)
	}

	if err = repo.Delete(ctx, target.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err = repo.Get(ctx, target.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}

	if err = repo.DeleteByChassisID(ctx, target2.ChassisID); err != nil {
		t.Fatalf("DeleteByChassisID: %v", err)
	}
	if _, err = repo.GetByChassisID(ctx, target2.ChassisID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByChassisID after DeleteByChassisID: got %v, want ErrNotFound", err)
	}
}

// TestSwitchSNMPTargetRepo_CommunityEncryptedAtRest mirrors
// TestSwitchRepo_CredentialsEncryptedAtRest: the SNMP v2c community string,
// sealed with the production AES-256-GCM SessionCipher (the identical
// primitive switches.credentials_enc / sessions.pve_ticket_enc use — not a
// second cipher), must never appear as plaintext in the stored bytes.
func TestSwitchSNMPTargetRepo_CommunityEncryptedAtRest(t *testing.T) {
	db := openTestDB(t)
	repo := NewSwitchSNMPTargetRepo(db)
	ctx := context.Background()

	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	cipher, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}

	const secret = "SUPER-SECRET-SNMP-COMMUNITY-7c1e"
	enc, err := cipher.Encrypt([]byte(secret))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	target := SwitchSNMPTarget{
		ID: NewULID(), ChassisID: "aa:bb:cc:dd:ee:ff", ChassisIDType: "mac-address",
		MgmtAddr: "10.0.0.2", Port: 161, CommunityEnc: enc, Enabled: true,
		AddedBy: "root@pam", AddedAt: 1700000000,
	}
	if err = repo.Insert(ctx, target); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, target.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if bytes.Contains(got.CommunityEnc, []byte(secret)) {
		t.Fatal("stored community_enc contains the plaintext community string!")
	}
	if bytes.Contains(got.CommunityEnc, []byte("SUPER-SECRET-SNMP-COMMUNITY")) {
		t.Fatal("stored community_enc contains a plaintext fragment of the community string")
	}
	dec, err := cipher.Decrypt(got.CommunityEnc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec) != secret {
		t.Errorf("Decrypt = %q, want the original community string", dec)
	}
}
