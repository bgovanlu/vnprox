// snapshots_scheduled.go implements T-2401's scheduled automatic config
// snapshots.
//
// THE GAP THIS CLOSES. Until now a snapshot existed only where vnprox itself
// acted: `pre`/`post` around an apply, or `manual` when someone clicked. So the
// class of change vnprox was LEAST able to undo was the one it did not make —
// an `ssh node && vi /etc/network/interfaces && ifreload -a`. The drift checker
// reports that within a cycle; there was no restore point from before it, which
// is exactly backwards from what an operator needs at that moment.
//
// (The `scheduled` kind has been in docs/data-model.md §2 and in
// apply_snapshot.go's kind constants since T-206. Nothing ever produced one.
// This file is what makes the documented behaviour true.)
//
// THREE PROPERTIES WORTH STATING, because each is the difference between a
// safety net and a disk-filling background job:
//
//   - OFF BY DEFAULT. A capture is a full read of every node's interfaces file.
//     The operator opts in.
//   - DE-DUPLICATED BY CONTENT. If nothing changed since the last scheduled
//     capture, nothing is recorded. An idle cluster accumulates exactly one row,
//     not one per tick forever.
//   - RETENTION NEVER TOUCHES ANOTHER KIND. Pruning is scoped to `scheduled` in
//     the SQL itself (store.PruneKindKeepNewest), so an automatic-capture policy
//     can never delete a changeset's rollback point or a human's manual
//     snapshot.
//
// Restore is unchanged: a scheduled snapshot is an ordinary snapshot, restored
// through POST /snapshots/{id}/restore, which stages an ordinary changeset. No
// new restore path, and nothing here bypasses the change engine.

package change

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// scheduledSnapshotNote is the note recorded on every automatic capture, so a
// reader of GET /snapshots can tell at a glance why it exists.
const scheduledSnapshotNote = "automatic scheduled capture"

// scheduledSnapshotActor is the audit actor for an unattended capture — the
// same convention systemRollbackActor sets for the commit-confirm timer.
const scheduledSnapshotActor = "system"

// CaptureScheduledSnapshot reads every known cluster node's interfaces file and
// records it as a `scheduled` snapshot, UNLESS the result is byte-identical to
// the newest existing scheduled snapshot.
//
// It reports whether a snapshot was actually created. `false, nil` is the
// ordinary, expected outcome on an unchanged cluster and is not an error.
//
// De-duplication compares the full (node, path, sha256) set, not just a single
// file: a cluster whose node set has changed is a different state even if every
// surviving node's file is unchanged, and a snapshot that silently omitted a
// newly-joined node would be a restore point that restores less than it claims.
func (s *Service) CaptureScheduledSnapshot(ctx context.Context) (SnapshotSummary, bool, error) {
	if !s.applyConfigured() {
		return SnapshotSummary{}, false, &ErrApplyNotConfigured{}
	}
	nodes := s.clusterNodes()
	files := make([]snapshotFile, 0, len(nodes))
	for _, node := range nodes {
		content, err := s.nodes.ReadInterfaces(ctx, node)
		if err != nil {
			return SnapshotSummary{}, false, fmt.Errorf("change: capturing scheduled snapshot on node %s: %w", node, err)
		}
		hash, err := s.blobs.Put(ctx, content)
		if err != nil {
			return SnapshotSummary{}, false, fmt.Errorf("change: storing scheduled snapshot blob for node %s: %w", node, err)
		}
		files = append(files, snapshotFile{Node: node, Path: interfacesPath, SHA256: hash, Content: content})
	}

	unchanged, err := s.matchesLatestScheduled(ctx, files)
	if err != nil {
		return SnapshotSummary{}, false, err
	}
	if unchanged {
		return SnapshotSummary{}, false, nil
	}

	id, err := s.persistSnapshot(ctx, "", snapshotKindScheduled, scheduledSnapshotNote, files)
	if err != nil {
		return SnapshotSummary{}, false, err
	}
	s.appendAudit(ctx, scheduledSnapshotActor, "snapshot.scheduled", "success", "",
		map[string]any{"snapshotId": id, "nodeCount": len(nodes)})
	row, err := s.snapshots.Get(ctx, id)
	if err != nil {
		return SnapshotSummary{}, false, fmt.Errorf("change: reloading scheduled snapshot %s: %w", id, err)
	}
	return toSnapshotSummary(row, files), true, nil
}

