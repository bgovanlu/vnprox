// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultSnapshotRetentionDays and DefaultSnapshotPinDays are T-206's
// documented snapshot-retention policy (planning/tasks/phase-2.md T-206:
// "retention job (config: keep N days, default 90, committed-changeset
// snapshots pinned 7d minimum per spec)" — the 7d minimum matches
// docs/features/change-management.md §4's "manual rollback of a committed
// changeset is offered for 7 days").
const (
	DefaultSnapshotRetentionDays = 90
	DefaultSnapshotPinDays       = 7
)

// SnapshotRetention runs one retention pass: delete snapshot rows (and their
// snapshot_files) older than keepDays, honoring the pinDays floor for
// committed-changeset snapshots AND — regardless of keepDays/pinDays, T-1905
// AC2 — never touching a snapshot linked to a changeset still `applying` or
// `awaiting_confirm` (SnapshotRepo.Prune's own doc comment has the full
// guardrail rationale), then reclaim any blob storage no longer referenced
// by a surviving snapshot (BlobRepo.PruneOrphans). It returns the number of
// snapshot rows and blob rows deleted.
func SnapshotRetention(ctx context.Context, snapshots *SnapshotRepo, blobs *BlobRepo, now time.Time, keepDays, pinDays int) (snapshotsDeleted, blobsDeleted int64, err error) {
	if keepDays <= 0 {
		keepDays = DefaultSnapshotRetentionDays
	}
	if pinDays <= 0 {
		pinDays = DefaultSnapshotPinDays
	}
	cutoff := now.AddDate(0, 0, -keepDays).Unix()
	pinCutoff := now.AddDate(0, 0, -pinDays).Unix()

	snapshotsDeleted, err = snapshots.Prune(ctx, cutoff, pinCutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("store: snapshot retention: %w", err)
	}
	blobsDeleted, err = blobs.PruneOrphans(ctx)
	if err != nil {
		return snapshotsDeleted, 0, fmt.Errorf("store: snapshot retention: pruning orphaned blobs: %w", err)
	}
	return snapshotsDeleted, blobsDeleted, nil
}

// RunSnapshotRetentionLoop runs SnapshotRetention every interval until ctx is
// cancelled, logging failures via logFn (nil discards them) rather than
// stopping the loop — mirrors MetricSampleRepo.RunPruneLoop's contract
// (func(ctx context.Context) error, suitable for cmd/vnproxd's runGroup).
func RunSnapshotRetentionLoop(ctx context.Context, snapshots *SnapshotRepo, blobs *BlobRepo, interval time.Duration, keepDays, pinDays int, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, _, err := SnapshotRetention(ctx, snapshots, blobs, now, keepDays, pinDays); err != nil && logFn != nil &&
				!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logFn(err)
			}
		}
	}
}
