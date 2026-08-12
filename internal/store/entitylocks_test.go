package store

import (
	"context"
	"errors"
	"testing"
)

func seedLock(t *testing.T, repo *EntityLockRepo, ref, changesetID, holder, session string, expires int64) {
	t.Helper()
	if err := repo.Upsert(context.Background(), EntityLock{
		Ref:         ref,
		ChangesetID: changesetID,
		Holder:      holder,
		SessionID:   session,
		AcquiredAt:  1000,
		ExpiresAt:   expires,
	}); err != nil {
		t.Fatalf("Upsert(%s): %v", ref, err)
	}
}

func TestEntityLockRepo_UpsertGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewEntityLockRepo(db)
	ctx := context.Background()

	seedLock(t, repo, "bridge:pve1:vmbr0", "cs-1", "alice@pam", "sess-a", 2000)
	seedLock(t, repo, "bridge:pve1:vmbr1", "cs-1", "alice@pam", "sess-a", 2000)

	got, err := repo.Get(ctx, "bridge:pve1:vmbr0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Holder != "alice@pam" || got.ChangesetID != "cs-1" || got.SessionID != "sess-a" ||
		got.AcquiredAt != 1000 || got.ExpiresAt != 2000 {
		t.Errorf("Get = %+v, want the seeded row", got)
	}

	if _, getErr := repo.Get(ctx, "bridge:pve1:nope"); !errors.Is(getErr, ErrNotFound) {
		t.Errorf("Get(absent) error = %v, want ErrNotFound", getErr)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 || all[0].Ref != "bridge:pve1:vmbr0" || all[1].Ref != "bridge:pve1:vmbr1" {
		t.Errorf("List = %+v, want both refs in ref order", all)
	}

	// Takeover: the PRIMARY KEY on ref is what makes one-holder-per-entity a
	// constraint. A second Upsert replaces the holder, it does not add a row.
	seedLock(t, repo, "bridge:pve1:vmbr0", "cs-2", "bob@pam", "sess-b", 3000)
	got, err = repo.Get(ctx, "bridge:pve1:vmbr0")
	if err != nil {
		t.Fatalf("Get after takeover: %v", err)
	}
	if got.Holder != "bob@pam" || got.ChangesetID != "cs-2" || got.SessionID != "sess-b" {
		t.Errorf("after takeover Get = %+v, want bob@pam/cs-2/sess-b", got)
	}
	all, err = repo.List(ctx)
	if err != nil {
		t.Fatalf("List after takeover: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List after takeover has %d rows, want 2 (a takeover replaces, never appends)", len(all))
	}
}

func TestEntityLockRepo_DeletePaths(t *testing.T) {
	db := openTestDB(t)
	repo := NewEntityLockRepo(db)
	ctx := context.Background()

	seedLock(t, repo, "bridge:pve1:vmbr0", "cs-1", "alice@pam", "sess-a", 2000)
	seedLock(t, repo, "bridge:pve1:vmbr1", "cs-1", "alice@pam", "sess-a", 2000)
	seedLock(t, repo, "bridge:pve2:vmbr0", "cs-2", "bob@pam", "sess-b", 2000)
	seedLock(t, repo, "bridge:pve3:vmbr0", "cs-3", "carol@pam", "", 1500)

	// DeleteBySession is the dropped-connection path: it frees exactly that
	// session's locks and nobody else's.
	n, err := repo.DeleteBySession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteBySession released %d, want 2", n)
	}
	if _, getErr := repo.Get(ctx, "bridge:pve2:vmbr0"); getErr != nil {
		t.Errorf("another session's lock was released too: %v", getErr)
	}

	// An empty session id must match nothing — "" is the sentinel for "not
	// bound to a live connection", not a selector.
	n, err = repo.DeleteBySession(ctx, "")
	if err != nil {
		t.Fatalf("DeleteBySession(\"\"): %v", err)
	}
	if n != 0 {
		t.Errorf("DeleteBySession(\"\") released %d rows, want 0", n)
	}
	if _, getErr := repo.Get(ctx, "bridge:pve3:vmbr0"); getErr != nil {
		t.Errorf("the session-less lock was released by an empty session id: %v", getErr)
	}

	n, err = repo.DeleteByChangeset(ctx, "cs-2")
	if err != nil {
		t.Fatalf("DeleteByChangeset: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteByChangeset released %d, want 1", n)
	}

	// Sweeping at 1500 removes the expires_at == 1500 row (expiry is
	// inclusive) and nothing else.
	seedLock(t, repo, "bridge:pve4:vmbr0", "cs-4", "dave@pam", "sess-d", 9000)
	n, err = repo.DeleteExpired(ctx, 1500)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteExpired released %d, want 1", n)
	}
	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Ref != "bridge:pve4:vmbr0" {
		t.Errorf("List after sweep = %+v, want only the unexpired lock", all)
	}

	// Releasing an absent lock is not an error: every release path is
	// idempotent (a session can disconnect twice).
	if err := repo.DeleteRef(ctx, "bridge:pve9:absent"); err != nil {
		t.Errorf("DeleteRef(absent) = %v, want nil", err)
	}
	if err := repo.DeleteRef(ctx, "bridge:pve4:vmbr0"); err != nil {
		t.Errorf("DeleteRef: %v", err)
	}
	if _, err := repo.Get(ctx, "bridge:pve4:vmbr0"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after DeleteRef = %v, want ErrNotFound", err)
	}
}
