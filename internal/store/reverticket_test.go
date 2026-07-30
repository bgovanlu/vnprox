package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// T-1805 store-layer coverage for the sealed apply-time revert ticket
// (changesets.revert_ticket_enc / revert_ticket_expires_at, migration 33).
//
// TestChangesetRepo_RevertTicketEncryptedAtRest is the direct counterpart of
// TestWireGuardRepo_PrivateKeyEncryptedAtRest for this new credential class:
// the PVE ticket is sealed with the SAME production AES-256-GCM SessionCipher
// every other at-rest credential in this product uses (docs/security.md — not
// a second cipher or key pair), must never appear as plaintext in the stored
// column, must not appear anywhere in the raw SQLite file's bytes, and must
// round-trip back only via the cipher.
func TestChangesetRepo_RevertTicketEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewChangesetRepo(db)

	key := make([]byte, KeySize)
	if _, err = rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	cipher, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}

	// A plausible PVE ticket shape (PVE:user@realm:HEX::base64-signature).
	const ticket = "PVE:root@pam:6A1B2C3D::SECRETticketSIGNATUREbytes0000000000=="
	sealed, err := cipher.Encrypt([]byte(ticket))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	cs := Changeset{ID: NewULID(), Title: "fw change", Author: "root@pam", Status: "awaiting_confirm", OpsJSON: "[]", CreatedAt: 100, UpdatedAt: 100}
	if err = repo.Insert(ctx, cs); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err = repo.SealRevertTicket(ctx, cs.ID, sealed, 1_700_007_200); err != nil {
		t.Fatalf("SealRevertTicket: %v", err)
	}

	got, expiresAt, err := repo.RevertTicket(ctx, cs.ID)
	if err != nil {
		t.Fatalf("RevertTicket: %v", err)
	}
	if expiresAt != 1_700_007_200 {
		t.Errorf("expiresAt = %d, want 1700007200", expiresAt)
	}
	if bytes.Contains(got, []byte(ticket)) {
		t.Fatal("stored revert_ticket_enc contains the plaintext PVE ticket!")
	}
	if bytes.Contains(got, []byte("SECRETticketSIGNATURE")) {
		t.Fatal("stored revert_ticket_enc contains a plaintext fragment of the ticket")
	}

	// Same assertion against the raw database file, so a stray index, WAL
	// frame, or debug column cannot hide a copy.
	if err = db.sqlDB.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(new(int), new(int), new(int)); err != nil {
		t.Logf("wal_checkpoint: %v (continuing; the file scan below still covers the main DB)", err)
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("reading the SQLite file: %v", err)
	}
	if bytes.Contains(raw, []byte(ticket)) || bytes.Contains(raw, []byte("SECRETticketSIGNATURE")) {
		t.Fatal("the raw SQLite file contains the plaintext PVE ticket")
	}

	// Only the cipher recovers it.
	dec, err := cipher.Decrypt(got)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec) != ticket {
		t.Errorf("Decrypt = %q, want the original ticket", dec)
	}

	// The expiry — a bound, not a secret — is readable alongside the ordinary
	// changeset columns, which is what the apply-time coverage report needs.
	row, err := repo.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.RevertTicketExpiresAt.Valid || row.RevertTicketExpiresAt.Int64 != 1_700_007_200 {
		t.Errorf("Changeset.RevertTicketExpiresAt = %v, want 1700007200", row.RevertTicketExpiresAt)
	}
}

