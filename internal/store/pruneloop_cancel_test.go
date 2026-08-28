// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPruneLoops_CancellationIsNotAPruneError covers a defect class found by
// the Arc 5 wave-1 merge gate: a background prune loop whose tick is still
// in flight when the daemon shuts down reported the resulting
// "context canceled" to its log callback as though the prune had FAILED.
//
// The loops themselves always returned nil on ctx.Done() — that part was
// right. The bug is one level in: PruneRetention inherits the same ctx, so
// cancelling mid-query returns context.Canceled, and six of the eight loops
// passed it straight to logFn. On a real daemon that means every shutdown
// racing a prune tick logs a spurious error, which is indistinguishable
// from a prune that genuinely could not run.
//
// It first surfaced as a flake (internal/store's TestAuditRepo_RunPruneLoop
// under full-parallel `make check` load) precisely because it is a race.
// Two of the eight loops — findingevents.go and posturescores.go — already
// had the guard, so this test also pins the convention rather than leaving
// it to whoever writes the ninth loop.
func TestPruneLoops_CancellationIsNotAPruneError(t *testing.T) {
	t.Parallel()

	// The race is won deterministically rather than chased: the context is
	// cancelled BEFORE the loop starts and the tick interval is a
	// nanosecond, so by the time the loop reaches its select both cases are
	// ready. Go picks a ready case pseudo-randomly, so across enough
	// attempts the ticker branch is taken with an already-cancelled
	// context, PruneRetention returns context.Canceled immediately, and an
	// unguarded loop hands it to logFn. Racing a real in-flight query
	// instead is what made the original failure a once-per-many-runs flake,
	// and a test that reproduces a bug only occasionally is not a test.
	const attempts = 40

	loops := []struct {
		start func(ctx context.Context, t *testing.T, logFn func(error)) error
		name  string
	}{
		{
			name: "audit_log",
			start: func(ctx context.Context, t *testing.T, logFn func(error)) error {
				db := openTestDB(t)
				repo := NewAuditRepo(db)
				old := time.Now().Add(-800 * 24 * time.Hour).Unix()
				for range 5 {
					if _, err := repo.Append(context.Background(), AuditEntry{
						At: old, Username: "root@pam", Action: "changeset.apply", Result: "success",
					}); err != nil {
						t.Fatalf("seed: %v", err)
					}
				}
				return repo.RunPruneLoop(ctx, time.Nanosecond, DefaultAuditRetentionDays, logFn)
			},
		},
		{
			name: "metric_samples",
			start: func(ctx context.Context, t *testing.T, logFn func(error)) error {
				db := openTestDB(t)
				repo := NewMetricSampleRepo(db)
				return repo.RunPruneLoop(ctx, time.Nanosecond, logFn)
			},
		},
		{
			name: "compaction",
			start: func(ctx context.Context, t *testing.T, logFn func(error)) error {
				db := openTestDB(t)
				return RunCompactionLoop(ctx, db, time.Nanosecond, 0, logFn)
			},
		},
	}

	for _, lp := range loops {
		t.Run(lp.name, func(t *testing.T) {
			t.Parallel()
			for i := range attempts {
				var mu sync.Mutex
				var reported []error

				ctx, cancel := context.WithCancel(context.Background())
				cancel() // already done before the loop's first select

				done := make(chan error, 1)
				go func() {
					done <- lp.start(ctx, t, func(err error) {
						mu.Lock()
						reported = append(reported, err)
						mu.Unlock()
					})
				}()

				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("attempt %d: loop returned %v, want nil on cancellation", i, err)
					}
				case <-time.After(10 * time.Second):
					t.Fatalf("attempt %d: loop did not return after cancellation", i)
				}

				mu.Lock()
				got := append([]error(nil), reported...)
				mu.Unlock()

				for _, err := range got {
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
						strings.Contains(err.Error(), "context canceled") {
						t.Fatalf("attempt %d: shutdown reported a cancellation as a prune failure: %v", i, err)
					}
				}
			}
		})
	}
}
