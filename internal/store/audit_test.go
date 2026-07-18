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

// T-1007: GET /history/events' changeset-lifecycle input.
func TestAuditRepo_ListActionsInRange(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	rows := []AuditEntry{
		{At: 1000, Username: "root@pam", Action: "changeset.apply", Result: "applying", ChangesetID: sql.NullString{String: "cs-1", Valid: true}},
		{At: 2000, Username: "root@pam", Action: "changeset.confirm", Result: "committed", ChangesetID: sql.NullString{String: "cs-1", Valid: true}},
		// Not in the lifecycle set: must never appear in ListActionsInRange's
		// output even though it's within the same time range.
		{At: 1500, Username: "root@pam", Action: "session.login", Result: "ok"},
		{At: 3000, Username: "root@pam", Action: "changeset.rollback", Result: "rolled_back", ChangesetID: sql.NullString{String: "cs-2", Valid: true}},
	}
	for _, e := range rows {
		if _, err := repo.Append(ctx, e); err != nil {
			t.Fatalf("Append(%+v): %v", e, err)
		}
	}

	t.Run("unbounded", func(t *testing.T) {
		got, err := repo.ListActionsInRange(ctx, ChangesetLifecycleActions, 0, 0)
		if err != nil {
			t.Fatalf("ListActionsInRange: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("ListActionsInRange(unbounded) len = %d, want 3 (apply/confirm/rollback, not session.login)", len(got))
		}
		for i := 1; i < len(got); i++ {
			if got[i].At < got[i-1].At {
				t.Errorf("results not ascending by at: %+v", got)
			}
		}
	})

	t.Run("bounded", func(t *testing.T) {
		got, err := repo.ListActionsInRange(ctx, ChangesetLifecycleActions, 1000, 2000)
		if err != nil {
			t.Fatalf("ListActionsInRange: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListActionsInRange(1000,2000) len = %d, want 2 (apply@1000, confirm@2000)", len(got))
		}
		if got[0].Action != "changeset.apply" || got[1].Action != "changeset.confirm" {
			t.Errorf("ListActionsInRange(1000,2000) = %+v, want apply then confirm", got)
		}
	})

	t.Run("empty actions", func(t *testing.T) {
		got, err := repo.ListActionsInRange(ctx, nil, 0, 0)
		if err != nil {
			t.Fatalf("ListActionsInRange(nil actions): %v", err)
		}
		if got != nil {
			t.Errorf("ListActionsInRange(nil actions) = %+v, want nil", got)
		}
	})
}
