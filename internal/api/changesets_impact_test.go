// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// impactRouter wires the real change.Service (newChangesetTestService) so this
// exercises the actual Impact computation through the actual route, not a
// stubbed response shape.
func impactRouter(t *testing.T, svc ChangesetService) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology:   fakeTopologyService{},
		Changesets: svc,
	})
}

func getImpact(t *testing.T, r http.Handler, id string) (*httptest.ResponseRecorder, change.Impact) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+id+"/impact", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var imp change.Impact
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &imp); err != nil {
			t.Fatalf("decoding impact: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, imp
}

func TestChangesetImpactRoute_ReturnsAServerComputedImpact(t *testing.T) {
	svc := newChangesetTestService(t)
	cs, err := svc.Create(t.Context(), "root@pam", "widen vmbr0",
		[]change.Op{{Type: change.OpBridgeCreate, Target: bridgeRef("pve1", "vmbr9"), Params: &change.BridgeCreateParams{}}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r := impactRouter(t, svc)

	rec, imp := getImpact(t, r, cs.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(imp.Ops) != 1 {
		t.Fatalf("op impacts = %d, want 1", len(imp.Ops))
	}
	if imp.Ops[0].Reason == "" {
		t.Fatal("an op impact came back with no reason; a verdict the UI cannot explain")
	}
}

// AC4: impact is computed server-side. A client cannot supply or override it —
// asserted by sending a body that names a different verdict and observing that
// the response ignores it entirely.
func TestChangesetImpactRoute_IgnoresAnyClientSuppliedImpact(t *testing.T) {
	svc := newChangesetTestService(t)
	cs, err := svc.Create(t.Context(), "root@pam", "create vmbr9",
		[]change.Op{{Type: change.OpBridgeCreate, Target: bridgeRef("pve1", "vmbr9"), Params: &change.BridgeCreateParams{}}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r := impactRouter(t, svc)

	// The route is a GET with no body, which is itself the point: there is no
	// request field an impact could arrive in. A query string trying to force
	// one must change nothing.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/changesets/"+cs.ID+"/impact?disruption=none&guests=0&touchesMgmtPath=false", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var forced change.Impact
	if err := json.Unmarshal(rec.Body.Bytes(), &forced); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	_, plain := getImpact(t, r, cs.ID)
	if forced.Disruption != plain.Disruption || len(forced.Ops) != len(plain.Ops) {
		t.Fatalf("query parameters changed the computed impact: %+v vs %+v", forced, plain)
	}
}

func TestChangesetImpactRoute_RequiresAuthentication(t *testing.T) {
	// An auth backend that still satisfies UsernameLookup (or the changeset
	// routes are not mounted at all and this would assert 404 == "denied",
	// which proves nothing) but reports no session.
	unauthenticated := fakeAuthWithCaps{
		caps: map[string]bool{"netRead": true}, csrf: true,
		fakeAuthWithUser: fakeAuthWithUser{username: "", fakeAuth: fakeAuth{authenticated: false}},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: unauthenticated, Topology: fakeTopologyService{},
		Changesets: newChangesetTestService(t),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/anything/impact", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// Reading the blast radius is a READ. Requiring the capability to apply in
// order to find out what applying would break would be exactly backwards.
func TestChangesetImpactRoute_NeedsOnlyNetRead(t *testing.T) {
	svc := newChangesetTestService(t)
	cs, err := svc.Create(t.Context(), "root@pam", "create vmbr9",
		[]change.Op{{Type: change.OpBridgeCreate, Target: bridgeRef("pve1", "vmbr9"), Params: &change.BridgeCreateParams{}}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true}), // no netWrite
		Topology: fakeTopologyService{}, Changesets: svc,
	})
	rec, _ := getImpact(t, r, cs.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d with netRead alone, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestChangesetImpactRoute_UnknownChangeset404(t *testing.T) {
	r := impactRouter(t, newChangesetTestService(t))
	rec, _ := getImpact(t, r, "01JZZZZZZZZZZZZZZZZZZZZZZZ")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}
