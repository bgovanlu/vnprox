// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// This file is T-1303's HTTP-level coverage for GET /latmesh/heatmap and
// GET /latmesh/history, mirroring flows_http_test.go's "fake local source,
// prove the wiring" pattern — internal/latmesh's own rolling-window/query
// logic is covered by that package's own tests; this file only proves the
// router mounts both routes netRead-gated and passes query params through
// correctly.

type fakeLatMeshService struct {
	heatmapErr    error
	historyErr    error
	lastLinkID    string
	heatmap       []latmesh.LinkHeat
	history       []latmesh.Sample
	lastFromTs    int64
	lastToTs      int64
	historyCalled bool
}

func (f *fakeLatMeshService) Heatmap(context.Context) ([]latmesh.LinkHeat, error) {
	return f.heatmap, f.heatmapErr
}

func (f *fakeLatMeshService) History(_ context.Context, linkID string, fromTs, toTs int64) ([]latmesh.Sample, error) {
	f.historyCalled = true
	f.lastLinkID, f.lastFromTs, f.lastToTs = linkID, fromTs, toTs
	return f.history, f.historyErr
}

func latMeshTestRouter(svc LatMeshService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, LatMesh: svc,
	})
}

func TestLatMeshHeatmap_ReturnsItems(t *testing.T) {
	svc := &fakeLatMeshService{heatmap: []latmesh.LinkHeat{
		{LinkID: "corosync:ring0|pve1->pve2", Fabric: latmesh.FabricCorosync, FromNode: "pve1", ToNode: "pve2",
			At: 100, RttMs: 10, LossPct: 0, RollingRttMs: 11, RollingLossPct: 0, SampleCount: 5},
	}}
	r := latMeshTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/heatmap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []latMeshLinkResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want 1", body.Items)
	}
	got := body.Items[0]
	if got.LinkID != "corosync:ring0|pve1->pve2" || got.Fabric != "corosync" || got.RollingRttMs != 11 || got.SampleCount != 5 {
		t.Fatalf("unexpected item shape: %+v", got)
	}
}

func TestLatMeshHeatmap_EmptyNotNull(t *testing.T) {
	svc := &fakeLatMeshService{}
	r := latMeshTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/heatmap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []latMeshLinkResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Items == nil {
		t.Fatal("items must be [] not null")
	}
}

func TestLatMeshHeatmap_ServiceError(t *testing.T) {
	svc := &fakeLatMeshService{heatmapErr: errors.New("boom")}
	r := latMeshTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/heatmap", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestLatMeshHistory_PassesQueryParams(t *testing.T) {
	svc := &fakeLatMeshService{history: []latmesh.Sample{
		{LinkID: "guest:vmbr0|pve1->pve2", At: 100, RttMs: 5, LossPct: 0},
	}}
	r := latMeshTestRouter(svc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/history?linkId=guest:vmbr0|pve1->pve2&fromTs=50&toTs=150", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !svc.historyCalled {
		t.Fatal("History was never called")
	}
	if svc.lastLinkID != "guest:vmbr0|pve1->pve2" || svc.lastFromTs != 50 || svc.lastToTs != 150 {
		t.Fatalf("params passed through = (%q,%d,%d)", svc.lastLinkID, svc.lastFromTs, svc.lastToTs)
	}
	var body struct {
		LinkID string                  `json:"linkId"`
		Items  []latMeshSampleResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].RttMs != 5 {
		t.Fatalf("items = %+v", body.Items)
	}
}

func TestLatMeshHistory_MissingLinkIdIs400(t *testing.T) {
	r := latMeshTestRouter(&fakeLatMeshService{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/history", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestLatMeshRoutes_NilServiceSkipsMounting(t *testing.T) {
	r := latMeshTestRouter(nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/heatmap", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when LatMesh is nil", rec.Code)
	}
}

func TestLatMeshRoutes_RequireAuth(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, LatMesh: &fakeLatMeshService{},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/latmesh/heatmap", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthenticated request got 200, want a rejection")
	}
}
