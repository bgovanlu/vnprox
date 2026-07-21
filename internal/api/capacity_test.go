package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeCapacityService serves a fixed set of aggregate rows, applying the
// `since` bound the handler passes so a test can prove the export is clamped
// to the retention window.
type fakeCapacityService struct {
	rows          []store.CapacityAggregate
	retentionDays int
}

func (f fakeCapacityService) ExportHistory(_ context.Context, ref, kind string, since int64) ([]store.CapacityAggregate, error) {
	var out []store.CapacityAggregate
	for _, a := range f.rows {
		if a.Ref == ref && a.Kind == kind && a.BucketAt >= since {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f fakeCapacityService) RetentionDays() int { return f.retentionDays }

func newCapacityTestRouter(svc CapacityService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Capacity: svc,
	})
}

func capacityTestRows() []store.CapacityAggregate {
	now := time.Now().UTC()
	recent := now.AddDate(0, 0, -2).Truncate(24 * time.Hour).Unix()
	older := now.AddDate(0, 0, -1).Truncate(24 * time.Hour).Unix()
	tooOld := now.AddDate(0, 0, -401).Truncate(24 * time.Hour).Unix() // beyond the 400-day window
	return []store.CapacityAggregate{
		{Ref: "iface:pve1:vmbr1", Kind: store.CapacityKindLink, BucketAt: tooOld, AvgUtilization: 5, MaxUtilization: 9, CreatedAt: 1},
		{Ref: "iface:pve1:vmbr1", Kind: store.CapacityKindLink, BucketAt: recent, AvgUtilization: 40, MaxUtilization: 55, CreatedAt: 2},
		{Ref: "iface:pve1:vmbr1", Kind: store.CapacityKindLink, BucketAt: older, AvgUtilization: 42, MaxUtilization: 60, CreatedAt: 3},
	}
}

func TestCapacityExport_JSONBoundedToRetention(t *testing.T) {
	svc := fakeCapacityService{rows: capacityTestRows(), retentionDays: store.DefaultCapacityRetentionDays}
	r := newCapacityTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/export?ref=iface:pve1:vmbr1&kind=link&format=json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var resp capacityExportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Ref != "iface:pve1:vmbr1" || resp.Kind != "link" {
		t.Errorf("ref/kind = %q/%q", resp.Ref, resp.Kind)
	}
	// The 401-day-old row must be excluded; only the two recent rows remain.
	if len(resp.Aggregates) != 2 {
		t.Fatalf("got %d aggregates, want 2 (the >400-day-old row must be bounded out)", len(resp.Aggregates))
	}
	if resp.Aggregates[1].MaxUtilization != 60 {
		t.Errorf("last row max = %.1f, want 60", resp.Aggregates[1].MaxUtilization)
	}
}

func TestCapacityExport_CSVRoundTrip(t *testing.T) {
	svc := fakeCapacityService{rows: capacityTestRows(), retentionDays: store.DefaultCapacityRetentionDays}
	r := newCapacityTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/export?ref=iface:pve1:vmbr1&kind=link&format=csv", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}
	// header + 2 bounded rows.
	if len(records) != 3 {
		t.Fatalf("got %d CSV records (incl header), want 3", len(records))
	}
	if records[0][0] != "ref" || records[0][4] != "max_utilization" {
		t.Errorf("header = %v", records[0])
	}
	if records[2][4] != "60" {
		t.Errorf("last data row max_utilization = %q, want 60", records[2][4])
	}
}

func TestCapacityExport_Validation(t *testing.T) {
	svc := fakeCapacityService{rows: capacityTestRows(), retentionDays: store.DefaultCapacityRetentionDays}
	r := newCapacityTestRouter(svc, fullCapsAuth("alice"))

	cases := []string{
		"/api/v1/capacity/export?ref=iface:pve1:vmbr1&kind=link&format=pdf",   // bad format
		"/api/v1/capacity/export?kind=link&format=json",                       // missing ref
		"/api/v1/capacity/export?ref=iface:pve1:vmbr1&kind=bogus&format=json", // bad kind
	}
	for _, url := range cases {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", url, rec.Code)
		}
	}
}

func TestCapacityExport_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/export?ref=x&kind=link", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestCapacityExport_RequiresNetRead(t *testing.T) {
	svc := fakeCapacityService{rows: capacityTestRows(), retentionDays: store.DefaultCapacityRetentionDays}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: false},
	}
	r := newCapacityTestRouter(svc, auth)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capacity/export?ref=x&kind=link&format=json", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
