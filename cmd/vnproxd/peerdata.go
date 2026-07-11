package main

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// auditPeerAdapter and snapshotPeerAdapter are T-303's wiring-layer bridges
// between this node's own store-backed audit/snapshot data and
// internal/peer's AuditReader/SnapshotReader seams (GET /api/peer/audit,
// GET /api/peer/snapshots) — the peer-server-side half of docs/
// architecture.md §7's "Audit/snapshot queries in the UI fan out to peers
// and merge". internal/peer deliberately doesn't import internal/store or
// internal/change (see peer.AuditReader/SnapshotReader's doc comments), so
// this small conversion lives here, the same wiring layer collect.go's
// collectorHealthAdapter already does the analogous job in.

// auditPeerAdapter adapts *store.AuditRepo to peer.AuditReader.
type auditPeerAdapter struct {
	repo *store.AuditRepo
}

func (a auditPeerAdapter) ListAuditPage(ctx context.Context, filter peer.AuditFilter, cursor string, limit int) ([]peer.AuditRecord, string, error) {
	entries, next, err := a.repo.ListPage(ctx, store.AuditFilter{
		User: filter.User, Action: filter.Action, Target: filter.Target, Result: filter.Result,
		ChangesetID: filter.ChangesetID, From: filter.From, To: filter.To,
	}, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]peer.AuditRecord, len(entries))
	for i, e := range entries {
		out[i] = peer.AuditRecord{
			ID: e.ID, At: e.At, Username: e.Username, Action: e.Action,
			Target: e.Target.String, ChangesetID: e.ChangesetID.String, Result: e.Result,
		}
		if e.DetailJSON.Valid {
			out[i].Detail = []byte(e.DetailJSON.String)
		}
	}
	return out, next, nil
}

// snapshotPeerAdapter adapts *change.Service to peer.SnapshotReader.
type snapshotPeerAdapter struct {
	svc *change.Service
}

func (a snapshotPeerAdapter) ListSnapshotPage(ctx context.Context, cursor string, limit int) ([]peer.SnapshotRecord, string, error) {
	items, next, err := a.svc.ListSnapshots(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]peer.SnapshotRecord, len(items))
	for i, it := range items {
		out[i] = peer.SnapshotRecord{
			ID: it.ID, Kind: it.Kind, ChangesetID: it.ChangesetID, Note: it.Note,
			Nodes: it.Nodes, TakenAt: it.TakenAt,
		}
	}
	return out, next, nil
}
