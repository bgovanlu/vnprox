// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

// TestSwitchRepo_RoundTrip covers CRUD + the enabled flag default and list
// ordering, mirroring TestClusterRepo_RoundTrip.
func TestSwitchRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewSwitchRepo(db)
	ctx := context.Background()

	s := Switch{
		ID: NewULID(), Name: "tor-a", MgmtAddr: "10.0.0.2", DriverType: "openconfig-gnmi",
		CredentialsEnc: []byte{0x01, 0x02}, Enabled: false, AddedBy: "root@pam", AddedAt: 1700000000,
	}
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "tor-a" || got.MgmtAddr != "10.0.0.2" || got.DriverType != "openconfig-gnmi" {
		t.Errorf("Get() = %+v, want name/mgmtAddr/driverType to match", got)
	}
	if got.Enabled {
		t.Errorf("Get().Enabled = true, want false (T-1205 ships dark by default)")
	}

	// A second switch; List is ordered by name.
	s2 := Switch{ID: NewULID(), Name: "core-1", MgmtAddr: "10.0.0.1", DriverType: "openconfig-gnmi", Enabled: true, AddedBy: "root@pam", AddedAt: 1700000100}
	if err = repo.Insert(ctx, s2); err != nil {
		t.Fatalf("Insert #2: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Name != "core-1" || list[1].Name != "tor-a" {
		t.Fatalf("List() ordering = %+v, want [core-1, tor-a]", list)
	}

	// Update flips enabled and rewrites credentials.
	s.Enabled = true
	s.CredentialsEnc = []byte{0xAA}
	if err = repo.Update(ctx, s); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if !got.Enabled || !bytes.Equal(got.CredentialsEnc, []byte{0xAA}) {
		t.Errorf("Get() after Update = %+v, want enabled + new credential", got)
	}

	if err = repo.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err = repo.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestSwitchRepo_CredentialsEncryptedAtRest is T-1208 AC4's targeted
// encrypted-at-rest test for the switch credential type: a switch's driver
// credentials, sealed with the production AES-256-GCM SessionCipher (the same
// primitive clusters.credential_enc / sessions.pve_ticket_enc use — not a
// second cipher), must never appear as plaintext in the stored bytes, and must
// round-trip back to the original only via the cipher.
func TestSwitchRepo_CredentialsEncryptedAtRest(t *testing.T) {
	db := openTestDB(t)
	repo := NewSwitchRepo(db)
	ctx := context.Background()

	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	cipher, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}

	const secret = "gnmi-user:SUPER-SECRET-SWITCH-PASSWORD-7c1e"
	enc, err := cipher.Encrypt([]byte(secret))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	s := Switch{
		ID: NewULID(), Name: "tor-a", MgmtAddr: "10.0.0.2", DriverType: "openconfig-gnmi",
		CredentialsEnc: enc, Enabled: false, AddedBy: "root@pam", AddedAt: 1700000000,
	}
	if err = repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The stored ciphertext must not leak the plaintext credential.
	if bytes.Contains(got.CredentialsEnc, []byte(secret)) {
		t.Fatal("stored credentials_enc contains the plaintext credential!")
	}
	if bytes.Contains(got.CredentialsEnc, []byte("SUPER-SECRET-SWITCH-PASSWORD")) {
		t.Fatal("stored credentials_enc contains a plaintext fragment of the credential")
	}
	// Only the cipher recovers it.
	dec, err := cipher.Decrypt(got.CredentialsEnc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec) != secret {
		t.Errorf("Decrypt = %q, want the original credential", dec)
	}
}
