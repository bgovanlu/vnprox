// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fwlog"
)

// fakeFwLogService is a minimal FwLogService stand-in for router tests:
// records the last filter/limit (or analytics window) it was called with
// and returns a pre-set page/analytics value.
type fakeFwLogService struct {
	analytics     fwlog.Analytics
	lastFilter    fwlog.Filter
	page          fwlog.Page
	lastLimit     int
	lastWindow    time.Duration
	lastTopN      int
	analyticsCall bool
}

func (f *fakeFwLogService) TailPage(filter fwlog.Filter, limit int) fwlog.Page {
	f.lastFilter = filter
	f.lastLimit = limit
	return f.page
}

func (f *fakeFwLogService) Analytics(_ time.Time, window time.Duration, topN int) fwlog.Analytics {
	f.analyticsCall = true
	f.lastWindow = window
	f.lastTopN = topN
	return f.analytics
}

func TestFwLogRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, FwLog: &fakeFwLogService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/log", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestFwLogRoute_NotMountedWhenServiceNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/log", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted when FwLog is nil)", rec.Code)
	}
}

func TestFwLogRoute_ListsAndDecodesEntries(t *testing.T) {
	svc := &fakeFwLogService{
		page: fwlog.Page{
			DroppedTotal:     3,
			UnavailableNodes: []string{"pve2"},
			Items: []fwlog.StreamEntry{
				{
					Seq: 1,
					Entry: fwlog.Entry{
						Node: "pve1", VMID: 100, Guest: true, Direction: "in", Action: "DROP",
						Raw: "100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 DROP: SRC=1.1.1.1 DST=2.2.2.2",
					},
					Correlation: fwlog.Correlation{
						Status: fwlog.StatusRule,
						Rule:   &fwlog.RuleRef{GuestRef: "guest:pve1:100", Origin: "guest", Pos: 2},
					},
				},
			},
		},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: svc,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/log?node=pve1&vmid=100&direction=in&action=drop&limit=50", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	if svc.lastFilter.Node != "pve1" || svc.lastFilter.VMID != 100 || svc.lastFilter.Direction != "in" || svc.lastFilter.Action != "drop" {
		t.Errorf("filter passed through = %+v, want node=pve1 vmid=100 direction=in action=drop", svc.lastFilter)
	}
	if svc.lastLimit != 50 {
		t.Errorf("limit = %d, want 50", svc.lastLimit)
	}

	var body struct {
		Items []struct {
			GuestRef    string `json:"guestRef"`
			Node        string `json:"node"`
			Correlation struct {
				Status string `json:"status"`
				Rule   struct {
					GuestRef string `json:"guestRef"`
					Origin   string `json:"origin"`
					Pos      int    `json:"pos"`
				} `json:"rule"`
			} `json:"correlation"`
		} `json:"items"`
		UnavailableNodes []string `json:"unavailableNodes"`
		DroppedTotal     int64    `json:"droppedTotal"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].GuestRef != "guest:pve1:100" {
		t.Fatalf("items = %+v", body.Items)
	}
	if body.Items[0].Correlation.Status != "rule" || body.Items[0].Correlation.Rule.Pos != 2 {
		t.Fatalf("correlation = %+v", body.Items[0].Correlation)
	}
	if body.DroppedTotal != 3 {
		t.Errorf("droppedTotal = %d, want 3", body.DroppedTotal)
	}
	if len(body.UnavailableNodes) != 1 || body.UnavailableNodes[0] != "pve2" {
		t.Errorf("unavailableNodes = %v, want [pve2]", body.UnavailableNodes)
	}
}

func TestFwLogRoute_DefaultsWhenNoQueryParams(t *testing.T) {
	svc := &fakeFwLogService{}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/log", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastLimit != defaultFwLogLimit {
		t.Errorf("limit = %d, want default %d", svc.lastLimit, defaultFwLogLimit)
	}
	if svc.lastFilter != (fwlog.Filter{}) {
		t.Errorf("filter = %+v, want zero value", svc.lastFilter)
	}

	var body struct {
		Items []fwlog.EntryView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body.Items == nil {
		t.Error("items must be an empty array, not null, when the buffer has nothing")
	}
}

func TestFwLogRoute_InvalidVMIDRejected(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: &fakeFwLogService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/log?vmid=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFwLogRoute_InvalidLimitRejected(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: &fakeFwLogService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/log?limit=-1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- GET /firewall/analytics (T-1006) ---------------------------------------

func TestFwAnalyticsRoute_NotMountedWhenServiceNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted when FwLog is nil)", rec.Code)
	}
}

func TestFwAnalyticsRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, FwLog: &fakeFwLogService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestFwAnalyticsRoute_DefaultsAndShape(t *testing.T) {
	lastSeen := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	svc := &fakeFwLogService{
		analytics: fwlog.Analytics{
			HitCounts: []fwlog.RuleHitCount{
				{Rule: fwlog.RuleRef{GuestRef: "guest:pve1:200", Origin: "guest", Pos: 0}, Hits: 3, LastSeenAt: lastSeen},
			},
			TopBlocked: fwlog.TopBlocked{
				Sources:      []fwlog.EndpointCount{{Value: "1.1.1.1", Count: 2}},
				Destinations: []fwlog.EndpointCount{{Value: "9.9.9.9", Count: 2}},
			},
			UnusedRules: []fwlog.UnusedRule{
				{Rule: fwlog.RuleRef{GuestRef: "guest:pve1:200", Origin: "guest", Pos: 1}, DaysSinceLastHit: 40},
			},
		},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !svc.analyticsCall {
		t.Fatal("Analytics was not called")
	}
	if svc.lastWindow != fwlog.DefaultAnalyticsWindow {
		t.Errorf("lastWindow = %v, want default %v", svc.lastWindow, fwlog.DefaultAnalyticsWindow)
	}

	var body fwAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.HitCounts) != 1 || body.HitCounts[0].Hits != 3 || body.HitCounts[0].Rule.GuestRef != "guest:pve1:200" {
		t.Fatalf("hitCounts = %+v", body.HitCounts)
	}
	if body.HitCounts[0].LastSeenAt != lastSeen.Unix() {
		t.Errorf("lastSeenAt = %d, want %d", body.HitCounts[0].LastSeenAt, lastSeen.Unix())
	}
	if len(body.TopBlocked.Sources) != 1 || body.TopBlocked.Sources[0].Value != "1.1.1.1" {
		t.Fatalf("topBlocked.sources = %+v", body.TopBlocked.Sources)
	}
	if len(body.UnusedRules) != 1 || body.UnusedRules[0].DaysSinceLastHit != 40 {
		t.Fatalf("unusedRules = %+v", body.UnusedRules)
	}
}

func TestFwAnalyticsRoute_WindowHoursParsed(t *testing.T) {
	svc := &fakeFwLogService{}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics?windowHours=48", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastWindow != 48*time.Hour {
		t.Errorf("lastWindow = %v, want 48h", svc.lastWindow)
	}
}

func TestFwAnalyticsRoute_InvalidWindowHoursRejected(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: &fakeFwLogService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics?windowHours=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFwAnalyticsRoute_ScopeGuestFiltersHitCountsAndUnusedRules(t *testing.T) {
	svc := &fakeFwLogService{
		analytics: fwlog.Analytics{
			HitCounts: []fwlog.RuleHitCount{
				{Rule: fwlog.RuleRef{GuestRef: "guest:pve1:200", Origin: "guest", Pos: 0}, Hits: 3},
				{Rule: fwlog.RuleRef{GuestRef: "guest:pve1:201", Origin: "guest", Pos: 0}, Hits: 5},
			},
			UnusedRules: []fwlog.UnusedRule{
				{Rule: fwlog.RuleRef{GuestRef: "guest:pve1:200", Origin: "guest", Pos: 1}, DaysSinceLastHit: 40},
				{Rule: fwlog.RuleRef{GuestRef: "guest:pve1:201", Origin: "guest", Pos: 1}, DaysSinceLastHit: 40},
			},
		},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics?scope=guest&ref=guest:pve1:200", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body fwAnalyticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.HitCounts) != 1 || body.HitCounts[0].Rule.GuestRef != "guest:pve1:200" {
		t.Fatalf("hitCounts = %+v, want only guest:pve1:200's rule", body.HitCounts)
	}
	if len(body.UnusedRules) != 1 || body.UnusedRules[0].Rule.GuestRef != "guest:pve1:200" {
		t.Fatalf("unusedRules = %+v, want only guest:pve1:200's rule", body.UnusedRules)
	}
}

func TestFwAnalyticsRoute_ScopeGuestRequiresRef(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, FwLog: &fakeFwLogService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/analytics?scope=guest", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
