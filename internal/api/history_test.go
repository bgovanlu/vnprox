// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

type fakeHistoryAuditSource struct {
	rows       []store.AuditEntry
	err        error
	gotActions []string
	gotFrom    int64
	gotTo      int64
}

func (f *fakeHistoryAuditSource) ListActionsInRange(_ context.Context, actions []string, from, to int64) ([]store.AuditEntry, error) {
	f.gotActions = actions
	f.gotFrom, f.gotTo = from, to
	return f.rows, f.err
}

type fakeHistoryFindingEventsSource struct {
	err     error
	rows    []store.FindingEvent
	gotFrom int64
	gotTo   int64
}

func (f *fakeHistoryFindingEventsSource) ListByTimeRange(_ context.Context, from, to int64) ([]store.FindingEvent, error) {
	f.gotFrom, f.gotTo = from, to
	return f.rows, f.err
}

func TestHistoryEventsRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
		History: &fakeHistoryAuditSource{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/events", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHistoryEventsRoute_NotMountedWhenBothSourcesNil(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/events", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestHistoryEventsRoute_MergesAndSortsBothKinds(t *testing.T) {
	audit := &fakeHistoryAuditSource{rows: []store.AuditEntry{
		{At: 2000, Action: "changeset.confirm", Result: "committed", ChangesetID: sql.NullString{String: "cs-1", Valid: true}},
	}}
	findingEvents := &fakeHistoryFindingEventsSource{rows: []store.FindingEvent{
		{At: 1000, FindingID: "f1", Transition: "new"},
		{At: 3000, FindingID: "f1", Transition: "resolved"},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		History: audit, HistoryFindingEvents: findingEvents,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/events?fromTs=500&toTs=4000", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	if audit.gotFrom != 500 || audit.gotTo != 4000 {
		t.Errorf("audit source got from/to = %d/%d, want 500/4000", audit.gotFrom, audit.gotTo)
	}
	if findingEvents.gotFrom != 500 || findingEvents.gotTo != 4000 {
		t.Errorf("finding events source got from/to = %d/%d, want 500/4000", findingEvents.gotFrom, findingEvents.gotTo)
	}
	wantActions := 6 // len(store.ChangesetLifecycleActions)
	if len(audit.gotActions) != wantActions {
		t.Errorf("audit source got %d lifecycle actions, want %d", len(audit.gotActions), wantActions)
	}

	var body historyEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("items = %+v, want 3", body.Items)
	}
	// Ascending by at, regardless of kind.
	if body.Items[0].At != 1000 || body.Items[0].Kind != "finding" || body.Items[0].Transition != "new" {
		t.Errorf("items[0] = %+v, want finding@1000 new", body.Items[0])
	}
	if body.Items[1].At != 2000 || body.Items[1].Kind != "changeset" || body.Items[1].ChangesetID != "cs-1" {
		t.Errorf("items[1] = %+v, want changeset@2000 cs-1", body.Items[1])
	}
	if body.Items[2].At != 3000 || body.Items[2].Kind != "finding" || body.Items[2].Transition != "resolved" {
		t.Errorf("items[2] = %+v, want finding@3000 resolved", body.Items[2])
	}
}

// TestHistoryEventsRoute_ServiceClassOnFindingEntries covers T-1504 AC2's
// GET /history/events half: a service_traffic_on_wrong_network finding's
// timeline entry carries its serviceClass (parsed from the finding's own
// id); every other finding's entry omits the field.
func TestHistoryEventsRoute_ServiceClassOnFindingEntries(t *testing.T) {
	findingEvents := &fakeHistoryFindingEventsSource{rows: []store.FindingEvent{
		{At: 1000, FindingID: "flow:service_traffic_on_wrong_network|ceph-public|vlan20", Transition: "new"},
		{At: 2000, FindingID: "health:corosync_link_degraded|pve1|ring0", Transition: "new"},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		HistoryFindingEvents: findingEvents,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/events", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body historyEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2", body.Items)
	}
	if body.Items[0].ServiceClass != "ceph-public" {
		t.Errorf("items[0].ServiceClass = %q, want ceph-public", body.Items[0].ServiceClass)
	}
	if body.Items[1].ServiceClass != "" {
		t.Errorf("items[1].ServiceClass = %q, want empty (not a service_traffic_on_wrong_network finding)", body.Items[1].ServiceClass)
	}
}

func TestHistoryEventsRoute_OneSourceNilStillWorks(t *testing.T) {
	findingEvents := &fakeHistoryFindingEventsSource{rows: []store.FindingEvent{
		{At: 1000, FindingID: "f1", Transition: "new"},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
		HistoryFindingEvents: findingEvents,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/history/events", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body historyEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Kind != "finding" {
		t.Errorf("items = %+v, want just the one finding event", body.Items)
	}
}
