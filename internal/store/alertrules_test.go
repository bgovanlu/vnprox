package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestAlertRuleRepo_InsertGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	ctx := context.Background()

	a := AlertRule{
		ID: "01A", Name: "Errors to Slack", Enabled: true,
		SeverityFilter: []string{"error"}, TargetKind: "slack",
		TargetURL: "https://hooks.slack.com/services/x", CreatedAt: 100, UpdatedAt: 100,
	}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, a) {
		t.Errorf("Get() = %+v, want %+v", got, a)
	}

	b := AlertRule{
		ID: "01B", Name: "Health to Gotify", Enabled: false,
		SourceFilter: []string{"health"}, TargetKind: "gotify",
		TargetURL: "https://gotify.example/message", TargetSecretEnc: []byte("cipher"),
		CreatedAt: 200, UpdatedAt: 200,
	}
	if insertErr := repo.Insert(ctx, b); insertErr != nil {
		t.Fatalf("Insert second: %v", insertErr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	// Ordered by name ASC: "Errors to Slack" < "Health to Gotify".
	if list[0].ID != a.ID || list[1].ID != b.ID {
		t.Errorf("List() order = [%s, %s], want [%s, %s]", list[0].ID, list[1].ID, a.ID, b.ID)
	}
	if !reflect.DeepEqual(list[1].TargetSecretEnc, b.TargetSecretEnc) {
		t.Errorf("List()[1].TargetSecretEnc = %v, want %v", list[1].TargetSecretEnc, b.TargetSecretEnc)
	}
}

func TestAlertRuleRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestAlertRuleRepo_Update(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	ctx := context.Background()

	a := AlertRule{
		ID: "01C", Name: "orig", Enabled: true, TargetKind: "generic",
		TargetURL: "https://example.com/hook", CreatedAt: 100, UpdatedAt: 100,
	}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	updated := a
	updated.Name = "renamed"
	updated.Enabled = false
	updated.SourceFilter = []string{"drift", "health"}
	updated.SeverityFilter = []string{"warning", "error"}
	updated.TargetURL = "https://example.com/hook2"
	updated.TargetSecretEnc = []byte("newsecret")
	updated.UpdatedAt = 200
	if err := repo.Update(ctx, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Errorf("Get() after Update = %+v, want %+v", got, updated)
	}
}

func TestAlertRuleRepo_UpdateNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	err := repo.Update(context.Background(), AlertRule{ID: "missing", Name: "x", TargetKind: "generic", TargetURL: "https://x"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing): got %v, want ErrNotFound", err)
	}
}

func TestAlertRuleRepo_Delete(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	ctx := context.Background()

	a := AlertRule{ID: "01D", Name: "to-delete", TargetKind: "ntfy", TargetURL: "https://ntfy.sh/x", CreatedAt: 100, UpdatedAt: 100}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
	// Deleting an already-absent rule is not an error.
	if err := repo.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("Delete(already-absent): %v, want nil", err)
	}
}

func TestAlertRuleRepo_NilFiltersRoundtrip(t *testing.T) {
	// No filter set on either dimension must round-trip as nil, not an
	// empty-but-non-nil slice — callers treat both the same ("match
	// everything"), but this pins the exact NULL-in-SQL representation the
	// migration's doc comment documents.
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	ctx := context.Background()

	a := AlertRule{ID: "01E", Name: "no-filters", TargetKind: "generic", TargetURL: "https://example.com", CreatedAt: 1, UpdatedAt: 1}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourceFilter != nil {
		t.Errorf("SourceFilter = %v, want nil", got.SourceFilter)
	}
	if got.SeverityFilter != nil {
		t.Errorf("SeverityFilter = %v, want nil", got.SeverityFilter)
	}
}
