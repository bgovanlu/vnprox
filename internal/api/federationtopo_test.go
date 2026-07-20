package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/federation"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeFederationAggregator scripts the global-read responses so the HTTP
// layer's envelope (partial/failedClusters, namespacing, error mapping) is
// tested without a real fan-out.
type fakeFederationAggregator struct {
	topoErr   error
	lastQuery string
	summaries []federation.ClusterSummary
	hits      []federation.SearchHit
	failed    []string
	topo      topology.Topology
	partial   bool
}

func (f *fakeFederationAggregator) TopologySummary(context.Context) ([]federation.ClusterSummary, bool, []string, error) {
	return f.summaries, f.partial, f.failed, nil
}

func (f *fakeFederationAggregator) ClusterTopology(_ context.Context, _ string) (topology.Topology, error) {
	if f.topoErr != nil {
		return topology.Topology{}, f.topoErr
	}
	return f.topo, nil
}

func (f *fakeFederationAggregator) Search(_ context.Context, q string, _ int) ([]federation.SearchHit, bool, []string, error) {
	f.lastQuery = q
	return f.hits, f.partial, f.failed, nil
}

func newFederationTopoRouter(caps map[string]bool, agg FederationAggregator) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuthWithCaps{
			caps: caps, csrf: true,
			fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
		},
		Topology:      fakeTopologyService{},
		FederationAgg: agg,
	})
}

func TestFederationTopologyRoutes_NotMountedWithoutAgg(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     fakeAuthWithCaps{caps: map[string]bool{"netRead": true}, csrf: true, fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}}},
		Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}

func TestFederationTopology_SummaryEnvelope(t *testing.T) {
	agg := &fakeFederationAggregator{
		summaries: []federation.ClusterSummary{
			{ClusterID: "a", ClusterName: "east", Reachable: true, Nodes: 3, NodesOnline: 3, Guests: 4},
			{ClusterID: "b", ClusterName: "west", Reachable: false},
		},
		partial: true,
		failed:  []string{"b"},
	}
	r := newFederationTopoRouter(map[string]bool{"netRead": true}, agg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp federationTopologyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Clusters) != 2 || !resp.Partial || len(resp.FailedClusters) != 1 || resp.FailedClusters[0] != "b" {
		t.Fatalf("unexpected envelope: %+v", resp)
	}
	if resp.Clusters[1].Reachable {
		t.Error("failed cluster reported reachable")
	}
}

func TestFederationTopology_RequiresNetRead(t *testing.T) {
	r := newFederationTopoRouter(map[string]bool{}, &fakeFederationAggregator{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing netRead)", rec.Code)
	}
}

func TestFederationSearch_NamespacedResults(t *testing.T) {
	agg := &fakeFederationAggregator{
		hits: []federation.SearchHit{
			{ClusterID: "a", ClusterName: "east", Ref: "guest:pve1:100", Kind: "guest", Label: "db", Node: "pve1", MatchedField: "name"},
			{ClusterID: "b", ClusterName: "west", Ref: "guest:pve1:100", Kind: "guest", Label: "db", Node: "pve1", MatchedField: "name"},
		},
	}
	r := newFederationTopoRouter(map[string]bool{"netRead": true}, agg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/search?q=db", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if agg.lastQuery != "db" {
		t.Errorf("aggregator saw q=%q, want db", agg.lastQuery)
	}
	var resp federationSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 || resp.Results[0].ClusterID == resp.Results[1].ClusterID {
		t.Fatalf("want two distinctly-namespaced hits, got %+v", resp.Results)
	}
}

func TestFederationClusterTopology_ErrorMapping(t *testing.T) {
	// Unknown cluster -> 404.
	notFound := &fakeFederationAggregator{topoErr: store.ErrNotFound}
	r := newFederationTopoRouter(map[string]bool{"netRead": true}, notFound)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/topology/clusters/nope", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown cluster status = %d, want 404", rec.Code)
	}

	// Reachable cluster -> 200 with the projected topology.
	ok := &fakeFederationAggregator{topo: topology.Topology{Nodes: []topology.Node{{ID: "x", Kind: "bridge"}}}}
	r = newFederationTopoRouter(map[string]bool{"netRead": true}, ok)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/federation/topology/clusters/a", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var topo topology.Topology
	if err := json.Unmarshal(rec.Body.Bytes(), &topo); err != nil {
		t.Fatalf("decode topo: %v", err)
	}
	if len(topo.Nodes) != 1 {
		t.Errorf("got %d nodes, want 1", len(topo.Nodes))
	}
}
