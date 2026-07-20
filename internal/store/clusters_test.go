package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestClusterRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewClusterRepo(db)
	ctx := context.Background()

	c := Cluster{
		ID: NewULID(), Name: "east", APIURL: "https://east.example:8006",
		CredentialEnc: []byte{0x01, 0x02, 0x03}, AddedBy: "root@pam", AddedAt: 1700000000,
	}
	if err := repo.Insert(ctx, c); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "east" || got.APIURL != c.APIURL || got.AddedBy != "root@pam" {
		t.Errorf("Get() = %+v, want name=east url/addedBy to match", got)
	}
	if got.Status != "unknown" {
		t.Errorf("Get().Status = %q, want default %q", got.Status, "unknown")
	}
	if !bytes.Equal(got.CredentialEnc, c.CredentialEnc) {
		t.Errorf("Get().CredentialEnc = %v, want %v", got.CredentialEnc, c.CredentialEnc)
	}

	// A second cluster, then List is stable by added_at, id.
	c2 := Cluster{ID: NewULID(), Name: "west", APIURL: "https://west.example:8006", CredentialEnc: []byte{0x09}, AddedBy: "root@pam", AddedAt: 1700000100}
	if err = repo.Insert(ctx, c2); err != nil {
		t.Fatalf("Insert #2: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != c.ID || list[1].ID != c2.ID {
		t.Fatalf("List() ordering = %+v, want [%s, %s]", list, c.ID, c2.ID)
	}

	// Update rewrites name/url/credential/status.
	c.Name = "east-renamed"
	c.CredentialEnc = []byte{0xAA, 0xBB}
	c.Status = "ok"
	if err = repo.Update(ctx, c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Name != "east-renamed" || got.Status != "ok" || !bytes.Equal(got.CredentialEnc, []byte{0xAA, 0xBB}) {
		t.Errorf("Get() after Update = %+v, want renamed/ok/new-credential", got)
	}

	// UpdateStatus touches status only, never the credential.
	if err = repo.UpdateStatus(ctx, c.ID, "unreachable"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, err = repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after UpdateStatus: %v", err)
	}
	if got.Status != "unreachable" || !bytes.Equal(got.CredentialEnc, []byte{0xAA, 0xBB}) {
		t.Errorf("after UpdateStatus = %+v, want status unreachable, credential unchanged", got)
	}

	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
	// Deleting an already-absent cluster is not an error.
	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Errorf("Delete(absent): %v", err)
	}
}

func TestClusterRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewClusterRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestClusterRepo_UpdateNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewClusterRepo(db)
	c := Cluster{ID: "nope", Name: "x", APIURL: "https://x:8006", CredentialEnc: []byte{0x01}, AddedBy: "root@pam", AddedAt: 1}
	if err := repo.Update(context.Background(), c); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing): got %v, want ErrNotFound", err)
	}
}
