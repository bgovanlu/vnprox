package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestWebhookRepo_CreateGetListDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewWebhookRepo(db)
	ctx := context.Background()

	wh := Webhook{
		ID:         "wh1",
		URL:        "https://example.com/hook",
		EventsJSON: sql.NullString{String: `["changeset.status"]`, Valid: true},
		SecretEnc:  []byte("ciphertext"),
		CreatedBy:  "root@pam",
		CreatedAt:  100,
	}
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "wh1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URL != wh.URL || got.EventsJSON.String != wh.EventsJSON.String || string(got.SecretEnc) != string(wh.SecretEnc) {
		t.Errorf("Get() = %+v, want fields matching %+v", got, wh)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("new webhook ConsecutiveFailures = %d, want 0", got.ConsecutiveFailures)
	}

	if createErr := repo.Create(ctx, Webhook{ID: "wh2", URL: "https://example.com/hook2", SecretEnc: []byte("x"), CreatedBy: "root@pam", CreatedAt: 200}); createErr != nil {
		t.Fatalf("Create second: %v", createErr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != "wh2" || list[1].ID != "wh1" {
		t.Fatalf("List() = %+v, want [wh2, wh1] newest-first", list)
	}

	if err := repo.Delete(ctx, "wh2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "wh2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(deleted) = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent webhook is a no-op, not an error.
	if err := repo.Delete(ctx, "wh2"); err != nil {
		t.Errorf("Delete(already deleted) = %v, want nil", err)
	}
}

func TestWebhookRepo_RecordSuccessAndFailure(t *testing.T) {
	db := openTestDB(t)
	repo := NewWebhookRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, Webhook{ID: "wh1", URL: "https://example.com", SecretEnc: []byte("s"), CreatedBy: "u", CreatedAt: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	count, err := repo.RecordFailure(ctx, "wh1", 10, "connection refused")
	if err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if count != 1 {
		t.Errorf("RecordFailure count = %d, want 1", count)
	}
	count, err = repo.RecordFailure(ctx, "wh1", 20, "timeout")
	if err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if count != 2 {
		t.Errorf("RecordFailure count = %d, want 2", count)
	}

	got, err := repo.Get(ctx, "wh1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConsecutiveFailures != 2 || !got.LastError.Valid || got.LastError.String != "timeout" {
		t.Errorf("Get() after 2 failures = %+v", got)
	}
	if !got.LastAttemptAt.Valid || got.LastAttemptAt.Int64 != 20 {
		t.Errorf("LastAttemptAt = %+v, want valid 20", got.LastAttemptAt)
	}

	if successErr := repo.RecordSuccess(ctx, "wh1", 30); successErr != nil {
		t.Fatalf("RecordSuccess: %v", successErr)
	}
	got, err = repo.Get(ctx, "wh1")
	if err != nil {
		t.Fatalf("Get after success: %v", err)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after success = %d, want 0", got.ConsecutiveFailures)
	}
	if got.LastError.Valid {
		t.Errorf("LastError after success = %+v, want cleared", got.LastError)
	}
	if !got.LastSuccessAt.Valid || got.LastSuccessAt.Int64 != 30 {
		t.Errorf("LastSuccessAt = %+v, want valid 30", got.LastSuccessAt)
	}

	if _, err := repo.RecordFailure(ctx, "no-such-id", 1, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RecordFailure(unknown id) = %v, want ErrNotFound", err)
	}
	if err := repo.RecordSuccess(ctx, "no-such-id", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("RecordSuccess(unknown id) = %v, want ErrNotFound", err)
	}
}
