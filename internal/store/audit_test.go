package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestAuditRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	e := AuditEntry{
		At:          100,
		Username:    "root@pam",
		Action:      "network.apply",
		Target:      sql.NullString{String: "node/pve1", Valid: true},
		ChangesetID: sql.NullString{String: "cs-1", Valid: true},
		Result:      "ok",
		DetailJSON:  sql.NullString{String: `{"ops":2}`, Valid: true},
	}

	id, err := repo.Append(ctx, e)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id == 0 {
		t.Fatal("Append returned id 0, want an assigned autoincrement id")
	}
	e.ID = id

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != e {
		t.Errorf("Get() = %+v, want %+v", got, e)
	}

	id2, err := repo.Append(ctx, AuditEntry{At: 200, Username: "root@pam", Action: "login", Result: "ok"})
	if err != nil {
		t.Fatalf("Append #2: %v", err)
	}

	all, err := repo.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List(all) len = %d, want 2", len(all))
	}
	// newest first
	if all[0].ID != id2 || all[1].ID != id {
		t.Errorf("List(all) order = [%d, %d], want [%d, %d]", all[0].ID, all[1].ID, id2, id)
	}

	byChangeset, err := repo.List(ctx, "cs-1", 0)
	if err != nil {
		t.Fatalf("List(cs-1): %v", err)
	}
	if len(byChangeset) != 1 || byChangeset[0].ID != id {
		t.Errorf("List(cs-1) = %+v, want just entry %d", byChangeset, id)
	}

	limited, err := repo.List(ctx, "", 1)
	if err != nil {
		t.Fatalf("List(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("List(limit=1) len = %d, want 1", len(limited))
	}
}

func TestAuditRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	if _, err := repo.Get(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}
