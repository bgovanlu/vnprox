package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func testCipher(t *testing.T) *SessionCipher {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	c, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	return c
}

func TestSessionRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewSessionRepo(db, testCipher(t))
	ctx := context.Background()

	s := Session{
		ID:        "sess-1",
		Username:  "root",
		Realm:     "pam",
		PVETicket: "PVE:root@pam:SUPERSECRETTICKET==",
		CSRFToken: "csrf-secret-value",
		CapsJSON:  `{"node/pve1":["read","write"]}`,
		CreatedAt: 1000,
		ExpiresAt: 1000 + 7200,
	}

	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != s {
		t.Errorf("Get() = %+v, want %+v", got, s)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0] != s {
		t.Errorf("List() = %+v, want [%+v]", list, s)
	}

	s.CSRFToken = "csrf-rotated"
	s.PVETicket = "PVE:root@pam:ROTATEDTICKET=="
	s.ExpiresAt = 2000 + 7200
	if updateErr := repo.Update(ctx, s); updateErr != nil {
		t.Fatalf("Update: %v", updateErr)
	}

	got, err = repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got != s {
		t.Errorf("Get() after Update = %+v, want %+v", got, s)
	}

	if err := repo.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestSessionRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewSessionRepo(db, testCipher(t))

	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestSessionRepo_UpdateNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewSessionRepo(db, testCipher(t))

	err := repo.Update(context.Background(), Session{ID: "nope"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing): got %v, want ErrNotFound", err)
	}
}

// TestSessionRepo_SecretsEncryptedAtRest is acceptance criterion 3: the raw
// database bytes on disk must not contain the plaintext PVE ticket, and
// decrypting the stored ciphertext must recover it.
func TestSessionRepo_SecretsEncryptedAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.db")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	cipher := testCipher(t)
	repo := NewSessionRepo(db, cipher)

	const plaintextTicket = "PVE:root@pam:16000000::VERY-DISTINCTIVE-PLAINTEXT-TICKET-MARKER"
	const plaintextCSRF = "ANOTHER-DISTINCTIVE-CSRF-MARKER"

	s := Session{
		ID:        "sess-enc",
		Username:  "root",
		Realm:     "pam",
		PVETicket: plaintextTicket,
		CSRFToken: plaintextCSRF,
		CapsJSON:  `{}`,
		CreatedAt: 1,
		ExpiresAt: 2,
	}
	if insertErr := repo.Insert(ctx, s); insertErr != nil {
		t.Fatalf("Insert: %v", insertErr)
	}

	// Checkpoint WAL into the main db file and close so every byte we wrote
	// is actually on disk under path, not sitting in a -wal file.
	if _, checkpointErr := db.sqlDB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); checkpointErr != nil {
		t.Fatalf("wal_checkpoint: %v", checkpointErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading raw db file: %v", err)
	}
	if bytes.Contains(raw, []byte(plaintextTicket)) {
		t.Error("raw database file contains the plaintext PVE ticket")
	}
	if bytes.Contains(raw, []byte(plaintextCSRF)) {
		t.Error("raw database file contains the plaintext CSRF token")
	}

	// Also assert directly against the raw column bytes (belt and braces,
	// independent of file layout/WAL details), and that decrypting recovers
	// the original plaintext.
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = db2.Close() }()

	var ticketEnc, csrfEnc []byte
	err = db2.sqlDB.QueryRowContext(ctx,
		`SELECT pve_ticket_enc, csrf_token_enc FROM sessions WHERE id = ?`, s.ID,
	).Scan(&ticketEnc, &csrfEnc)
	if err != nil {
		t.Fatalf("reading raw encrypted columns: %v", err)
	}
	if bytes.Contains(ticketEnc, []byte(plaintextTicket)) {
		t.Error("raw pve_ticket_enc column contains the plaintext ticket")
	}
	if bytes.Contains(csrfEnc, []byte(plaintextCSRF)) {
		t.Error("raw csrf_token_enc column contains the plaintext csrf token")
	}

	decryptedTicket, err := cipher.Decrypt(ticketEnc)
	if err != nil {
		t.Fatalf("Decrypt(pve_ticket_enc): %v", err)
	}
	if string(decryptedTicket) != plaintextTicket {
		t.Errorf("decrypted ticket = %q, want %q", decryptedTicket, plaintextTicket)
	}

	decryptedCSRF, err := cipher.Decrypt(csrfEnc)
	if err != nil {
		t.Fatalf("Decrypt(csrf_token_enc): %v", err)
	}
	if string(decryptedCSRF) != plaintextCSRF {
		t.Errorf("decrypted csrf = %q, want %q", decryptedCSRF, plaintextCSRF)
	}
}

func TestSessionCipher_WrongKeyFailsToDecrypt(t *testing.T) {
	c1 := testCipher(t)
	c2 := testCipher(t)

	ciphertext, err := c1.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c2.Decrypt(ciphertext); err == nil {
		t.Error("Decrypt with wrong key: got nil error, want a failure")
	}
}

func TestNewSessionCipher_RejectsWrongKeySize(t *testing.T) {
	_, err := NewSessionCipher([]byte("too-short"))
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("NewSessionCipher(short key): got %v, want ErrInvalidKey", err)
	}
}

func TestKeyFile_GenerateAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "session.key")

	if err := GenerateKeyFile(path); err != nil {
		t.Fatalf("GenerateKeyFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 0600", perm)
	}

	key, err := LoadKeyFile(path)
	if err != nil {
		t.Fatalf("LoadKeyFile: %v", err)
	}
	if len(key) != KeySize {
		t.Errorf("loaded key length = %d, want %d", len(key), KeySize)
	}

	// A second GenerateKeyFile at the same path must refuse to clobber it.
	if err := GenerateKeyFile(path); err == nil {
		t.Error("GenerateKeyFile over an existing key: got nil error, want a refusal")
	}
}

func TestLoadKeyFile_RejectsWrongSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.key")
	if err := os.WriteFile(path, []byte("not-32-bytes"), 0o600); err != nil {
		t.Fatalf("writing bad key file: %v", err)
	}
	if _, err := LoadKeyFile(path); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("LoadKeyFile(wrong size): got %v, want ErrInvalidKey", err)
	}
}
