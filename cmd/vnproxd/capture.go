// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/capturemock"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// setupCapture builds T-1301's packet-capture coordinator: the local capture
// Agent, the peer client for multi-point fan-out, the capture_sessions store,
// the audit seam, and the configured server-enforced cap ceilings. It returns
// the coordinator (wired into api.Options.Captures and, via
// capturePeerAdapter, peer.ServerOptions.Capture) and the retention-sweep
// actor for the run group.
//
// The local Agent is internal/capturemock's scripted agent: there is no live
// Proxmox host to capture packets from in this environment, and CLAUDE.md's
// stdlib-first rule bars adding a libpcap/AF_PACKET binding here. The
// production, on-hardware agent (a real tcpdump/AF_PACKET capture) is a
// needs-hardware-validation item and drops in at exactly this line — every
// other layer (caps, filter validation, audit, retention, peer fan-out) is
// real and agent-agnostic. See planning/reports/needs-hardware-validation.md.
func setupCapture(cfg *config.Config, db *store.DB, auditRepo *store.AuditRepo, peerClient *peer.Client, localNode func() string, logger *slog.Logger) *capture.Coordinator {
	var remote capture.RemoteCapturer
	if peerClient != nil {
		remote = captureRemoteAdapter{client: peerClient}
	}
	return capture.New(capture.Config{
		Ceilings: capture.Caps{
			MaxDurationSec: cfg.Capture.MaxDurationSec,
			MaxBytes:       cfg.Capture.MaxBytes,
			MaxPackets:     cfg.Capture.MaxPackets,
			RetentionHours: cfg.Capture.RetentionHours,
		},
		Root:                  cfg.Capture.Root,
		MaxFilterInstructions: cfg.Capture.MaxFilterInstructions,
		Agent:                 capturemock.NewAgent(),
		Remote:                remote,
		Resolver:              capture.RefResolver{},
		Store:                 captureStoreAdapter{repo: store.NewCaptureRepo(db)},
		Audit:                 captureAuditAdapter{repo: auditRepo},
		LocalNode:             localNode,
		Logger:                logger,
	})
}

// captureStoreAdapter bridges the concrete *store.CaptureRepo (which speaks
// store.CaptureSession, keeping internal/store free of an internal/capture
// import) to internal/capture.SessionStore (which speaks capture.Session).
type captureStoreAdapter struct{ repo *store.CaptureRepo }

func (a captureStoreAdapter) Upsert(ctx context.Context, s capture.Session) error {
	return a.repo.Upsert(ctx, toStoreCapture(s))
}

func (a captureStoreAdapter) Get(ctx context.Context, id string) (capture.Session, error) {
	s, err := a.repo.Get(ctx, id)
	if err != nil {
		return capture.Session{}, err
	}
	return fromStoreCapture(s), nil
}

func (a captureStoreAdapter) ByGroup(ctx context.Context, groupID string) ([]capture.Session, error) {
	rows, err := a.repo.ByGroup(ctx, groupID)
	return fromStoreCaptures(rows), err
}

func (a captureStoreAdapter) List(ctx context.Context) ([]capture.Session, error) {
	rows, err := a.repo.List(ctx)
	return fromStoreCaptures(rows), err
}

func (a captureStoreAdapter) ListGroups(ctx context.Context) ([]string, error) {
	return a.repo.ListGroups(ctx)
}

