// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// This file is T-303's HTTP-level coverage for the audit/snapshot cluster
// fan-out: GET /audit and GET /snapshots exercised through the full router
// (auth middleware, query parsing, JSON envelope) with a fake local service
// plus a fake PeerAuditSource/PeerSnapshotSource standing in for real
// peers — no real HTTP peer round trip, since internal/peer's own
// client/server wire-level correctness is already covered by that
// package's tests (TestPeerAudit_FetchesFilteredPage etc.).

// fakeAuditListService is a minimal in-memory AuditService.
type fakeAuditListService struct {
	entries []store.AuditEntry
}

// ListPage is a simple non-paginating stand-in: this file's fixtures are
// always small enough to fit one page, so cursor handling only needs to
// support "" (start from the top) — never called with a non-empty cursor
// here.
func (f *fakeAuditListService) ListPage(_ context.Context, _ store.AuditFilter, _ string, limit int) ([]store.AuditEntry, string, error) {
	end := limit
	if end > len(f.entries) {
		end = len(f.entries)
	}
	return f.entries[:end], "", nil
}

// fakePeerAuditSource is a minimal PeerAuditSource: peers is served
// verbatim by Peers; perPeer holds each peer's full (small, single-page)
// item set, and failNodes lists peers whose Audit call always errors.
type fakePeerAuditSource struct {
	peersErr  error
	perPeer   map[string][]peer.AuditRecord
	failNodes map[string]bool
	peers     []peer.Peer
}

func (f *fakePeerAuditSource) Peers(context.Context) ([]peer.Peer, error) {
	return f.peers, f.peersErr
}

func (f *fakePeerAuditSource) Audit(_ context.Context, p peer.Peer, _ peer.AuditFilter, _ string, limit int) ([]peer.AuditRecord, string, error) {
	if f.failNodes[p.Node] {
		return nil, "", errors.New("fake: peer unreachable")
	}
	items := f.perPeer[p.Node]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, "", nil
}

func auditTestRouter(svc AuditService, peers PeerAuditSource) http.Handler {
	return NewRouter(Options{
		Version:   "test",
		DistFS:    testDistFS(),
		Logger:    testLogger(),
		Auth:      fakeAuth{authenticated: true},
		Audit:     svc,
		PeerAudit: peers,
	})
}

func TestAuditRoute_LocalOnly_UnchangedWhenNoPeerSource(t *testing.T) {
	svc := &fakeAuditListService{entries: []store.AuditEntry{
		{ID: 1, At: 100, Username: "alice", Action: "changeset.apply", Result: "success"},
	}}
	r := auditTestRouter(svc, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body auditListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Username != "alice" {
		t.Fatalf("items = %+v", body.Items)
	}
	if body.Partial {
		t.Errorf("partial = true, want false (no peer source configured)")
	}
	// The field must be entirely absent (omitempty), matching pre-T-303
	// wire shape exactly, not just false.
	if strings.Contains(rec.Body.String(), `"partial"`) {
		t.Errorf("response includes a \"partial\" key at all with no peer source configured: %s", rec.Body.String())
	}
}

func TestAuditRoute_ClusterMerge(t *testing.T) {
	svc := &fakeAuditListService{entries: []store.AuditEntry{
		{ID: 1, At: 300, Username: "local-user", Action: "changeset.apply", Result: "success"},
	}}
	peers := &fakePeerAuditSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}, {Node: "pve3", Addr: "10.0.0.3:8007"}},
		perPeer: map[string][]peer.AuditRecord{
			"pve2": {{ID: 1, At: 200, Username: "pve2-user", Action: "changeset.confirm", Result: "success"}},
		},
		failNodes: map[string]bool{"pve3": true},
	}
	r := auditTestRouter(svc, peers)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body auditListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2 (local-user + pve2-user; pve3 failed)", body.Items)
	}
	if body.Items[0].Username != "local-user" || body.Items[1].Username != "pve2-user" {
		t.Errorf("items order = %+v, want [local-user (at=300), pve2-user (at=200)]", body.Items)
	}
	if !body.Partial {
		t.Error("partial = false, want true (pve3 failed)")
	}
	if len(body.FailedNodes) != 1 || body.FailedNodes[0] != "pve3" {
		t.Errorf("failedNodes = %v, want [pve3]", body.FailedNodes)
	}
}

