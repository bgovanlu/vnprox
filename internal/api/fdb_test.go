package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeFDBService is a minimal FDBService stand-in for router tests.
type fakeFDBService struct {
	lastQ   string
	all     []topology.FDBRow
	results []topology.FDBRow
}

func (f *fakeFDBService) FDB() []topology.FDBRow { return f.all }
func (f *fakeFDBService) FDBSearch(q string) []topology.FDBRow {
	f.lastQ = q
	return f.results
}

func TestFDBRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{}, FDB: &fakeFDBService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fdb", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/fdb (unauthenticated) status = %d, want 401", rec.Code)
	}
}

func TestFDBRoute_NoQuery_ListsEverything(t *testing.T) {
	svc := &fakeFDBService{all: []topology.FDBRow{
		{Node: "pve1", Bridge: "vmbr0", Mac: "AA:BB:CC:DD:EE:FF", Owner: topology.OwnerUnknown, Stale: false},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, FDB: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fdb", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []topology.FDBRow `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Mac != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("items = %+v, want the one FDB() row (blank ?mac= lists, doesn't search)", body.Items)
	}
	if svc.lastQ != "" {
		t.Errorf("FDBSearch was called with q=%q; blank ?mac= must call FDB(), not FDBSearch", svc.lastQ)
	}
}

func TestFDBRoute_WithQuery_Searches(t *testing.T) {
	svc := &fakeFDBService{
		all:     []topology.FDBRow{{Mac: "should-not-be-returned"}},
		results: []topology.FDBRow{{Node: "pve2", Bridge: "vmbr0", Mac: "AA:24:11:00:00:01", Owner: topology.OwnerGuest, Score: 80}},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, FDB: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fdb?mac=aa24", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastQ != "aa24" {
		t.Errorf("FDBSearch called with q=%q, want aa24", svc.lastQ)
	}
	var body struct {
		Items []topology.FDBRow `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Owner != topology.OwnerGuest {
		t.Errorf("items = %+v, want the one ranked search result", body.Items)
	}
}
