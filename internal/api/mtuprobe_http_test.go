package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// This file is T-1306's HTTP-level coverage for GET /mtuprobe/results,
// mirroring latmesh_http_test.go's "fake local source, prove the wiring"
// pattern — internal/mtuprobe's own probe/query logic is covered by that
// package's own tests; this file only proves the router mounts the route
// netRead-gated.

type fakeMTUProbeService struct {
	results []mtuprobe.Result
}

func (f *fakeMTUProbeService) Results() []mtuprobe.Result {
	return f.results
}

func mtuProbeTestRouter(svc MTUProbeService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, MTUProbe: svc,
	})
}

func TestMTUProbeResults_ReturnsItems(t *testing.T) {
	svc := &fakeMTUProbeService{results: []mtuprobe.Result{
		{LinkID: "guest:vmbr0|pve1->pve2", Fabric: latmesh.FabricGuest, FromNode: "pve1", ToNode: "pve2",
			MTU: 1450, At: 100, ProbeCount: 12},
	}}
	r := mtuProbeTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mtuprobe/results", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []mtuProbeResultResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want 1", body.Items)
	}
	got := body.Items[0]
	if got.LinkID != "guest:vmbr0|pve1->pve2" || got.Fabric != "guest" || got.MTU != 1450 || got.ProbeCount != 12 {
		t.Fatalf("unexpected item shape: %+v", got)
	}
}

func TestMTUProbeResults_EmptyNotNull(t *testing.T) {
	svc := &fakeMTUProbeService{}
	r := mtuProbeTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mtuprobe/results", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []mtuProbeResultResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Items == nil {
		t.Fatal("items must be [] not null")
	}
}

func TestMTUProbeRoutes_NilServiceSkipsMounting(t *testing.T) {
	r := mtuProbeTestRouter(nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mtuprobe/results", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when MTUProbe is nil", rec.Code)
	}
}

func TestMTUProbeRoutes_RequireAuth(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, MTUProbe: &fakeMTUProbeService{},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/mtuprobe/results", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthenticated request got 200, want a rejection")
	}
}
