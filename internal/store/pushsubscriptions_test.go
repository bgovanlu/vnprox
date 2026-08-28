// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

// seedSession inserts a minimal, valid sessions row and returns its id —
// push_subscriptions.session_id is a foreign key (0046's doc comment), so
// every test in this file needs a live session to attach a subscription to.
func seedSession(t *testing.T, db *DB, id string) {
	t.Helper()
	key := make([]byte, KeySize)
	cipher, err := NewSessionCipher(key)
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	repo := NewSessionRepo(db, cipher)
	if err := repo.Insert(context.Background(), Session{
		ID: id, Username: "alice@pam", Realm: "pam", PVETicket: "t", CSRFToken: "c",
		CapsJSON: "{}", CreatedAt: 1, ExpiresAt: 999999999,
	}); err != nil {
		t.Fatalf("seeding session %s: %v", id, err)
	}
}

func TestPushSubscriptionRepo_CreateGetListDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedSession(t, db, "sess-1")
	repo := NewPushSubscriptionRepo(db)

	sub := PushSubscription{
		ID: "push1", SessionID: "sess-1", Username: "alice@pam",
		EndpointHash: "hash1", EndpointEnc: []byte("enc-endpoint"),
		P256dhEnc: []byte("enc-p256dh"), AuthEnc: []byte("enc-auth"),
		CategoriesJSON: `["critical","awaitingConfirm"]`,
		DeviceLabel:    "iPhone — Safari",
		CreatedAt:      100,
	}
	if err := repo.Create(ctx, sub); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "push1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Username != sub.Username || got.CategoriesJSON != sub.CategoriesJSON || got.DeviceLabel != sub.DeviceLabel {
		t.Errorf("Get() = %+v, want fields matching %+v", got, sub)
	}
	if got.LastUsedAt.Valid {
		t.Errorf("new subscription LastUsedAt.Valid = true, want false")
	}

	byHash, err := repo.GetByEndpointHash(ctx, "hash1")
	if err != nil {
		t.Fatalf("GetByEndpointHash: %v", err)
	}
	if byHash.ID != "push1" {
		t.Errorf("GetByEndpointHash().ID = %q, want push1", byHash.ID)
	}

	if createErr := repo.Create(ctx, PushSubscription{
		ID: "push2", SessionID: "sess-1", Username: "alice@pam",
		EndpointHash: "hash2", EndpointEnc: []byte("e2"), P256dhEnc: []byte("p2"), AuthEnc: []byte("a2"),
		CategoriesJSON: `["drift"]`, CreatedAt: 200,
	}); createErr != nil {
		t.Fatalf("Create second: %v", createErr)
	}

	list, err := repo.ListByUsername(ctx, "alice@pam")
	if err != nil {
		t.Fatalf("ListByUsername: %v", err)
	}
	if len(list) != 2 || list[0].ID != "push2" || list[1].ID != "push1" {
		t.Fatalf("ListByUsername() = %+v, want [push2, push1] newest-first", list)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll() len = %d, want 2", len(all))
	}

	if touchErr := repo.TouchLastUsed(ctx, "push1", 555); touchErr != nil {
		t.Fatalf("TouchLastUsed: %v", touchErr)
	}
	touched, err := repo.Get(ctx, "push1")
	if err != nil {
		t.Fatalf("Get after touch: %v", err)
	}
	if !touched.LastUsedAt.Valid || touched.LastUsedAt.Int64 != 555 {
		t.Errorf("LastUsedAt after touch = %+v, want valid 555", touched.LastUsedAt)
	}

	if err := repo.Delete(ctx, "push2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "push2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted) = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent subscription is a no-op, not an error.
	if err := repo.Delete(ctx, "push2"); err != nil {
		t.Errorf("Delete(already deleted) = %v, want nil", err)
	}
}

func TestPushSubscriptionRepo_DeleteByEndpointHash(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedSession(t, db, "sess-1")
	repo := NewPushSubscriptionRepo(db)

	if err := repo.Create(ctx, PushSubscription{
		ID: "push1", SessionID: "sess-1", Username: "alice@pam",
		EndpointHash: "hash1", EndpointEnc: []byte("e"), P256dhEnc: []byte("p"), AuthEnc: []byte("a"),
		CategoriesJSON: `["critical"]`, CreatedAt: 100,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.DeleteByEndpointHash(ctx, "hash1"); err != nil {
		t.Fatalf("DeleteByEndpointHash: %v", err)
	}
	if _, err := repo.GetByEndpointHash(ctx, "hash1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByEndpointHash(deleted) = %v, want ErrNotFound", err)
	}
}

// TestPushSubscriptionRepo_DiesWithItsSession is the schema-level half of
// T-2005's "subscriptions are per-session and die with it": deleting the
// owning sessions row (what internal/auth's logout path does) must remove
// every push_subscriptions row that pointed at it, via the ON DELETE CASCADE
// foreign key declared in 0046_push_subscriptions.sql — not because some
// handler remembered to clean up, but because the schema enforces it.
//
// This is the POSITIVE leg proving the mechanism actually fires: a second
// subscription tied to a DIFFERENT, still-live session must survive the
// same delete untouched, so a passing test can't be explained by "Delete
// wipes the whole table" or some other vacuous behavior.
func TestPushSubscriptionRepo_DiesWithItsSession(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedSession(t, db, "sess-dying")
	seedSession(t, db, "sess-alive")
	repo := NewPushSubscriptionRepo(db)

	if err := repo.Create(ctx, PushSubscription{
		ID: "push-dying", SessionID: "sess-dying", Username: "alice@pam",
		EndpointHash: "hash-dying", EndpointEnc: []byte("e"), P256dhEnc: []byte("p"), AuthEnc: []byte("a"),
		CategoriesJSON: `["critical"]`, CreatedAt: 100,
	}); err != nil {
		t.Fatalf("Create dying: %v", err)
	}
	if err := repo.Create(ctx, PushSubscription{
		ID: "push-alive", SessionID: "sess-alive", Username: "bob@pam",
		EndpointHash: "hash-alive", EndpointEnc: []byte("e"), P256dhEnc: []byte("p"), AuthEnc: []byte("a"),
		CategoriesJSON: `["drift"]`, CreatedAt: 100,
	}); err != nil {
		t.Fatalf("Create alive: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, "sess-dying"); err != nil {
		t.Fatalf("deleting session: %v", err)
	}

	if _, err := repo.Get(ctx, "push-dying"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(push-dying) after session delete = %v, want ErrNotFound (cascade should have removed it)", err)
	}
	stillAlive, err := repo.Get(ctx, "push-alive")
	if err != nil {
		t.Fatalf("Get(push-alive) after unrelated session delete: %v, want it to still exist", err)
	}
	if stillAlive.SessionID != "sess-alive" {
		t.Errorf("push-alive.SessionID = %q, want sess-alive", stillAlive.SessionID)
	}
}
