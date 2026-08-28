// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/route"
)

// fakeRouteService is a minimal RouteService stand-in for router tests.
type fakeRouteService struct {
	snapErr   error
	lookupErr error
	lastNode  string
	lastDst   string
	lastIface string
	nodes     []string
	lookup    route.LookupResult
	snapshot  route.Snapshot
}

func (f *fakeRouteService) Nodes(context.Context) []string { return f.nodes }

func (f *fakeRouteService) Snapshot(_ context.Context, node string) (route.Snapshot, error) {
	f.lastNode = node
	return f.snapshot, f.snapErr
}

func (f *fakeRouteService) Lookup(_ context.Context, node, dst, ifaceHint string) (route.LookupResult, error) {
	f.lastNode, f.lastDst, f.lastIface = node, dst, ifaceHint
	return f.lookup, f.lookupErr
}

func TestRouteRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{}, Route: &fakeRouteService{},
	})
	for _, path := range []string{"/api/v1/route/nodes", "/api/v1/route/snapshot", "/api/v1/route/lookup?dst=1.2.3.4"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s (unauthenticated) status = %d, want 401", path, rec.Code)
		}
	}
}

func TestRouteRoutes_NotMountedWhenNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/nodes", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (Route service not wired)", rec.Code)
	}
}

func TestRouteNodes(t *testing.T) {
	svc := &fakeRouteService{nodes: []string{"pve1", "pve2"}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Route: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/nodes", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Nodes []string `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Nodes) != 2 || body.Nodes[0] != "pve1" {
		t.Errorf("nodes = %v", body.Nodes)
	}
}

func TestRouteSnapshot(t *testing.T) {
	svc := &fakeRouteService{snapshot: route.Snapshot{
		Node: "pve1",
		FIB: []route.FIBRoute{
			{AFI: route.AFIv4, Table: "main", Type: "unicast", Dst: "0.0.0.0/0", Gateway: "192.168.1.1", Dev: "vmbr0"},
		},
		Rules:          []route.PolicyRule{{AFI: route.AFIv4, Priority: 32766, Src: "all", Table: "main"}},
		FRRUnavailable: true,
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Route: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/snapshot?node=pve1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastNode != "pve1" {
		t.Errorf("Snapshot called with node=%q, want pve1", svc.lastNode)
	}
	var body routeSnapshotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Node != "pve1" || len(body.FIB) != 1 || len(body.Rules) != 1 || !body.FRRUnavailable {
		t.Errorf("body = %+v", body)
	}
	if len(body.RIB) != 0 {
		t.Errorf("RIB = %v, want empty (FRRUnavailable)", body.RIB)
	}
}

func TestRouteSnapshot_NodeNotFound(t *testing.T) {
	svc := &fakeRouteService{snapErr: route.ErrNodeNotFound}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Route: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/snapshot?node=nope", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRouteLookup_MissingDst400(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Route: &fakeRouteService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/lookup", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (dst is required)", rec.Code)
	}
}

func TestRouteLookup_Reachable(t *testing.T) {
	matched := route.FIBRoute{AFI: route.AFIv4, Table: "main", Dst: "0.0.0.0/0", Gateway: "192.168.1.1", Dev: "vmbr0"}
	svc := &fakeRouteService{lookup: route.LookupResult{
		Dst: "8.8.8.8", Reachable: true, MatchedRoute: &matched, Trace: []string{"table main: matched"},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Route: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/lookup?node=pve1&dst=8.8.8.8&iface=vmbr0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastNode != "pve1" || svc.lastDst != "8.8.8.8" || svc.lastIface != "vmbr0" {
		t.Errorf("Lookup called with (%q, %q, %q)", svc.lastNode, svc.lastDst, svc.lastIface)
	}
	var body routeLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !body.Reachable || body.MatchedRoute == nil || body.MatchedRoute.Dev != "vmbr0" {
		t.Errorf("body = %+v", body)
	}
}

func TestRouteLookup_Ambiguous(t *testing.T) {
	svc := &fakeRouteService{lookup: route.LookupResult{
		Dst: "fe80::1", Reachable: false, Ambiguous: []string{"vmbr0", "vmbr2", "vmbr99"},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Route: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/route/lookup?dst=fe80::1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body routeLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Reachable || len(body.Ambiguous) != 3 {
		t.Errorf("body = %+v, want unreachable with 3 ambiguous candidates", body)
	}
}
