// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestAPITokenRepo_CreateGetListRevoke(t *testing.T) {
	db := openTestDB(t)
	repo := NewAPITokenRepo(db)
	ctx := context.Background()

	tok := APIToken{
		ID:         "tok1",
		Name:       "ci-runner",
		TokenHash:  "hash1",
		ScopesJSON: `["netRead","automation"]`,
		CreatedBy:  "root@pam",
		CreatedAt:  100,
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "tok1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != tok.Name || got.TokenHash != tok.TokenHash || got.ScopesJSON != tok.ScopesJSON {
		t.Errorf("Get() = %+v, want fields matching %+v", got, tok)
	}
	if got.LastUsedAt.Valid || got.RevokedAt.Valid {
		t.Errorf("Get() new token should have no last_used_at/revoked_at, got %+v", got)
	}

	byHash, err := repo.GetByHash(ctx, "hash1")
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if byHash.ID != "tok1" {
		t.Errorf("GetByHash().ID = %q, want tok1", byHash.ID)
	}

	if _, hashErr := repo.GetByHash(ctx, "no-such-hash"); !errors.Is(hashErr, ErrNotFound) {
		t.Errorf("GetByHash(missing) = %v, want ErrNotFound", hashErr)
	}

	if createErr := repo.Create(ctx, APIToken{
		ID: "tok2", Name: "second", TokenHash: "hash2", ScopesJSON: `["netRead"]`,
		CreatedBy: "root@pam", CreatedAt: 200,
	}); createErr != nil {
		t.Fatalf("Create second: %v", createErr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	// Newest-first.
	if list[0].ID != "tok2" || list[1].ID != "tok1" {
		t.Errorf("List() order = [%s, %s], want [tok2, tok1]", list[0].ID, list[1].ID)
	}

	if updateErr := repo.UpdateLastUsed(ctx, "tok1", 150); updateErr != nil {
		t.Fatalf("UpdateLastUsed: %v", updateErr)
	}
	got, err = repo.Get(ctx, "tok1")
	if err != nil {
		t.Fatalf("Get after UpdateLastUsed: %v", err)
	}
	if !got.LastUsedAt.Valid || got.LastUsedAt.Int64 != 150 {
		t.Errorf("Get().LastUsedAt = %+v, want valid 150", got.LastUsedAt)
	}

	if revokeErr := repo.Revoke(ctx, "tok1", 300); revokeErr != nil {
		t.Fatalf("Revoke: %v", revokeErr)
	}
	got, err = repo.Get(ctx, "tok1")
	if err != nil {
		t.Fatalf("Get after Revoke: %v", err)
	}
	if !got.RevokedAt.Valid || got.RevokedAt.Int64 != 300 {
		t.Errorf("Get().RevokedAt = %+v, want valid 300", got.RevokedAt)
	}

	// A second revoke of an already-revoked token is a no-op success, not
	// an error — only a genuinely unknown id 404s.
	if err := repo.Revoke(ctx, "tok1", 400); err != nil {
		t.Errorf("Revoke(already revoked) = %v, want nil (no-op)", err)
	}
	got, _ = repo.Get(ctx, "tok1")
	if got.RevokedAt.Int64 != 300 {
		t.Errorf("double-Revoke must not move revoked_at forward, got %d, want 300", got.RevokedAt.Int64)
	}

	if err := repo.Revoke(ctx, "no-such-id", 500); !errors.Is(err, ErrNotFound) {
		t.Errorf("Revoke(unknown id) = %v, want ErrNotFound", err)
	}

	if _, err := repo.Get(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(unknown id) = %v, want ErrNotFound", err)
	}
}

func TestAPITokenRepo_TokenHashIsUnique(t *testing.T) {
	db := openTestDB(t)
	repo := NewAPITokenRepo(db)
	ctx := context.Background()

	if err := repo.Create(ctx, APIToken{ID: "a", Name: "a", TokenHash: "dup", ScopesJSON: "[]", CreatedBy: "u", CreatedAt: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := repo.Create(ctx, APIToken{ID: "b", Name: "b", TokenHash: "dup", ScopesJSON: "[]", CreatedBy: "u", CreatedAt: 2})
	if err == nil {
		t.Fatal("Create with duplicate token_hash should fail the unique index")
	}
}
