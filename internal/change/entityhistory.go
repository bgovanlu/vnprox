// SPDX-License-Identifier: Apache-2.0

// entityhistory.go implements T-2403's entity change history ("blame").
//
// THE GAP THIS CLOSES. Standing on any entity in the inspector, there was no
// way to ask "what has been done to this, and by whom." The data existed —
// every op carries its target and every changeset carries its author — but it
// was only reachable by reading the whole audit list and filtering by eye.
//
// The history merges three sources that each know a different half of the
// story, and merging them is the point: a changeset says WHAT was intended and
// by whom, an audit row says WHEN something actually happened and whether it
// succeeded, and a snapshot says WHERE a restore point sits relative to both.
// Any one of them alone leaves an operator reconstructing the other two.
//
// It is a read over data the store already holds. Nothing here writes, and
// nothing here is authoritative about network state — Proxmox remains the
// source of truth (CLAUDE.md); this is vnprox's record of its own actions.

package change

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// HistoryKind names which of the three sources an entry came from.
type HistoryKind string

const (
	HistoryKindChangeset HistoryKind = "changeset"
	HistoryKindAudit     HistoryKind = "audit"
	HistoryKindSnapshot  HistoryKind = "snapshot"
)

// EntityHistoryEntry is one row of an entity's history, newest first.
type EntityHistoryEntry struct {
	Kind        HistoryKind `json:"kind"`
	Actor       string      `json:"actor,omitempty"`
	Summary     string      `json:"summary"`
	ChangesetID string      `json:"changesetId,omitempty"`
	SnapshotID  string      `json:"snapshotId,omitempty"`
	OpID        string      `json:"opId,omitempty"`
	Result      string      `json:"result,omitempty"`
	At          int64       `json:"at"`
}

// DefaultEntityHistoryLimit bounds one page.
const DefaultEntityHistoryLimit = 50

// maxEntityHistoryLimit is the ceiling a caller can ask for.
const maxEntityHistoryLimit = 200

// changesetHistoryScanLimit bounds how many changesets are scanned for ops
// targeting the ref.
//
// There is no index on "op target" — ops live in a JSON blob — so this is a
// scan. It is bounded rather than unbounded, and when the bound truncates the
// scan the caller is TOLD (EntityHistory's `truncated` return), because a
// silently short history is indistinguishable from a genuinely short one, and
// an operator reading "nothing has touched this bridge" needs that to be true.
const changesetHistoryScanLimit = 500

// EntityHistory returns the merged, newest-first history of one entity.
//
// truncated reports whether the changeset scan hit its bound — see
// changesetHistoryScanLimit for why that is surfaced rather than swallowed.
func (s *Service) EntityHistory(ctx context.Context, ref inventory.Ref, limit int) (entries []EntityHistoryEntry, truncated bool, err error) {
	if ref.IsZero() {
		return nil, false, fmt.Errorf("change: entity history requires a ref")
	}
	if limit <= 0 {
		limit = DefaultEntityHistoryLimit
	}
	if limit > maxEntityHistoryLimit {
		limit = maxEntityHistoryLimit
	}

	out := []EntityHistoryEntry{}

	csEntries, csTruncated, err := s.changesetHistory(ctx, ref)
	if err != nil {
		return nil, false, err
	}
	out = append(out, csEntries...)

	auditEntries, err := s.auditHistory(ctx, ref, limit)
	if err != nil {
		return nil, false, err
	}
	out = append(out, auditEntries...)

	snapEntries, err := s.snapshotHistory(ctx, ref, limit)
	if err != nil {
		return nil, false, err
	}
	out = append(out, snapEntries...)

	// Newest first, with a deterministic tiebreak so two entries recorded in
	// the same second do not shuffle between reads.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At > out[j].At
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return historyEntryKey(out[i]) < historyEntryKey(out[j])
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, csTruncated, nil
}

func historyEntryKey(e EntityHistoryEntry) string {
	return e.ChangesetID + "|" + e.SnapshotID + "|" + e.OpID + "|" + e.Summary
}

// changesetHistory finds every changeset carrying an op that targets ref.
func (s *Service) changesetHistory(ctx context.Context, ref inventory.Ref) ([]EntityHistoryEntry, bool, error) {
	rows, err := s.repo.List(ctx, "")
	if err != nil {
		return nil, false, fmt.Errorf("change: listing changesets for entity history: %w", err)
	}
	truncated := false
	if len(rows) > changesetHistoryScanLimit {
		rows = rows[:changesetHistoryScanLimit]
		truncated = true
	}

	out := []EntityHistoryEntry{}
	for _, row := range rows {
		cs, convErr := fromStoreRow(row)
		if convErr != nil {
			// A single undecodable changeset must not blank the whole
			// history; skip it and keep the rest.
			continue
		}
		for _, op := range cs.Ops {
			if op.Target != ref {
				continue
			}
			out = append(out, EntityHistoryEntry{
				Kind:        HistoryKindChangeset,
				At:          cs.UpdatedAt,
				Actor:       cs.Author,
				Summary:     string(op.Type) + " in " + changesetLabel(cs),
				ChangesetID: cs.ID,
				OpID:        op.ID,
				Result:      string(cs.Status),
			})
		}
	}
	return out, truncated, nil
}

func changesetLabel(cs Changeset) string {
	if strings.TrimSpace(cs.Title) != "" {
		return cs.Title
	}
	return cs.ID
}

// auditHistory finds audit rows whose target is this ref.
func (s *Service) auditHistory(ctx context.Context, ref inventory.Ref, limit int) ([]EntityHistoryEntry, error) {
	if s.audit == nil {
		return []EntityHistoryEntry{}, nil
	}
	rows, _, err := s.audit.ListPage(ctx, store.AuditFilter{Target: ref.String()}, "", limit)
	if err != nil {
		return nil, fmt.Errorf("change: listing audit rows for entity history: %w", err)
	}
	out := make([]EntityHistoryEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, EntityHistoryEntry{
			Kind:        HistoryKindAudit,
			At:          r.At,
			Actor:       r.Username,
			Summary:     r.Action,
			ChangesetID: r.ChangesetID.String,
			Result:      r.Result,
		})
	}
	return out, nil
}

// snapshotHistory finds snapshots that captured this ref's node — the restore
// points available for it.
//
// Scoped by NODE rather than by entity, deliberately: a snapshot captures a
// whole interfaces file, so every entity on that node has a restore point in
// it. Claiming otherwise would understate what is recoverable.
func (s *Service) snapshotHistory(ctx context.Context, ref inventory.Ref, limit int) ([]EntityHistoryEntry, error) {
	if s.snapshots == nil || ref.Node == "" {
		return []EntityHistoryEntry{}, nil
	}
	rows, _, err := s.snapshots.ListPage(ctx, "", limit)
	if err != nil {
		return nil, fmt.Errorf("change: listing snapshots for entity history: %w", err)
	}
	out := []EntityHistoryEntry{}
	for _, row := range rows {
		files, decErr := decodeSnapshotFiles(row)
		if decErr != nil {
			continue
		}
		covers := false
		for _, f := range files {
			if f.Node == ref.Node {
				covers = true
				break
			}
		}
		if !covers {
			continue
		}
		out = append(out, EntityHistoryEntry{
			Kind:        HistoryKindSnapshot,
			At:          row.TakenAt,
			Summary:     row.Kind + " snapshot of " + ref.Node,
			SnapshotID:  row.ID,
			ChangesetID: row.ChangesetID.String,
		})
	}
	return out, nil
}
