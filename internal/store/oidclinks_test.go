// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestOIDCPVELinkRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewOIDCPVELinkRepo(db)
	ctx := context.Background()

	l := OIDCPVELink{
		ID: NewULID(), ClusterID: "", OIDCGroup: "net-admins", PVEUsername: "automation@pve",
		CredentialEnc: []byte{0x01, 0x02, 0x03}, CreatedBy: "root@pam", CreatedAt: 1700000000,
	}
	if err := repo.Upsert(ctx, l); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.GetByGroup(ctx, "", "net-admins")
	if err != nil {
		t.Fatalf("GetByGroup: %v", err)
	}
	if got.PVEUsername != "automation@pve" || got.CreatedBy != "root@pam" {
		t.Errorf("GetByGroup() = %+v", got)
	}
	if !bytes.Equal(got.CredentialEnc, l.CredentialEnc) {
		t.Errorf("CredentialEnc = %v, want %v", got.CredentialEnc, l.CredentialEnc)
	}

	// Absent group is ErrNotFound.
	if _, err = repo.GetByGroup(ctx, "", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByGroup(absent) err = %v, want ErrNotFound", err)
	}

	// Re-linking the same (cluster, group) replaces the credential, not errors.
	l.PVEUsername = "readers@pve"
	l.CredentialEnc = []byte{0x09}
	if err = repo.Upsert(ctx, l); err != nil {
		t.Fatalf("Upsert replace: %v", err)
	}
	got, err = repo.GetByGroup(ctx, "", "net-admins")
	if err != nil {
		t.Fatalf("GetByGroup after replace: %v", err)
	}
	if got.PVEUsername != "readers@pve" || !bytes.Equal(got.CredentialEnc, []byte{0x09}) {
		t.Errorf("replace not applied: %+v", got)
	}

	// A second group on the same cluster + a group on another cluster, then
	// ListByCluster is scoped and ordered.
	if err = repo.Upsert(ctx, OIDCPVELink{ID: NewULID(), ClusterID: "", OIDCGroup: "auditors", PVEUsername: "a@pve", CredentialEnc: []byte{0x11}, CreatedBy: "root@pam", CreatedAt: 1700000100}); err != nil {
		t.Fatalf("Upsert #2: %v", err)
	}
	if err = repo.Upsert(ctx, OIDCPVELink{ID: NewULID(), ClusterID: "east", OIDCGroup: "net-admins", PVEUsername: "e@pve", CredentialEnc: []byte{0x22}, CreatedBy: "root@pam", CreatedAt: 1700000200}); err != nil {
		t.Fatalf("Upsert #3: %v", err)
	}
	local, err := repo.ListByCluster(ctx, "")
	if err != nil {
		t.Fatalf("ListByCluster: %v", err)
	}
	if len(local) != 2 || local[0].OIDCGroup != "auditors" || local[1].OIDCGroup != "net-admins" {
		t.Errorf("ListByCluster('') = %+v, want [auditors, net-admins]", local)
	}

	// Delete is scoped and idempotent.
	if err = repo.Delete(ctx, "", "auditors"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err = repo.Delete(ctx, "", "auditors"); err != nil {
		t.Fatalf("Delete idempotent: %v", err)
	}
	if _, err = repo.GetByGroup(ctx, "", "auditors"); !errors.Is(err, ErrNotFound) {
		t.Errorf("group not deleted")
	}
	// The other cluster's identically-named group is untouched.
	if _, err = repo.GetByGroup(ctx, "east", "net-admins"); err != nil {
		t.Errorf("cross-cluster group wrongly affected: %v", err)
	}
}
