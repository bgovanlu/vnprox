package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// jsonBody marshals v into a request-body reader for POST/PUT test requests.
func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling test request body: %v", err)
	}
	return bytes.NewBuffer(b)
}

// fakeNodeAgent is a minimal in-memory change.NodeAgent for API-layer tests
// that need Snapshots wired (change.Service.applyConfigured() requires
// Nodes/Snapshots/Blobs all non-nil).
type fakeNodeAgent struct {
	committed map[string]string
	staged    map[string]string
	mu        sync.Mutex
}

func newFakeNodeAgentAPI(seed map[string]string) *fakeNodeAgent {
	committed := make(map[string]string, len(seed))
	for k, v := range seed {
		committed[k] = v
	}
	return &fakeNodeAgent{committed: committed, staged: map[string]string{}}
}

func (a *fakeNodeAgent) ReadInterfaces(_ context.Context, node string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.committed[node], nil
}

func (a *fakeNodeAgent) StageInterfaces(_ context.Context, node, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.staged[node] = content
	return nil
}

func (a *fakeNodeAgent) ReloadInterfaces(_ context.Context, node string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.committed[node] = a.staged[node]
	delete(a.staged, node)
	return nil
}

func (a *fakeNodeAgent) DiscardStaged(_ context.Context, node string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.staged, node)
	return nil
}

type snapshotFakeInventory struct{ snap inventory.Snapshot }

func (f snapshotFakeInventory) Snapshot() inventory.Snapshot { return f.snap }

func oneNodeInventorySnapshot() inventory.Snapshot {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online", Quorate: true, Local: true},
	})
	return g.Snapshot()
}

const snapshotTestBaseInterfaces = "auto lo\niface lo inet loopback\n"

// inertTimer is a change.Stopper whose timer never fires (see
// newSnapshotTestService's TimerFunc).
type inertTimer struct{}

func (inertTimer) Stop() bool { return true }

func newSnapshotTestService(t *testing.T) (*change.Service, *store.AuditRepo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	auditRepo := store.NewAuditRepo(db)
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      auditRepo,
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Nodes:      newFakeNodeAgentAPI(map[string]string{"pve1": snapshotTestBaseInterfaces}),
		Inventory:  snapshotFakeInventory{snap: oneNodeInventorySnapshot()},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
		// Inert commit-confirm timer: with the frozen Now above, a real
		// time.AfterFunc would compute a deadline in the past and fire the
		// auto-rollback immediately, racing any test that confirms.
		TimerFunc: func(time.Duration, func()) change.Stopper { return inertTimer{} },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc, auditRepo
}

func newSnapshotTestRouter(svc *change.Service, audit *store.AuditRepo, auth fakeAuthWithCaps) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Snapshots: svc, Audit: audit,
	})
}

func snapshotCapsAuth(username string) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: username},
		caps:             map[string]bool{capNetRead: true, capNetWrite: true, capAudit: true},
	}
}

func TestSnapshotsRoutes_ListCreateDetailDiffRestore(t *testing.T) {
	svc, audit := newSnapshotTestService(t)
	auth := snapshotCapsAuth("alice")
	r := newSnapshotTestRouter(svc, audit, auth)

	// Create a manual snapshot via the HTTP route.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/snapshots", jsonBody(t, map[string]any{"note": "baseline"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /snapshots status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var created change.SnapshotSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.Kind != "manual" || created.Note != "baseline" {
		t.Fatalf("created = %+v", created)
	}

	// List it back.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /snapshots status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var list snapshotListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list = %+v, want [created]", list.Items)
	}

	// Detail.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /snapshots/{id} status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var detail change.SnapshotDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decoding detail response: %v", err)
	}
	if len(detail.Files) != 1 || detail.Files[0].Node != "pve1" {
		t.Fatalf("detail.Files = %+v", detail.Files)
	}

	// Diff against live (identical: no changes since capture).
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots/diff?from="+created.ID+"&to=live", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /snapshots/diff status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var diff change.SnapshotDiff
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decoding diff response: %v", err)
	}
	for _, f := range diff.Files {
		if f.Changed {
			t.Fatalf("diff(created,live) reports a change with no drift: %+v", f)
		}
	}

	// Missing from/to -> 400.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots/diff?from="+created.ID, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /snapshots/diff (missing to) status = %d, want 400", rec.Code)
	}

	// Restore: creates a draft changeset (empty ops here, since live already
	// matches).
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/snapshots/"+created.ID+"/restore", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /snapshots/{id}/restore status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var draft changesetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decoding restore response: %v", err)
	}
	if draft.Status != "draft" {
		t.Fatalf("restore draft status = %s, want draft", draft.Status)
	}

	// Unknown snapshot -> 404.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /snapshots/{missing} status = %d, want 404", rec.Code)
	}
}

func TestSnapshotsRoutes_RequireNetReadNetWrite(t *testing.T) {
	svc, audit := newSnapshotTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: false, capNetWrite: false},
	}
	r := newSnapshotTestRouter(svc, audit, auth)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snapshots", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET /snapshots without netRead: status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/snapshots", jsonBody(t, map[string]any{})))
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /snapshots without netWrite: status = %d, want 403", rec.Code)
	}
}

func TestAuditRoutes_ListFiltersAndCapability(t *testing.T) {
	svc, audit := newSnapshotTestService(t)
	ctx := context.Background()
	if _, err := audit.Append(ctx, store.AuditEntry{At: 100, Username: "alice", Action: "changeset.apply", Result: "success"}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}
	if _, err := audit.Append(ctx, store.AuditEntry{At: 101, Username: "bob", Action: "changeset.apply", Result: "failed"}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	auth := snapshotCapsAuth("alice")
	r := newSnapshotTestRouter(svc, audit, auth)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit?user=bob", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /audit?user=bob status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var list auditListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding audit list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Username != "bob" {
		t.Fatalf("audit list = %+v, want [bob]", list.Items)
	}

	noAudit := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capAudit: false},
	}
	r2 := newSnapshotTestRouter(svc, audit, noAudit)
	rec = httptest.NewRecorder()
	r2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /audit without audit cap: status = %d, want 403", rec.Code)
	}
}

// AC5's "every T-205 lifecycle event appears": drive a real apply→confirm→
// rollback(committed, creates restoring draft) lifecycle through the change
// service, then assert each T-205 audit action surfaces through GET /audit.
func TestAuditRoutes_LifecycleEventsAppear(t *testing.T) {
	svc, audit := newSnapshotTestService(t)
	ctx := context.Background()

	ops := []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"},
		Params: &change.BridgeCreateParams{},
	}}
	cs, err := svc.Create(ctx, "root@pam", "add vmbr1", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := svc.Rollback(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Rollback (committed): %v", err)
	}

	r := newSnapshotTestRouter(svc, audit, snapshotCapsAuth("root@pam"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=100", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /audit status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var list auditListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding audit list: %v", err)
	}
	seen := map[string]map[string]bool{}
	for _, item := range list.Items {
		if seen[item.Action] == nil {
			seen[item.Action] = map[string]bool{}
		}
		seen[item.Action][item.Result] = true
	}
	for _, want := range []struct{ action, result string }{
		{"changeset.create", "success"},
		{"changeset.apply", "applying"},
		{"changeset.apply", "awaiting_confirm"},
		{"changeset.confirm", "committed"},
		{"changeset.rollback", "restoring_draft_created"},
	} {
		if !seen[want.action][want.result] {
			t.Errorf("audit route missing %s/%s; got %v", want.action, want.result, seen)
		}
	}
}
