// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTcMirrorSessionRepo_InsertGetActiveExpireDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewTcMirrorSessionRepo(db)
	ctx := context.Background()

	s := TcMirrorSession{
		ID: "span1", Node: "pve1", SourceIface: "vmbr0", DestIface: "vmbr99",
		MaxDurationSec: 3600, Status: TcMirrorSessionActive,
		CreatedBy: "root@pam", StartedAt: 100, ExpiresAt: 3700,
	}
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Errorf("Get() = %+v, want %+v", got, s)
	}

	s2 := TcMirrorSession{
		ID: "span2", Node: "pve1", SourceIface: "vmbr1", DestIface: "vmbr99",
		MaxMbit: intPtr(50), MaxDurationSec: 60, Status: TcMirrorSessionActive,
		CreatedBy: "root@pam", StartedAt: 100, ExpiresAt: 160,
	}
	if insertErr := repo.Insert(ctx, s2); insertErr != nil {
		t.Fatalf("Insert second: %v", insertErr)
	}
	got2, err := repo.Get(ctx, s2.ID)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if !reflect.DeepEqual(got2, s2) {
		t.Errorf("Get(second) = %+v, want %+v (nullable maxMbit round-trips)", got2, s2)
	}

	active, err := repo.ActiveByNode(ctx, "pve1")
	if err != nil {
		t.Fatalf("ActiveByNode: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("ActiveByNode(pve1) len = %d, want 2", len(active))
	}

	// s2 (expires_at=160) is due at now=200; s (expires_at=3700) is not.
	due, err := repo.DueForExpiry(ctx, 200)
	if err != nil {
		t.Fatalf("DueForExpiry: %v", err)
	}
	if len(due) != 1 || due[0].ID != s2.ID {
		t.Fatalf("DueForExpiry(200) = %+v, want only %s", due, s2.ID)
	}

	if statusErr := repo.SetStatus(ctx, s2.ID, TcMirrorSessionExpired, 200); statusErr != nil {
		t.Fatalf("SetStatus: %v", statusErr)
	}
	got2, err = repo.Get(ctx, s2.ID)
	if err != nil {
		t.Fatalf("Get after SetStatus: %v", err)
	}
	if got2.Status != TcMirrorSessionExpired || got2.StoppedAt == nil || *got2.StoppedAt != 200 {
		t.Errorf("Get after SetStatus = %+v, want status=expired stoppedAt=200", got2)
	}

	active, err = repo.ActiveByNode(ctx, "pve1")
	if err != nil {
		t.Fatalf("ActiveByNode after expiry: %v", err)
	}
	if len(active) != 1 || active[0].ID != s.ID {
		t.Fatalf("ActiveByNode(pve1) after expiry = %+v, want only %s", active, s.ID)
	}

	if durErr := repo.UpdateDuration(ctx, s.ID, 7200, 7300); durErr != nil {
		t.Fatalf("UpdateDuration: %v", durErr)
	}
	got, err = repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get after UpdateDuration: %v", err)
	}
	if got.MaxDurationSec != 7200 || got.ExpiresAt != 7300 {
		t.Errorf("Get after UpdateDuration = %+v, want maxDurationSec=7200 expiresAt=7300", got)
	}

	if err := repo.UpdateDuration(ctx, "missing", 1, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateDuration(missing) error = %v, want ErrNotFound", err)
	}
	// An already-expired session is not "active", so re-arming it is also
	// ErrNotFound (UpdateDuration is scoped to status=active).
	if err := repo.UpdateDuration(ctx, s2.ID, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateDuration(expired) error = %v, want ErrNotFound", err)
	}

	if err := repo.Delete(ctx, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete error = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent row is not an error (rollback convergence).
	if err := repo.Delete(ctx, s.ID); err != nil {
		t.Errorf("Delete(already-absent) = %v, want nil", err)
	}
}
