// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"
)

// The distinct-approver guarantee T-2604's two-person rule rests on is a
// STORAGE property, and this is where it is asserted: one row per
// (changeset, principal), whatever the credential, session, or number of
// clicks behind each approval.
func TestChangesetSignoffRepo_SamePrincipalUpsertsRatherThanAccumulates(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSignoffRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")

	for _, at := range []int64{100, 200, 300} {
		if err := repo.Upsert(ctx, ChangesetSignoff{ChangesetID: "cs1", Principal: "bob", DecidedAt: at}); err != nil {
			t.Fatalf("Upsert(bob, %d): %v", at, err)
		}
	}
	got, listErr := repo.List(ctx, "cs1")
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d (%+v), want 1 — one person is one approver", len(got), got)
	}
	if got[0].DecidedAt != 300 {
		t.Errorf("DecidedAt = %d, want the most recent approval (300)", got[0].DecidedAt)
	}

	// CONTROL LEG: a different principal really does add a row, so the
	// assertion above is about identity and not about Upsert never inserting.
	if err := repo.Upsert(ctx, ChangesetSignoff{ChangesetID: "cs1", Principal: "carol", DecidedAt: 400}); err != nil {
		t.Fatalf("Upsert(carol): %v", err)
	}
	got, listErr = repo.List(ctx, "cs1")
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(got) != 2 || got[0].Principal != "bob" || got[1].Principal != "carol" {
		t.Fatalf("rows = %+v, want bob and carol in principal order", got)
	}
}

func TestChangesetSignoffRepo_DeleteAndClear(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSignoffRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")
	seedChangesetForApproval(t, db, "cs2")

	for _, who := range []string{"bob", "carol"} {
		if err := repo.Upsert(ctx, ChangesetSignoff{ChangesetID: "cs1", Principal: who, DecidedAt: 1}); err != nil {
			t.Fatalf("Upsert(%s): %v", who, err)
		}
	}
	if err := repo.Upsert(ctx, ChangesetSignoff{ChangesetID: "cs2", Principal: "bob", DecidedAt: 1}); err != nil {
		t.Fatalf("Upsert(cs2/bob): %v", err)
	}

	if err := repo.Delete(ctx, "cs1", "carol"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, _ := repo.List(ctx, "cs1"); len(got) != 1 || got[0].Principal != "bob" {
		t.Fatalf("after Delete, rows = %+v, want only bob", got)
	}
	// Deleting an absent sign-off is not an error.
	if err := repo.Delete(ctx, "cs1", "nobody"); err != nil {
		t.Fatalf("Delete(absent): %v", err)
	}

	if err := repo.Clear(ctx, "cs1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got, _ := repo.List(ctx, "cs1"); len(got) != 0 {
		t.Fatalf("after Clear, rows = %+v, want none", got)
	}
	// Clear is scoped to one changeset: another changeset's sign-offs stand.
	if got, _ := repo.List(ctx, "cs2"); len(got) != 1 {
		t.Fatalf("cs2's rows = %+v, want cs1's Clear to have left them alone", got)
	}
}

func TestChangesetSignoffRepo_CascadesWithItsChangeset(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSignoffRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")
	if err := repo.Upsert(ctx, ChangesetSignoff{ChangesetID: "cs1", Principal: "bob", DecidedAt: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM changesets WHERE id = 'cs1'`); err != nil {
		t.Fatalf("deleting the changeset: %v", err)
	}
	if got, _ := repo.List(ctx, "cs1"); len(got) != 0 {
		t.Fatalf("rows = %+v, want the sign-offs to have cascaded away with their changeset", got)
	}
}
