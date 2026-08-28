// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeDriftService is a minimal DriftService stand-in for router tests.
type fakeDriftService struct {
	fixOps   map[string][]change.Op
	fixTitle map[string]string
	findings []drift.Finding
}

func (f fakeDriftService) Findings() []drift.Finding { return f.findings }

func (f fakeDriftService) FixOps(id string) ([]change.Op, string, bool) {
	ops, ok := f.fixOps[id]
	if !ok {
		return nil, "", false
	}
	return ops, f.fixTitle[id], true
}

func driftTestAuth(caps map[string]bool) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		caps: caps, csrf: true,
		fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
	}
}

func TestDriftRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{}, Drift: fakeDriftService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/drift", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/drift (unauthenticated) status = %d, want 401", rec.Code)
	}
}

func TestDriftRoute_Authenticated(t *testing.T) {
	finding := drift.Finding{ID: "mtu_consistency|bridge:pve2:vmbr0", Check: drift.CheckMTUConsistency,
		Severity: drift.SeverityWarning, Detail: "bridge vmbr0 MTU drifted", Nodes: []string{"pve2"}, Fixable: true}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"netRead": true}), Topology: fakeTopologyService{},
		Drift: fakeDriftService{findings: []drift.Finding{finding}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/drift", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/drift status = %d, body: %s", rec.Code, rec.Body.String())
	}

	// docs/api.md documents GET /drift as a bare array, not an
	// {items:[...]} envelope — decoding straight into a slice proves the
	// shape matches exactly.
	var got []drift.Finding
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response as a bare array: %v", err)
	}
	if len(got) != 1 || got[0].ID != finding.ID || got[0].Check != finding.Check || !got[0].Fixable {
		t.Errorf("got %+v, want one finding matching %+v", got, finding)
	}
}

func TestDriftRoute_EmptyFindingsIsEmptyArray(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"netRead": true}), Topology: fakeTopologyService{},
		Drift: fakeDriftService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/drift", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if body := rec.Body.String(); body != "[]\n" {
		t.Errorf("body = %q, want a JSON empty array (never null)", body)
	}
}

func TestDriftFix_CreatesChangeset(t *testing.T) {
	svc := newChangesetTestService(t)
	target := inventory.Ref{Kind: inventory.KindBridge, Node: "pve2", ID: "vmbr0"}
	mtu := 1500
	ds := fakeDriftService{
		fixOps: map[string][]change.Op{
			"finding-1": {{Type: change.OpBridgeUpdate, Target: target, Params: &change.BridgeUpdateParams{MTU: &mtu}}},
		},
		fixTitle: map[string]string{"finding-1": "drift: align bridge vmbr0 MTU to 1500"},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology: fakeTopologyService{}, Drift: ds, Changesets: svc,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drift/finding-1/fix", bytes.NewReader(nil))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/drift/finding-1/fix status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var body changesetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Title != "drift: align bridge vmbr0 MTU to 1500" {
		t.Errorf("title = %q, want the fix's suggested title", body.Title)
	}
	if body.Author != "root@pam" {
		t.Errorf("author = %q, want root@pam (the session's username)", body.Author)
	}
	if len(body.Ops) != 1 || body.Ops[0].Type != change.OpBridgeUpdate {
		t.Fatalf("ops = %+v, want the one bridge.update op", body.Ops)
	}

	// The changeset was really persisted through the normal engine.
	stored, err := svc.Get(req.Context(), body.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", body.ID, err)
	}
	if stored.Status != change.StatusDraft {
		t.Errorf("stored status = %s, want draft", stored.Status)
	}
}

func TestDriftFix_UnknownFinding404(t *testing.T) {
	svc := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology: fakeTopologyService{}, Drift: fakeDriftService{}, Changesets: svc,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drift/no-such-finding/fix", bytes.NewReader(nil))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestDriftFix_RequiresNetWrite(t *testing.T) {
	svc := newChangesetTestService(t)
	target := inventory.Ref{Kind: inventory.KindBridge, Node: "pve2", ID: "vmbr0"}
	mtu := 1500
	ds := fakeDriftService{fixOps: map[string][]change.Op{
		"finding-1": {{Type: change.OpBridgeUpdate, Target: target, Params: &change.BridgeUpdateParams{MTU: &mtu}}},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		// netWrite deliberately omitted.
		Auth:     driftTestAuth(map[string]bool{"netRead": true}),
		Topology: fakeTopologyService{}, Drift: ds, Changesets: svc,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drift/finding-1/fix", bytes.NewReader(nil))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing netWrite): %s", rec.Code, rec.Body.String())
	}
}

// TestTopologyRoute_PaintsDriftBadge is T-305's map dashed-outline
// deliverable: GET /topology decorates any node named by an open drift
// finding's Refs with a "drift" badge, additive to whatever badges the
// projection itself already produced.
func TestTopologyRoute_PaintsDriftBadge(t *testing.T) {
	nodes := []topology.Node{
		{ID: "bridge:pve2:vmbr0", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{"vlans=10-20"}},
		{ID: "bridge:pve1:vmbr0", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{}},
	}
	finding := drift.Finding{ID: "mtu_consistency|bridge:pve2:vmbr0", Check: drift.CheckMTUConsistency,
		Severity: drift.SeverityWarning, Refs: []string{"bridge:pve2:vmbr0"}, Fixable: true}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true}),
		Topology: fakeTopologyService{nodes: nodes}, Drift: fakeDriftService{findings: []drift.Finding{finding}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/topology status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var got topology.Topology
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	byID := map[string]topology.Node{}
	for _, n := range got.Nodes {
		byID[n.ID] = n
	}
	drifted := byID["bridge:pve2:vmbr0"]
	if !containsString(drifted.Badges, "drift") {
		t.Errorf("bridge:pve2:vmbr0 badges = %v, want a \"drift\" badge", drifted.Badges)
	}
	if !containsString(drifted.Badges, "vlans=10-20") {
		t.Errorf("bridge:pve2:vmbr0 badges = %v, want its original \"vlans=10-20\" badge preserved", drifted.Badges)
	}
	clean := byID["bridge:pve1:vmbr0"]
	if containsString(clean.Badges, "drift") {
		t.Errorf("bridge:pve1:vmbr0 (not named by any finding) badges = %v, want no \"drift\" badge", clean.Badges)
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
