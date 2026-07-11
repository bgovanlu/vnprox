package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/metrics"
)

// fakeMetricsService is a minimal MetricsService stand-in for router tests.
type fakeMetricsService struct {
	historyErr     error
	lastHistoryRef string
	lastRefs       []string
	liveResult     []metrics.LiveMetric
	historyResult  []metrics.HistoryPoint
	lastFromTs     int64
	lastToTs       int64
}

func (f *fakeMetricsService) Live(refs []string) []metrics.LiveMetric {
	f.lastRefs = refs
	return f.liveResult
}

func (f *fakeMetricsService) History(_ context.Context, ref string, fromTs, toTs int64) ([]metrics.HistoryPoint, error) {
	f.lastHistoryRef = ref
	f.lastFromTs, f.lastToTs = fromTs, toTs
	return f.historyResult, f.historyErr
}

func TestMetricsLiveRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{}, Metrics: &fakeMetricsService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/live?refs=a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMetricsLiveRoute_ParsesRefsAndReturnsItems(t *testing.T) {
	svc := &fakeMetricsService{liveResult: []metrics.LiveMetric{
		{Ref: "physnic:pve1:eno1", At: 100, Rates: metrics.Rates{RxBps: 1000}},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Metrics: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/live?refs=physnic:pve1:eno1,%20bond:pve1:bond0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if len(svc.lastRefs) != 2 || svc.lastRefs[0] != "physnic:pve1:eno1" || svc.lastRefs[1] != "bond:pve1:bond0" {
		t.Errorf("Live() called with refs = %v, want the two trimmed, comma-split refs", svc.lastRefs)
	}
	var body struct {
		Items []metrics.LiveMetric `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Ref != "physnic:pve1:eno1" {
		t.Errorf("items = %+v, want the one stubbed LiveMetric", body.Items)
	}
}

func TestMetricsLiveRoute_NoRefs_EmptyItems(t *testing.T) {
	svc := &fakeMetricsService{}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Metrics: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/live", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []metrics.LiveMetric `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 0 {
		t.Errorf("items = %+v, want empty (no ?refs=)", body.Items)
	}
}

func TestMetricsHistoryRoute_MissingRef_400(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Metrics: &fakeMetricsService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMetricsHistoryRoute_ParsesRefAndTimeRange(t *testing.T) {
	svc := &fakeMetricsService{historyResult: []metrics.HistoryPoint{
		{At: 30, Rates: metrics.Rates{RxBps: 500}},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Metrics: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history?ref=physnic:pve1:eno1&fromTs=0&toTs=60", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastHistoryRef != "physnic:pve1:eno1" || svc.lastFromTs != 0 || svc.lastToTs != 60 {
		t.Errorf("History() called with ref=%q from=%d to=%d, want physnic:pve1:eno1/0/60", svc.lastHistoryRef, svc.lastFromTs, svc.lastToTs)
	}
	var body struct {
		Ref   string                 `json:"ref"`
		Items []metrics.HistoryPoint `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Ref != "physnic:pve1:eno1" || len(body.Items) != 1 {
		t.Errorf("body = %+v, want ref echoed + the one stubbed point", body)
	}
}

func TestMetricsHistoryRoute_ServiceError_500(t *testing.T) {
	svc := &fakeMetricsService{historyErr: errors.New("boom")}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Metrics: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history?ref=x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
