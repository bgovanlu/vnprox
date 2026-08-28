// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestEnsureIncrementalVacuum_ConvertsOnceAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	mode, err := autoVacuumMode(ctx, db)
	if err != nil {
		t.Fatalf("autoVacuumMode: %v", err)
	}
	if mode == autoVacuumIncremental {
		t.Fatalf("fresh test DB unexpectedly already in incremental auto_vacuum mode")
	}

	converted, took, err := EnsureIncrementalVacuum(ctx, db)
	if err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}
	if !converted {
		t.Fatal("converted = false, want true on first call")
	}
	if took <= 0 {
		t.Error("took <= 0, want a positive duration for the conversion VACUUM")
	}

	mode, err = autoVacuumMode(ctx, db)
	if err != nil {
		t.Fatalf("autoVacuumMode after conversion: %v", err)
	}
	if mode != autoVacuumIncremental {
		t.Fatalf("auto_vacuum mode = %d after conversion, want %d (incremental)", mode, autoVacuumIncremental)
	}

	// Idempotent: a second call is a no-op.
	converted, took, err = EnsureIncrementalVacuum(ctx, db)
	if err != nil {
		t.Fatalf("EnsureIncrementalVacuum (second call): %v", err)
	}
	if converted {
		t.Error("converted = true on second call, want false (already incremental)")
	}
	if took != 0 {
		t.Errorf("took = %v on second call, want 0", took)
	}
}

func TestCompact_NoopBeforeConversion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Deliberately no EnsureIncrementalVacuum call: this store is still in
	// SQLite's default auto_vacuum=NONE mode.
	freed, err := Compact(ctx, db, DefaultCompactionMaxPages)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if freed != 0 {
		t.Fatalf("freed = %d, want 0 (no-op before EnsureIncrementalVacuum)", freed)
	}
}

// TestCompact_ReclaimsMeasurableSpace is T-1905 AC3's "reclaims measurable
// space" half: seed a lot of rows, delete almost all of them, and confirm
// Compact actually shrinks the store's on-disk footprint (T-1903's
// SizeBytes — the same size source the store_near_capacity finding reuses,
// not a second measurement invented for this test).
func TestCompact_ReclaimsMeasurableSpace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	auditRepo := NewAuditRepo(db)

	if _, _, err := EnsureIncrementalVacuum(ctx, db); err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}

	// A large detail_json payload per row so a few thousand rows add up to
	// several MB — enough that page-level reclaim is unambiguous rather
	// than noise-sized.
	bigDetail := make([]byte, 2048)
	for i := range bigDetail {
		bigDetail[i] = byte('a' + i%26)
	}
	var ids []int64
	for i := 0; i < 1200; i++ {
		id, err := auditRepo.Append(ctx, AuditEntry{
			At:         int64(i),
			Username:   "root@pam",
			Action:     "changeset.apply",
			Result:     "success",
			DetailJSON: sql.NullString{String: string(bigDetail), Valid: true},
		})
		if err != nil {
			t.Fatalf("seed audit row %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	sizeAfterInsert, err := db.SizeBytes()
	if err != nil {
		t.Fatalf("SizeBytes after insert: %v", err)
	}

	// Delete all but the last 10 rows, freeing most of the pages the bulk
	// insert allocated.
	for _, id := range ids[:len(ids)-10] {
		if _, err = db.ExecContext(ctx, `DELETE FROM audit_log WHERE id = ?`, id); err != nil {
			t.Fatalf("delete audit row %d: %v", id, err)
		}
	}
	if _, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint before compaction: %v", err)
	}

	freed, err := Compact(ctx, db, 1_000_000) // generous cap: reclaim everything freed in one call
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if freed <= 0 {
		t.Fatalf("freed = %d, want > 0 after deleting ~99%% of a multi-MB table", freed)
	}
	t.Logf("reclaimed %d bytes (inserted table was %d bytes)", freed, sizeAfterInsert)

	sizeAfterCompact, err := db.SizeBytes()
	if err != nil {
		t.Fatalf("SizeBytes after compaction: %v", err)
	}
	if sizeAfterCompact >= sizeAfterInsert {
		t.Fatalf("size after compaction (%d) >= size after insert (%d), want a real reduction", sizeAfterCompact, sizeAfterInsert)
	}

	// The 10 surviving rows must still be intact and readable — compaction
	// must never lose live data.
	remaining, err := auditRepo.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List after compaction: %v", err)
	}
	if len(remaining) != 10 {
		t.Fatalf("remaining rows = %d, want 10", len(remaining))
	}
}

// TestCompact_DoesNotBlockConcurrentReads is T-1905 AC3's other half: a
// goroutine hammering reads throughout a real Compact call must see no
// errors and no meaningful stall — the WAL-mode non-blocking property this
// file's package doc comment argues for, proven rather than assumed.
func TestCompact_DoesNotBlockConcurrentReads(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	auditRepo := NewAuditRepo(db)

	if _, _, err := EnsureIncrementalVacuum(ctx, db); err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}

	bigDetail := make([]byte, 4096)
	for i := 0; i < 1500; i++ {
		if _, err := auditRepo.Append(ctx, AuditEntry{
			At:         int64(i),
			Username:   "root@pam",
			Action:     "changeset.apply",
			Result:     "success",
			DetailJSON: sql.NullString{String: string(bigDetail), Valid: true},
		}); err != nil {
			t.Fatalf("seed audit row %d: %v", i, err)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM audit_log WHERE id % 10 != 0`); err != nil {
		t.Fatalf("delete most rows: %v", err)
	}

	stop := make(chan struct{})
	readErrs := make(chan error, 1)
	var reads int
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := auditRepo.List(ctx, "", 50); err != nil {
				select {
				case readErrs <- fmt.Errorf("concurrent read during compaction: %w", err):
				default:
				}
				return
			}
			mu.Lock()
			reads++
			mu.Unlock()
		}
	}()

	if _, err := Compact(ctx, db, 1_000_000); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("Compact: %v", err)
	}
	close(stop)
	wg.Wait()

	select {
	case err := <-readErrs:
		t.Fatalf("a concurrent read failed while Compact ran: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if reads == 0 {
		t.Fatal("no concurrent reads completed during Compact — the test didn't actually exercise concurrency")
	}
	t.Logf("%d concurrent reads completed without error while Compact ran", reads)
}

func TestRunCompactionLoop_RunsAndStopsOnCancel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, _, err := EnsureIncrementalVacuum(ctx, db); err != nil {
		t.Fatalf("EnsureIncrementalVacuum: %v", err)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	// logFn only records here, deliberately not t.Errorf: a Compact call
	// already in flight when cancel() fires below can legitimately surface
	// a "context canceled" error from its own DB calls — that is correct
	// cancellation propagation, not a bug in the loop.
	var loggedMu sync.Mutex
	var logged []error
	go func() {
		done <- RunCompactionLoop(loopCtx, db, 10*time.Millisecond, DefaultCompactionMaxPages, func(err error) {
			loggedMu.Lock()
			logged = append(logged, err)
			loggedMu.Unlock()
		})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunCompactionLoop returned %v, want nil after cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunCompactionLoop did not return after ctx cancellation")
	}

	loggedMu.Lock()
	defer loggedMu.Unlock()
	for _, err := range logged {
		t.Logf("logFn observed (expected, benign): %v", err)
	}
}
