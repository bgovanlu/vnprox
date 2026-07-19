package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"
)

func TestK8sClusterRepo_Lifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repo := NewK8sClusterRepo(db)

	c := K8sCluster{
		ID: NewULID(), Name: "prod-k8s", KubeconfigEnc: []byte{0x01, 0x02, 0x03},
		AddedBy: "root@pam", AddedAt: 100,
	}
	if err := repo.Insert(ctx, c); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "prod-k8s" || got.AddedBy != "root@pam" || got.Status != "unpolled" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if !bytes.Equal(got.KubeconfigEnc, c.KubeconfigEnc) {
		t.Errorf("kubeconfig ciphertext not round-tripped verbatim")
	}

	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}

	if statusErr := repo.UpdateStatus(ctx, c.ID, "calico", "ok"); statusErr != nil {
		t.Fatalf("UpdateStatus: %v", statusErr)
	}
	got, err = repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after UpdateStatus: %v", err)
	}
	if got.CNIDetected != "calico" || got.Status != "ok" {
		t.Errorf("UpdateStatus did not persist: %+v", got)
	}
	if !bytes.Equal(got.KubeconfigEnc, c.KubeconfigEnc) {
		t.Error("UpdateStatus must never touch kubeconfig_enc")
	}

	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, c.ID); err != ErrNotFound {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent row is not an error.
	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Errorf("Delete (already absent): %v", err)
	}
}

// TestK8sClusterRepo_KubeconfigEncryptedAtRest is T-1501 AC5's targeted
// encrypted-at-rest test: sealing a kubeconfig containing a recognizable
// plaintext bearer token and client-cert PEM material with the same
// AES-256-GCM SessionCipher every other secret column in this package
// uses, the raw stored ciphertext bytes never contain either plaintext
// substring — verified both via the repository's own round-trip and by
// reading the raw column bytes directly, mirroring
// TestSessionsRoundTrip_EncryptedAtRest's own belt-and-braces shape.
func TestK8sClusterRepo_KubeconfigEncryptedAtRest(t *testing.T) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generating key: %v", err)
	}
	cipher, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}

	const plaintextToken = "eyJhbGciOiJSUzI1NiJ9.super-secret-service-account-token.sig"
	const plaintextCertPEM = "-----BEGIN CERTIFICATE-----\nMIIC-fake-but-recognizable-cert-material\n-----END CERTIFICATE-----"
	plaintextKubeconfig := "apiVersion: v1\nkind: Config\nusers:\n  - user:\n      token: " + plaintextToken +
		"\n      client-certificate-data: " + plaintextCertPEM + "\n"

	sealed, err := cipher.Encrypt([]byte(plaintextKubeconfig))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(sealed, []byte(plaintextToken)) || bytes.Contains(sealed, []byte(plaintextCertPEM)) {
		t.Fatal("freshly sealed ciphertext already contains plaintext credential material — cipher is broken")
	}

	db := openTestDB(t)
	ctx := context.Background()
	repo := NewK8sClusterRepo(db)

	c := K8sCluster{ID: NewULID(), Name: "prod-k8s", KubeconfigEnc: sealed, AddedBy: "root@pam", AddedAt: 100}
	if insertErr := repo.Insert(ctx, c); insertErr != nil {
		t.Fatalf("Insert: %v", insertErr)
	}

	var raw []byte
	err = db.sqlDB.QueryRowContext(ctx, `SELECT kubeconfig_enc FROM k8s_clusters WHERE id = ?`, c.ID).Scan(&raw)
	if err != nil {
		t.Fatalf("reading raw kubeconfig_enc column: %v", err)
	}
	if bytes.Contains(raw, []byte(plaintextToken)) {
		t.Error("raw kubeconfig_enc column contains the plaintext bearer token")
	}
	if bytes.Contains(raw, []byte(plaintextCertPEM)) {
		t.Error("raw kubeconfig_enc column contains the plaintext client-certificate PEM")
	}

	decrypted, err := cipher.Decrypt(raw)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != plaintextKubeconfig {
		t.Error("decrypting the stored ciphertext did not recover the original kubeconfig")
	}
}