// --- snapshots ---

type fakeSnapshotListService struct {
	items []change.SnapshotSummary
}

func (f *fakeSnapshotListService) ListSnapshots(_ context.Context, _ string, limit int) ([]change.SnapshotSummary, string, error) {
	items := f.items
	if len(items) > limit {
		items = items[:limit]
	}
	return items, "", nil
}
func (f *fakeSnapshotListService) GetSnapshot(context.Context, string) (change.SnapshotDetail, error) {
	return change.SnapshotDetail{}, sql.ErrNoRows
}
func (f *fakeSnapshotListService) CreateManualSnapshot(context.Context, string, string) (change.SnapshotSummary, error) {
	return change.SnapshotSummary{}, errors.New("unsupported in this fake")
}
func (f *fakeSnapshotListService) DiffSnapshots(context.Context, string, string) (*change.SnapshotDiff, error) {
	return nil, errors.New("unsupported in this fake")
}
func (f *fakeSnapshotListService) RestoreSnapshot(context.Context, string, string) (change.Changeset, error) {
	return change.Changeset{}, errors.New("unsupported in this fake")
}

type fakePeerSnapshotSource struct {
	perPeer   map[string][]peer.SnapshotRecord
	failNodes map[string]bool
	peers     []peer.Peer
}

func (f *fakePeerSnapshotSource) Peers(context.Context) ([]peer.Peer, error) { return f.peers, nil }

func (f *fakePeerSnapshotSource) Snapshots(_ context.Context, p peer.Peer, _ string, limit int) ([]peer.SnapshotRecord, string, error) {
	if f.failNodes[p.Node] {
		return nil, "", errors.New("fake: peer unreachable")
	}
	items := f.perPeer[p.Node]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, "", nil
}

func snapshotsTestRouter(svc SnapshotService, peers PeerSnapshotSource) http.Handler {
	return NewRouter(Options{
		Version:       "test",
		DistFS:        testDistFS(),
		Logger:        testLogger(),
		Auth:          fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		Snapshots:     svc,
		PeerSnapshots: peers,
	})
}

func TestSnapshotsRoute_ClusterMerge(t *testing.T) {
	svc := &fakeSnapshotListService{items: []change.SnapshotSummary{
		{ID: "01LOCAL", Kind: "manual", Nodes: []string{"pve1"}, TakenAt: 500},
	}}
	peers := &fakePeerSnapshotSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.0.0.2:8007"}},
		perPeer: map[string][]peer.SnapshotRecord{
			"pve2": {{ID: "01PEER", Kind: "pre", Nodes: []string{"pve1", "pve2"}, TakenAt: 400}},
		},
	}
	r := snapshotsTestRouter(svc, peers)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body snapshotListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 2 || body.Items[0].ID != "01LOCAL" || body.Items[1].ID != "01PEER" {
		t.Fatalf("items = %+v, want [01LOCAL (newer), 01PEER]", body.Items)
	}
	if body.Partial {
		t.Error("partial = true, want false (both sources healthy)")
	}
}

// TestAuditRoute_NoMutatingMethodMounted is T-604's automated check for
// docs/security.md's "Audit" claim: "Audit entries are append-only at the
// API layer; there is no delete endpoint." internal/api/audit.go mounts
// only r.Get("/audit", ...) and store.AuditRepo (internal/store/audit.go)
// exposes no Update/Delete method at all — this test closes the loop at
// the HTTP layer, proving no other route registration anywhere in the
// router answers a mutating method on this path (chi's default "method not
// allowed"/"not found" behavior, not bespoke app logic, is exactly the
// desired outcome here).
func TestAuditRoute_NoMutatingMethodMounted(t *testing.T) {
	svc := &fakeAuditListService{entries: []store.AuditEntry{
		{ID: 1, At: 100, Username: "alice", Action: "changeset.apply", Result: "success"},
	}}
	r := auditTestRouter(svc, nil)

	for _, method := range []string{http.MethodDelete, http.MethodPut, http.MethodPatch, http.MethodPost} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/audit", nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s /api/v1/audit = %d, want anything but 200 (audit must be append-only, no mutating method)", method, rec.Code)
		}
	}
}