func (a captureStoreAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func toStoreCapture(s capture.Session) store.CaptureSession {
	return store.CaptureSession{
		ID: s.ID, GroupID: s.GroupID, TargetRef: s.TargetRef, Node: s.Node, Nodes: s.Nodes,
		Filter: s.Filter, Caps: store.CaptureCaps(s.Caps), Status: string(s.Status),
		StartedBy: s.StartedBy, StartedAt: s.StartedAt, StoppedAt: s.StoppedAt,
		FilePath: s.FilePath, FileBytes: s.FileBytes, Packets: s.Packets,
	}
}

func fromStoreCapture(s store.CaptureSession) capture.Session {
	return capture.Session{
		ID: s.ID, GroupID: s.GroupID, TargetRef: s.TargetRef, Node: s.Node, Nodes: s.Nodes,
		Filter: s.Filter, Caps: capture.Caps(s.Caps), Status: capture.Status(s.Status),
		StartedBy: s.StartedBy, StartedAt: s.StartedAt, StoppedAt: s.StoppedAt,
		FilePath: s.FilePath, FileBytes: s.FileBytes, Packets: s.Packets,
	}
}

func fromStoreCaptures(rows []store.CaptureSession) []capture.Session {
	out := make([]capture.Session, len(rows))
	for i, s := range rows {
		out[i] = fromStoreCapture(s)
	}
	return out
}

// captureAuditAdapter bridges *store.AuditRepo to internal/capture.Auditor —
// the capture.start / capture.stop audit rows (docs/api.md's Audit section).
type captureAuditAdapter struct{ repo *store.AuditRepo }

func (a captureAuditAdapter) AppendCapture(ctx context.Context, e capture.AuditEvent) error {
	if a.repo == nil {
		return nil
	}
	entry := store.AuditEntry{At: e.At, Username: e.Actor, Action: e.Action, Result: e.Result}
	if e.TargetRef != "" {
		entry.Target.String, entry.Target.Valid = e.TargetRef, true
	}
	if len(e.Detail) > 0 {
		if b, err := json.Marshal(e.Detail); err == nil {
			entry.DetailJSON.String, entry.DetailJSON.Valid = string(b), true
		}
	}
	_, err := a.repo.Append(ctx, entry)
	return err
}

// captureRemoteAdapter satisfies internal/capture.RemoteCapturer over a
// *peer.Client: it resolves a node name to its discovered Peer and issues the
// HMAC-gated /api/peer/capture/* call, translating peer's own wire types back
// to internal/capture's.
type captureRemoteAdapter struct{ client *peer.Client }

func (a captureRemoteAdapter) peerFor(ctx context.Context, node string) (peer.Peer, error) {
	peers, err := a.client.Peers(ctx)
	if err != nil {
		return peer.Peer{}, fmt.Errorf("capture: discovering peer %q: %w", node, err)
	}
	for _, p := range peers {
		if p.Node == node {
			return p, nil
		}
	}
	return peer.Peer{}, fmt.Errorf("capture: no reachable peer named %q", node)
}

func (a captureRemoteAdapter) Start(ctx context.Context, node string, spec capture.Spec) (capture.Result, error) {
	p, err := a.peerFor(ctx, node)
	if err != nil {
		return capture.Result{Status: capture.StatusError}, err
	}
	res, err := a.client.CaptureStart(ctx, p, toPeerSpec(spec))
	if err != nil {
		return capture.Result{Status: capture.StatusError}, err
	}
	return fromPeerResult(res), nil
}

func (a captureRemoteAdapter) Stop(ctx context.Context, node, sessionID string) (capture.Result, error) {
	p, err := a.peerFor(ctx, node)
	if err != nil {
		return capture.Result{}, err
	}
	res, err := a.client.CaptureStop(ctx, p, sessionID)
	if err != nil {
		return capture.Result{}, err
	}
	return fromPeerResult(res), nil
}

func (a captureRemoteAdapter) Status(ctx context.Context, node, sessionID string) (capture.Result, error) {
	p, err := a.peerFor(ctx, node)
	if err != nil {
		return capture.Result{}, err
	}
	res, err := a.client.CaptureStatus(ctx, p, sessionID)
	if err != nil {
		return capture.Result{}, err
	}
	return fromPeerResult(res), nil
}

// Download fetches sessionID's raw pcap bytes from the peer node that
// captured it (T-1302) — the cluster-aware half of per-session download: a
// session whose Node isn't this daemon's own is never locally readable, so
// the coordinator always reaches it through here.
func (a captureRemoteAdapter) Download(ctx context.Context, node, sessionID string) ([]byte, error) {
	p, err := a.peerFor(ctx, node)
	if err != nil {
		return nil, err
	}
	return a.client.CaptureDownload(ctx, p, sessionID)
}

// capturePeerAdapter satisfies peer.CaptureAgent by delegating to the local
// coordinator's node-local-only entry points (StartLocalSpec/StopLocal/
// StatusLocal) — the peer-server side of a coordinating daemon's fan-out.
type capturePeerAdapter struct{ coord *capture.Coordinator }

func (a capturePeerAdapter) StartLocal(ctx context.Context, spec peer.CaptureSpec) (peer.CaptureResult, error) {
	res, err := a.coord.StartLocalSpec(ctx, fromPeerSpec(spec))
	if err != nil {
		return peer.CaptureResult{}, err
	}
	return toPeerResult(res), nil
}

func (a capturePeerAdapter) StopLocal(ctx context.Context, sessionID string) (peer.CaptureResult, error) {
	res, err := a.coord.StopLocal(ctx, sessionID)
	if err != nil {
		return peer.CaptureResult{}, err
	}
	return toPeerResult(res), nil
}

func (a capturePeerAdapter) StatusLocal(ctx context.Context, sessionID string) (peer.CaptureResult, error) {
	res, err := a.coord.StatusLocal(ctx, sessionID)
	if err != nil {
		return peer.CaptureResult{}, err
	}
	return toPeerResult(res), nil
}

// DownloadLocal returns sessionID's raw pcap bytes (T-1302) — a peer
// download request always names a session this node itself captured (a
// StartLocalSpec-received session's Node is always re-derived to this
// node's own LocalNode(), never trusted from the caller), so
// Coordinator.Download's local-file branch is exactly what runs here; it
// never re-triggers a further remote hop.
func (a capturePeerAdapter) DownloadLocal(ctx context.Context, sessionID string) ([]byte, error) {
	data, _, err := a.coord.Download(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func toPeerSpec(s capture.Spec) peer.CaptureSpec {
	// FilePath is intentionally not sent: the receiving node derives it from
	// its own [capture] root (StartLocalSpec), so it never travels the wire.
	return peer.CaptureSpec{
		SessionID: s.SessionID, GroupID: s.GroupID, TargetRef: s.TargetRef, Node: s.Node,
		Iface: s.Iface, Filter: s.Filter, Caps: peer.CaptureCaps(s.Caps),
		StartedBy: s.StartedBy, StartedAt: s.StartedAt, Nodes: s.Nodes,
	}
}

func fromPeerSpec(s peer.CaptureSpec) capture.Spec {
	// FilePath and Node are (re-)derived locally by StartLocalSpec, never
	// taken from the caller — see that method's doc comment.
	return capture.Spec{
		SessionID: s.SessionID, GroupID: s.GroupID, TargetRef: s.TargetRef, Node: s.Node,
		Iface: s.Iface, Filter: s.Filter, Caps: capture.Caps(s.Caps),
		StartedBy: s.StartedBy, StartedAt: s.StartedAt, Nodes: s.Nodes,
	}
}

func toPeerResult(r capture.Result) peer.CaptureResult {
	return peer.CaptureResult{Status: string(r.Status), Packets: r.Packets, Bytes: r.Bytes}
}

func fromPeerResult(r peer.CaptureResult) capture.Result {
	return capture.Result{Status: capture.Status(r.Status), Packets: r.Packets, Bytes: r.Bytes}
}

// captureSweepInterval is how often the capture retention sweep runs.
const captureSweepInterval = capture.DefaultSweepInterval