// TestChangesetRepo_OrdinaryUpdateNeverTouchesTheRevertTicket is the invariant
// that lets internal/change persist a changeset freely without thinking about
// the credential: Update (and Insert/Upsert) has no revert-ticket column in its
// statement, so an ordinary persist can neither clobber a live ticket nor
// resurrect a wiped one.
func TestChangesetRepo_OrdinaryUpdateNeverTouchesTheRevertTicket(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repo := NewChangesetRepo(db)

	cs := Changeset{ID: NewULID(), Title: "t", Author: "root@pam", Status: "applying", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}
	if err := repo.Insert(ctx, cs); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.SealRevertTicket(ctx, cs.ID, []byte("sealed-bytes"), 999); err != nil {
		t.Fatalf("SealRevertTicket: %v", err)
	}

	// A persist that carries a zero RevertTicketExpiresAt must not erase it.
	cs.Status = "awaiting_confirm"
	cs.UpdatedAt = 2
	if err := repo.Update(ctx, cs); err != nil {
		t.Fatalf("Update: %v", err)
	}
	sealed, exp, err := repo.RevertTicket(ctx, cs.ID)
	if err != nil {
		t.Fatalf("RevertTicket: %v", err)
	}
	if string(sealed) != "sealed-bytes" || exp != 999 {
		t.Fatalf("an ordinary Update clobbered the sealed ticket: %q / %d", sealed, exp)
	}

	// ...and a persist after a wipe must not bring it back.
	if err = repo.WipeRevertTicket(ctx, cs.ID); err != nil {
		t.Fatalf("WipeRevertTicket: %v", err)
	}
	cs.UpdatedAt = 3
	if err = repo.Update(ctx, cs); err != nil {
		t.Fatalf("Update after wipe: %v", err)
	}
	sealed, exp, err = repo.RevertTicket(ctx, cs.ID)
	if err != nil {
		t.Fatalf("RevertTicket after wipe: %v", err)
	}
	if len(sealed) != 0 || exp != 0 {
		t.Fatalf("an ordinary Update resurrected a wiped ticket: %q / %d", sealed, exp)
	}
}

// TestChangesetRepo_WipeRevertTicket covers the wipe's contract directly:
// idempotent, and never an error for a changeset that has (or is) nothing.
func TestChangesetRepo_WipeRevertTicket(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repo := NewChangesetRepo(db)

	cs := Changeset{ID: NewULID(), Title: "t", Author: "root@pam", Status: "draft", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}
	if err := repo.Insert(ctx, cs); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := repo.WipeRevertTicket(ctx, cs.ID); err != nil {
			t.Fatalf("WipeRevertTicket call %d: %v", i, err)
		}
	}
	if err := repo.WipeRevertTicket(ctx, "no-such-changeset"); err != nil {
		t.Errorf("WipeRevertTicket on an unknown id = %v, want nil (the wipe must never be the reason a terminal transition fails)", err)
	}
	if _, _, err := repo.RevertTicket(ctx, "no-such-changeset"); err != ErrNotFound {
		t.Errorf("RevertTicket on an unknown id = %v, want ErrNotFound", err)
	}
}

// TestChangesetRepo_WipeExpiredRevertTickets covers the expiry sweep: only
// tickets at or past their expiry are cleared, and a live one is untouched.
func TestChangesetRepo_WipeExpiredRevertTickets(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repo := NewChangesetRepo(db)

	mk := func(id string, expiresAt int64) string {
		t.Helper()
		cs := Changeset{ID: NewULID(), Title: id, Author: "root@pam", Status: "awaiting_confirm", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}
		if err := repo.Insert(ctx, cs); err != nil {
			t.Fatalf("Insert(%s): %v", id, err)
		}
		if err := repo.SealRevertTicket(ctx, cs.ID, []byte("sealed-"+id), expiresAt); err != nil {
			t.Fatalf("SealRevertTicket(%s): %v", id, err)
		}
		return cs.ID
	}

	const now = 1_000
	expired := mk("expired", now-1)
	exactly := mk("exactly-now", now)
	live := mk("live", now+1)

	n, werr := repo.WipeExpiredRevertTickets(ctx, now)
	if werr != nil {
		t.Fatalf("WipeExpiredRevertTickets: %v", werr)
	}
	if n != 2 {
		t.Errorf("wiped %d rows, want 2 (past and exactly-at expiry)", n)
	}
	for _, id := range []string{expired, exactly} {
		sealed, exp, rerr := repo.RevertTicket(ctx, id)
		if rerr != nil {
			t.Fatalf("RevertTicket: %v", rerr)
		}
		if len(sealed) != 0 || exp != 0 {
			t.Errorf("expired ticket %s survived the sweep", id)
		}
	}
	sealed, exp, err := repo.RevertTicket(ctx, live)
	if err != nil {
		t.Fatalf("RevertTicket(live): %v", err)
	}
	if len(sealed) == 0 || exp != now+1 {
		t.Errorf("the sweep cleared a still-valid ticket")
	}
}
