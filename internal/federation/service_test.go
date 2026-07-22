package federation

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// newTestService builds a Service over a fresh in-memory-ish store DB with a
// real AES-256-GCM SessionCipher, so credential-sealing assertions exercise
// the production cipher, not a fake.
func newTestService(t *testing.T) (*Service, *store.ClusterRepo, *store.DB) {
	t.Helper()
	db := openStoreDB(t)
	key := make([]byte, store.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	repo := store.NewClusterRepo(db)
	svc, err := NewService(Config{Clusters: repo, Cipher: cipher, Now: func() time.Time { return time.Unix(1_700_000_000, 0) }})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo, db
}

func openStoreDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir()+"/vnprox.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestService_CRUD(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	c, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialTicket, Username: "root@pam", Password: "pw"}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.ID == "" || c.Name != "east" || c.Status != "unknown" || c.AddedBy != "admin@pam" {
		t.Fatalf("Add() = %+v, want id/name/status/addedBy populated", c)
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "east" || got.APIURL != "https://east:8006" {
		t.Errorf("Get() = %+v", got)
	}

	// Update rename only (nil credential) keeps working.
	if _, err = svc.Update(ctx, c.ID, "east-renamed", "", nil, nil); err != nil {
		t.Fatalf("Update rename: %v", err)
	}
	got, _ = svc.Get(ctx, c.ID)
	if got.Name != "east-renamed" {
		t.Errorf("after rename Name = %q, want east-renamed", got.Name)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}

	if err := svc.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
}

func TestService_Add_ValidatesCredential(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	// A ticket credential missing the password must be rejected before any
	// row is written.
	if _, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialTicket, Username: "root@pam"}, "admin@pam"); err == nil {
		t.Fatal("Add with incomplete ticket credential: want error, got nil")
	}
	if _, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: "bogus"}, "admin@pam"); err == nil {
		t.Fatal("Add with unknown credential kind: want error, got nil")
	}
}

// TestService_CredentialCiphertextNeverContainsPlaintext is T-1201 AC1: the
// stored credential ciphertext must never contain the plaintext token —
// asserted on the raw stored bytes.
func TestService_CredentialCiphertextNeverContainsPlaintext(t *testing.T) {
	svc, repo, _ := newTestService(t)
	ctx := context.Background()

	const secretToken = "root@pve!federation=SUPER-SECRET-TOKEN-VALUE-9f3a"
	c, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialToken, Token: secretToken}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	row, err := repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	if len(row.CredentialEnc) == 0 {
		t.Fatal("stored credential_enc is empty")
	}
	if bytes.Contains(row.CredentialEnc, []byte(secretToken)) {
		t.Fatalf("stored credential_enc contains the plaintext token! (%d bytes)", len(row.CredentialEnc))
	}
	if bytes.Contains(row.CredentialEnc, []byte("SUPER-SECRET-TOKEN-VALUE")) {
		t.Fatal("stored credential_enc contains a plaintext fragment of the token")
	}

	// And the credential is recoverable through the Service's own unseal
	// path (proving it was really encrypted, not just dropped).
	cred, err := svc.openCredential(row.CredentialEnc)
	if err != nil {
		t.Fatalf("openCredential: %v", err)
	}
	if cred.Token != secretToken {
		t.Errorf("unsealed token = %q, want the original", cred.Token)
	}
}
