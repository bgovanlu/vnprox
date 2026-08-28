// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentWriters is acceptance criterion 4: 10 goroutines x 100
// inserts against the same database must all succeed with no SQLITE_BUSY
// error surfacing to callers. WAL mode plus the busy_timeout pragma (set on
// every pooled connection via the DSN, see store.go) means concurrent
// writers block-and-retry inside SQLite instead of failing immediately.
//
// Run with -race (as the task requires) to also catch any data races in the
// repository/cipher code itself.
func TestConcurrentWriters(t *testing.T) {
	db := openTestDB(t)
	repo := NewMetricSampleRepo(db)
	ctx := context.Background()

	const goroutines = 10
	const perGoroutine = 100

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				s := MetricSample{
					Ref: fmt.Sprintf("worker-%d", g),
					At:  int64(i),
				}
				if err := repo.Insert(ctx, s); err != nil {
					errs <- fmt.Errorf("goroutine %d insert %d: %w", g, i, err)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		t.Fatalf("%d/%d inserts failed (first: %v)", len(failures), goroutines*perGoroutine, failures[0])
	}

	for g := 0; g < goroutines; g++ {
		list, err := repo.List(ctx, fmt.Sprintf("worker-%d", g), 0, perGoroutine)
		if err != nil {
			t.Fatalf("List(worker-%d): %v", g, err)
		}
		if len(list) != perGoroutine {
			t.Errorf("worker-%d: got %d rows, want %d", g, len(list), perGoroutine)
		}
	}
}

// TestConcurrentWriters_MixedTables exercises concurrent writers across
// several different tables at once (sessions, changesets, audit_log), which
// is closer to real daemon usage than a single hot table.
func TestConcurrentWriters_MixedTables(t *testing.T) {
	db := openTestDB(t)
	sessions := NewSessionRepo(db, testCipher(t))
	changesets := NewChangesetRepo(db)
	audit := NewAuditRepo(db)
	ctx := context.Background()

	const goroutines = 10
	const perGoroutine = 100

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine*3)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				sid := fmt.Sprintf("sess-%d-%d", g, i)
				if err := sessions.Insert(ctx, Session{
					ID: sid, Username: "u", Realm: "pam",
					PVETicket: "t", CSRFToken: "c", CapsJSON: "{}",
					CreatedAt: int64(i), ExpiresAt: int64(i + 1),
				}); err != nil {
					errs <- fmt.Errorf("session insert: %w", err)
				}

				if err := changesets.Insert(ctx, Changeset{
					ID: NewULID(), Author: "u", Status: "draft", OpsJSON: "[]",
					CreatedAt: int64(i), UpdatedAt: int64(i),
				}); err != nil {
					errs <- fmt.Errorf("changeset insert: %w", err)
				}

				if _, err := audit.Append(ctx, AuditEntry{
					At: int64(i), Username: "u", Action: "test", Result: "ok",
				}); err != nil {
					errs <- fmt.Errorf("audit append: %w", err)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		t.Fatalf("%d writes failed (first: %v)", len(failures), failures[0])
	}
}
