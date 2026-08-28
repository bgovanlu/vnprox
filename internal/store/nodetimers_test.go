// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestNodeTimerRepo_ArmGetResolve(t *testing.T) {
	db := openTestDB(t)
	repo := NewNodeTimerRepo(db)
	ctx := context.Background()

	if _, err := repo.Get(ctx, "cs-1", "pve2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before arm: err = %v, want ErrNotFound", err)
	}

	if err := repo.Arm(ctx, NodeTimer{
		ChangesetID: "cs-1", Node: "pve2", PreContent: "iface content", Deadline: 1000, ArmedAt: 500,
	}); err != nil {
		t.Fatalf("Arm: %v", err)
	}

	got, err := repo.Get(ctx, "cs-1", "pve2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != NodeTimerArmed || got.PreContent != "iface content" || got.Deadline != 1000 {
		t.Errorf("Get() = %+v, want armed row", got)
	}
	if got.ResolvedAt.Valid {
		t.Errorf("Get().ResolvedAt = %+v, want NULL for a freshly-armed timer", got.ResolvedAt)
	}

	armed, err := repo.ListByStatus(ctx, NodeTimerArmed)
	if err != nil {
		t.Fatalf("ListByStatus(armed): %v", err)
	}
	if len(armed) != 1 || armed[0].ChangesetID != "cs-1" {
		t.Errorf("ListByStatus(armed) = %+v, want [cs-1/pve2]", armed)
	}

	if resolveErr := repo.Resolve(ctx, "cs-1", "pve2", NodeTimerCancelled, 900, ""); resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	got, err = repo.Get(ctx, "cs-1", "pve2")
	if err != nil {
		t.Fatalf("Get after resolve: %v", err)
	}
	if got.Status != NodeTimerCancelled || !got.ResolvedAt.Valid || got.ResolvedAt.Int64 != 900 {
		t.Errorf("Get() after resolve = %+v, want cancelled/resolvedAt=900", got)
	}
	if got.Error.Valid {
		t.Errorf("Get().Error = %+v, want NULL for a clean cancel", got.Error)
	}

	armed, err = repo.ListByStatus(ctx, NodeTimerArmed)
	if err != nil {
		t.Fatalf("ListByStatus(armed) after resolve: %v", err)
	}
	if len(armed) != 0 {
		t.Errorf("ListByStatus(armed) after resolve = %+v, want none", armed)
	}
}

func TestNodeTimerRepo_ReArmOverwrites(t *testing.T) {
	db := openTestDB(t)
	repo := NewNodeTimerRepo(db)
	ctx := context.Background()

	if err := repo.Arm(ctx, NodeTimer{ChangesetID: "cs-1", Node: "pve1", PreContent: "v1", Deadline: 100, ArmedAt: 1}); err != nil {
		t.Fatalf("first Arm: %v", err)
	}
	if err := repo.Resolve(ctx, "cs-1", "pve1", NodeTimerRolledBack, 150, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Re-arming the same key (a fresh apply of a differently-shaped changeset
	// reusing an id would never happen, but a retried arm-timer call on the
	// same changeset/node must cleanly reset any prior resolution) clears the
	// old resolved_at/error and content.
	if err := repo.Arm(ctx, NodeTimer{ChangesetID: "cs-1", Node: "pve1", PreContent: "v2", Deadline: 200, ArmedAt: 160}); err != nil {
		t.Fatalf("re-Arm: %v", err)
	}
	got, err := repo.Get(ctx, "cs-1", "pve1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != NodeTimerArmed || got.PreContent != "v2" || got.Deadline != 200 || got.ResolvedAt.Valid {
		t.Errorf("Get() after re-arm = %+v, want fresh armed row with v2/200", got)
	}
}

func TestNodeTimerRepo_ResolveRollbackFailedRecordsError(t *testing.T) {
	db := openTestDB(t)
	repo := NewNodeTimerRepo(db)
	ctx := context.Background()

	if err := repo.Arm(ctx, NodeTimer{ChangesetID: "cs-9", Node: "pve3", PreContent: "x", Deadline: 10, ArmedAt: 1}); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	if err := repo.Resolve(ctx, "cs-9", "pve3", NodeTimerRollbackFailed, 11, "reload: exit status 1"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, err := repo.Get(ctx, "cs-9", "pve3")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != NodeTimerRollbackFailed || !got.Error.Valid || got.Error.String != "reload: exit status 1" {
		t.Errorf("Get() = %+v, want rollback_failed with error detail", got)
	}
}

func TestNodeTimerRepo_ResolveMissingRowIsNoop(t *testing.T) {
	db := openTestDB(t)
	repo := NewNodeTimerRepo(db)
	ctx := context.Background()

	if err := repo.Resolve(ctx, "cs-missing", "pve1", NodeTimerCancelled, 1, ""); err != nil {
		t.Fatalf("Resolve on missing row: %v", err)
	}
	if _, err := repo.Get(ctx, "cs-missing", "pve1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after no-op resolve: err = %v, want ErrNotFound", err)
	}
}