// matchesLatestScheduled reports whether files is byte-identical (by the
// content hashes already computed) to the newest existing scheduled snapshot.
//
// No previous scheduled snapshot means "not a match": the first capture always
// records.
func (s *Service) matchesLatestScheduled(ctx context.Context, files []snapshotFile) (bool, error) {
	prev, err := s.snapshots.LatestOfKind(ctx, snapshotKindScheduled)
	if err != nil {
		// ErrNotFound (no previous capture) and any other read failure are
		// both handled the same way — by capturing. Skipping a capture
		// because we could not read the previous one would turn a transient
		// store hiccup into a hole in the history.
		return false, nil //nolint:nilerr // see comment: a read failure must not skip the capture.
	}
	prevFiles, err := decodeSnapshotFiles(prev)
	if err != nil {
		return false, nil //nolint:nilerr // a corrupt previous row must not suppress a new capture.
	}
	if len(prevFiles) != len(files) {
		return false, nil
	}
	prevByNode := make(map[nodePath]string, len(prevFiles))
	for _, f := range prevFiles {
		prevByNode[nodePath{f.Node, f.Path}] = f.SHA256
	}
	for _, f := range files {
		if prevByNode[nodePath{f.Node, f.Path}] != f.SHA256 {
			return false, nil
		}
	}
	return true, nil
}

// PruneScheduledSnapshots enforces the count-based retention ceiling for
// automatic captures, keeping the newest `keep`. Scoped to the `scheduled`
// kind in the SQL — see store.PruneKindKeepNewest.
func (s *Service) PruneScheduledSnapshots(ctx context.Context, keep int) (int64, error) {
	n, err := s.snapshots.PruneKindKeepNewest(ctx, snapshotKindScheduled, keep)
	if err != nil {
		return 0, fmt.Errorf("change: pruning scheduled snapshots: %w", err)
	}
	return n, nil
}

// RunSnapshotScheduler drives CaptureScheduledSnapshot on `interval` until ctx
// is cancelled, pruning to `keep` after each capture. Matches cmd/vnproxd's
// runGroup actor signature, like every other RunLoop in this codebase.
//
// An interval of 0 (or less) means the feature is off: this returns
// immediately, having started no ticker, rather than running a loop that does
// nothing. A capture failure is logged and the loop continues — one unreadable
// node must not silently end the entire snapshot history.
func (s *Service) RunSnapshotScheduler(ctx context.Context, interval time.Duration, keep int, log *slog.Logger) error {
	if interval <= 0 {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.scheduledSnapshotTick(ctx, keep, log)
		}
	}
}

// scheduledSnapshotTick is one iteration, factored out so a test can drive it
// directly rather than racing a real ticker.
func (s *Service) scheduledSnapshotTick(ctx context.Context, keep int, log *slog.Logger) {
	summary, created, err := s.CaptureScheduledSnapshot(ctx)
	if err != nil {
		log.Warn("change: scheduled snapshot capture failed", "error", err)
		return
	}
	if !created {
		log.Debug("change: scheduled snapshot skipped, cluster config unchanged")
		return
	}
	log.Info("change: scheduled snapshot captured", "snapshot_id", summary.ID, "nodes", len(summary.Nodes))
	if pruned, pruneErr := s.PruneScheduledSnapshots(ctx, keep); pruneErr != nil {
		log.Warn("change: pruning scheduled snapshots", "error", pruneErr)
	} else if pruned > 0 {
		log.Info("change: pruned scheduled snapshots", "count", pruned)
	}
}
