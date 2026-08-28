// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// SnapshotFileMeta is one captured file's identity within a snapshot (no
// content — docs/api.md's GET /snapshots/{id} is "metadata + file list").
type SnapshotFileMeta struct {
	Node   string `json:"node"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// SnapshotSummary is one row of GET /snapshots' paginated list.
type SnapshotSummary struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	ChangesetID string   `json:"changesetId,omitempty"`
	Note        string   `json:"note,omitempty"`
	Nodes       []string `json:"nodes"`
	TakenAt     int64    `json:"takenAt"`
}

// SnapshotDetail is GET /snapshots/{id}'s response: the summary plus the
// full captured file list.
type SnapshotDetail struct {
	Files []SnapshotFileMeta `json:"files"`
	SnapshotSummary
}

// SnapshotDiff is GET /snapshots/diff's response: per (node,path) unified
// diffs between two states — mirrors ifaces.ChangesetDiff's Files shape (no
// Ops: a snapshot diff is raw file state, not changeset ops).
type SnapshotDiff struct {
	Files []ifaces.FileDiff `json:"files"`
}

// snapshotLiveToken is the docs/api.md-documented `to=live` sentinel for
// GET /snapshots/diff.
const snapshotLiveToken = "live"

func toSnapshotSummary(row store.Snapshot, files []snapshotFile) SnapshotSummary {
	nodeSet := map[string]bool{}
	var nodes []string
	for _, f := range files {
		if !nodeSet[f.Node] {
			nodeSet[f.Node] = true
			nodes = append(nodes, f.Node)
		}
	}
	return SnapshotSummary{
		ID: row.ID, Kind: row.Kind, ChangesetID: row.ChangesetID.String,
		Note: row.Note.String, TakenAt: row.TakenAt, Nodes: nodes,
	}
}

// clusterNodes returns every node the live inventory snapshot knows about,
// for a manual snapshot's "capture every node" scope. Order is whatever the
// snapshot's All() yields (not significant here — files_json's node list is
// unordered anyway).
func (s *Service) clusterNodes() []string {
	var nodes []string
	for _, e := range s.inventorySnapshot().All() {
		if e.GetRef().Kind == inventory.KindNode {
			nodes = append(nodes, e.GetRef().ID)
		}
	}
	return nodes
}

// ListSnapshots returns one page of snapshots newest-first (docs/api.md:
// "GET /snapshots — list (paginated, cluster-merged)"). See
// store.SnapshotRepo.ListPage for the cursor convention.
func (s *Service) ListSnapshots(ctx context.Context, cursor string, limit int) ([]SnapshotSummary, string, error) {
	rows, next, err := s.snapshots.ListPage(ctx, cursor, limit)
	if err != nil {
		return nil, "", fmt.Errorf("change: listing snapshots: %w", err)
	}
	out := make([]SnapshotSummary, 0, len(rows))
	for _, row := range rows {
		files, err := decodeSnapshotFiles(row)
		if err != nil {
			return nil, "", err
		}
		out = append(out, toSnapshotSummary(row, files))
	}
	return out, next, nil
}

// GetSnapshot returns one snapshot's metadata and file list (docs/api.md:
// "GET /snapshots/{id} — metadata + file list").
func (s *Service) GetSnapshot(ctx context.Context, id string) (SnapshotDetail, error) {
	row, err := s.snapshots.Get(ctx, id)
	if err != nil {
		return SnapshotDetail{}, fmt.Errorf("change: getting snapshot %s: %w", id, err)
	}
	files, err := decodeSnapshotFiles(row)
	if err != nil {
		return SnapshotDetail{}, err
	}
	metas := make([]SnapshotFileMeta, len(files))
	for i, f := range files {
		metas[i] = SnapshotFileMeta{Node: f.Node, Path: f.Path, SHA256: f.SHA256}
	}
	return SnapshotDetail{SnapshotSummary: toSnapshotSummary(row, files), Files: metas}, nil
}

// CreateManualSnapshot captures every known cluster node's current
// interfaces file as a new, unlinked ("manual") snapshot (docs/api.md:
// "POST /snapshots — manual snapshot {note}").
func (s *Service) CreateManualSnapshot(ctx context.Context, author, note string) (SnapshotSummary, error) {
	if !s.applyConfigured() {
		return SnapshotSummary{}, &ErrApplyNotConfigured{}
	}
	nodes := s.clusterNodes()
	files := make([]snapshotFile, 0, len(nodes))
	for _, node := range nodes {
		content, err := s.nodes.ReadInterfaces(ctx, node)
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("change: capturing manual snapshot on node %s: %w", node, err)
		}
		hash, err := s.blobs.Put(ctx, content)
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("change: storing manual snapshot blob for node %s: %w", node, err)
		}
		files = append(files, snapshotFile{Node: node, Path: interfacesPath, SHA256: hash, Content: content})
	}
	id, err := s.persistSnapshot(ctx, "", snapshotKindManual, note, files)
	if err != nil {
		return SnapshotSummary{}, err
	}
	s.appendAudit(ctx, author, "snapshot.create", "success", "", map[string]any{"snapshotId": id, "nodeCount": len(nodes)})
	row, err := s.snapshots.Get(ctx, id)
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("change: reloading manual snapshot %s: %w", id, err)
	}
	return toSnapshotSummary(row, files), nil
}

// nodePath keys a (node,path) pair for DiffSnapshots' file matching.
type nodePath struct{ node, path string }

// DiffSnapshots renders unified diffs between two states (docs/api.md: "GET
// /snapshots/diff?from=&to= — unified diffs between two snapshots (or
// to=live)"). Only files that actually differ are included (matching
// ifaces.UnifiedDiff's "" == identical convention, docs/api.md's diff
// endpoint only lists files the change touches).
func (s *Service) DiffSnapshots(ctx context.Context, from, to string) (*SnapshotDiff, error) {
	fromFiles, err := s.loadSnapshotFiles(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("change: loading snapshot %s for diff: %w", from, err)
	}

	var toFiles []snapshotFile
	if to == snapshotLiveToken {
		for _, f := range fromFiles {
			content, readErr := s.nodes.ReadInterfaces(ctx, f.Node)
			if readErr != nil {
				return nil, fmt.Errorf("change: reading live %s on node %s for diff: %w", f.Path, f.Node, readErr)
			}
			toFiles = append(toFiles, snapshotFile{Node: f.Node, Path: f.Path, Content: content})
		}
	} else {
		toFiles, err = s.loadSnapshotFiles(ctx, to)
		if err != nil {
			return nil, fmt.Errorf("change: loading snapshot %s for diff: %w", to, err)
		}
	}

	fromByKey := make(map[nodePath]string, len(fromFiles))
	var order []nodePath
	for _, f := range fromFiles {
		k := nodePath{f.Node, f.Path}
		fromByKey[k] = f.Content
		order = append(order, k)
	}
	toByKey := make(map[nodePath]string, len(toFiles))
	for _, f := range toFiles {
		k := nodePath{f.Node, f.Path}
		toByKey[k] = f.Content
		if _, ok := fromByKey[k]; !ok {
			order = append(order, k)
		}
	}

	out := &SnapshotDiff{Files: make([]ifaces.FileDiff, 0, len(order))}
	for _, k := range order {
		oldContent, newContent := fromByKey[k], toByKey[k]
		unified := ifaces.UnifiedDiff(k.path, k.path, oldContent, newContent)
		out.Files = append(out.Files, ifaces.FileDiff{
			Node: k.node, Path: k.path, Unified: unified, Changed: unified != "",
		})
	}
	return out, nil
}

// RestoreSnapshot builds a new draft changeset that would restore every node
// the given snapshot captured to that captured state (docs/api.md: "POST
// /snapshots/{id}/restore — creates a changeset draft that would restore
// this state (goes through normal review/apply)"), by diffing the
// snapshot's content against each node's current live file
// (restoreOpsForNode). A snapshot whose captured nodes already match live
// produces a valid, empty-ops draft rather than an error — "nothing to
// restore" is a legitimate outcome for a general snapshot restore (unlike
// rolling back a *specific* committed changeset, where it would signal a
// stale rollback affordance).
func (s *Service) RestoreSnapshot(ctx context.Context, author, id string) (Changeset, error) {
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	files, err := s.loadSnapshotFiles(ctx, id)
	if err != nil {
		return Changeset{}, fmt.Errorf("change: loading snapshot %s to restore: %w", id, err)
	}
	var ops []Op
	for _, f := range files {
		live, readErr := s.nodes.ReadInterfaces(ctx, f.Node)
		if readErr != nil {
			return Changeset{}, fmt.Errorf("change: reading live %s on node %s to restore snapshot %s: %w", f.Path, f.Node, id, readErr)
		}
		nodeOps, opsErr := restoreOpsForNode(f.Node, f.Content, live)
		if opsErr != nil {
			return Changeset{}, opsErr
		}
		ops = append(ops, nodeOps...)
	}
	draft, err := s.Create(ctx, author, fmt.Sprintf("Restore to snapshot %s", id), ops)
	if err != nil {
		return Changeset{}, err
	}
	s.appendAudit(ctx, author, "snapshot.restore", "restoring_draft_created", draft.ID, map[string]any{"snapshotId": id})
	return draft, nil
}
