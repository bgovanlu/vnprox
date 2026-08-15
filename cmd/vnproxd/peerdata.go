package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

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
			ID: e.ID, At: e.At, Username: e.Username, Action: e.Action, IP: e.IP,
			Target: e.Target.String, ChangesetID: e.ChangesetID.String, Result: e.Result,
		}
		if e.DetailJSON.Valid {
			out[i].Detail = []byte(e.DetailJSON.String)
		}
	}
	return out, next, nil
}

// flowPeerAdapter adapts *store.FlowSampleRepo to peer.FlowReader (T-1002,
// GET /api/peer/flows) — the same wiring-layer bridge shape as
// auditPeerAdapter above, converting internal/store's flow.FlowFilter/
// FlowSample to internal/peer's own duplicate wire types (peer.FlowFilter/
// FlowRecord never import internal/store — see peer.FlowReader's doc
// comment).
type flowPeerAdapter struct {
	repo *store.FlowSampleRepo
}

func (a flowPeerAdapter) ListFlowPage(ctx context.Context, filter peer.FlowFilter, cursor string, limit int) ([]peer.FlowRecord, string, error) {
	samples, next, err := a.repo.Query(ctx, store.FlowFilter{
		Guest: filter.Guest, Subnet: filter.Subnet, Source: filter.Source,
		VLAN: filter.VLAN, Port: filter.Port, Proto: filter.Proto, FromTs: filter.FromTs, ToTs: filter.ToTs,
	}, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]peer.FlowRecord, len(samples))
	for i, s := range samples {
		out[i] = peer.FlowRecord{
			ID: s.ID, At: s.At, Node: s.Node, SrcIP: s.SrcIP, DstIP: s.DstIP,
			SrcRef: s.SrcRef, DstRef: s.DstRef, Source: s.Source,
			Bytes: s.Bytes, Packets: s.Packets, SrcPort: s.SrcPort, DstPort: s.DstPort,
			Proto: s.Proto, VLAN: s.VLAN, IngressIfIndex: s.IngressIf, EgressIfIndex: s.EgressIf,
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

// hostWriteGuardAdapter (T-2902) adapts the receiving-side safety seams —
// change.Service.ValidatePeerHostWrite (the exact validator pipeline a
// local changeset runs, protected-path interlocks included) and
// store.SnapshotRepo.HasFileHash (the provenance check that keeps
// distributed rollback working) — to peer.HostWriteGuard.
type hostWriteGuardAdapter struct {
	change    *change.Service
	snapshots *store.SnapshotRepo
	logger    *slog.Logger
}

func (g hostWriteGuardAdapter) ValidateStagedContent(ctx context.Context, node, content string) []string {
	return g.change.ValidatePeerHostWrite(ctx, node, content)
}

func (g hostWriteGuardAdapter) KnownContent(ctx context.Context, node, content string) bool {
	sum := sha256.Sum256([]byte(content))
	ok, err := g.snapshots.HasFileHash(ctx, node, hex.EncodeToString(sum[:]))
	if err != nil {
		// Soft-fail to "not known": the content then goes through full
		// validation instead of the provenance exemption — the safe
		// direction (a transient store error must never widen what an
		// unvalidated restore may write).
		g.logger.Warn("peer: snapshot provenance check failed; treating restore content as unknown", "node", node, "error", err)
		return false
	}
	return ok
}

// hostWriteAuditAdapter (T-2902) appends the receiving-side audit row for
// every peer host write to this node's own audit_log — origin attribution
// in detail_json, the coordinator-attributed client IP in the first-class
// ip column (0047).
type hostWriteAuditAdapter struct {
	repo   *store.AuditRepo
	logger *slog.Logger
}

func (a hostWriteAuditAdapter) AppendHostWrite(ctx context.Context, e peer.HostWriteAudit) {
	detail := map[string]any{}
	if e.OriginNode != "" {
		detail["originNode"] = e.OriginNode
	}
	if e.ContentSHA256 != "" {
		detail["contentSha256"] = e.ContentSHA256
	}
	if e.Provenance != "" {
		detail["provenance"] = e.Provenance
	}
	if e.Detail != "" {
		detail["detail"] = e.Detail
	}
	var detailJSON sql.NullString
	if len(detail) > 0 {
		if b, err := json.Marshal(detail); err == nil {
			detailJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	entry := store.AuditEntry{
		At:         time.Now().Unix(),
		Username:   e.Actor,
		Action:     e.Action,
		Result:     e.Result,
		IP:         e.OriginIP,
		Target:     sql.NullString{String: e.Node, Valid: e.Node != ""},
		DetailJSON: detailJSON,
	}
	if _, err := a.repo.Append(ctx, entry); err != nil {
		// Log-and-continue by contract (peer.HostWriteAuditor): the write
		// path never blocks on audit storage.
		a.logger.Error("peer: appending host-write audit entry", "action", e.Action, "error", err)
	}
}
