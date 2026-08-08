package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// recordingHistory captures the ref the handler parsed, so a test can assert
// what actually reached the service rather than only what came back.
type recordingHistory struct {
	err      error
	gotRef   inventory.Ref
	entries  []change.EntityHistoryEntry
	gotLimit int
}

func (h *recordingHistory) EntityHistory(_ context.Context, ref inventory.Ref, limit int) ([]change.EntityHistoryEntry, bool, error) {
	h.gotRef = ref
	h.gotLimit = limit
	return h.entries, false, h.err
}

func historyRouter(t *testing.T, svc EntityHistoryService, caps map[string]bool) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(caps), Topology: fakeTopologyService{},
		EntityHistory: svc,
	})
}

func getHistory(t *testing.T, r http.Handler, query string) (*httptest.ResponseRecorder, entityHistoryResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/history?"+query, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body entityHistoryResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, body
}

func TestEntityHistoryRoute_ReturnsTheServicesEntries(t *testing.T) {
	svc := &recordingHistory{entries: []change.EntityHistoryEntry{
		{Kind: change.HistoryKindChangeset, At: 200, Actor: "alice", Summary: "bridge.update in widen vmbr0"},
		{Kind: change.HistoryKindAudit, At: 100, Actor: "brian", Summary: "changeset.apply", Result: "ok"},
	}}
	r := historyRouter(t, svc, map[string]bool{"audit": true})

	rec, body := getHistory(t, r, "ref=bridge:pve1:vmbr0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	if svc.gotRef != (inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}) {
		t.Fatalf("the handler parsed %+v", svc.gotRef)
	}
}

// AC2, in the form the guest-interior defect took: a ref containing "/" and
// ":" must survive the round trip. That defect (T-1304) made a whole feature
// return 400 to every browser request for months because one handler did not
// decode its ref.
//
// This route carries the ref as a query parameter precisely so the decoding is
// net/url's job rather than a handler's, and this test pins both the plain and
// the percent-encoded forms.
func TestEntityHistoryRoute_HandlesRefsContainingSlashesAndColons(t *testing.T) {
	want := inventory.Ref{Kind: inventory.KindSDNVnet, Node: "", ID: "zone1/vnet1"}
	for _, name := range []string{"plain", "encoded"} {
		t.Run(name, func(t *testing.T) {
			svc := &recordingHistory{}
			r := historyRouter(t, svc, map[string]bool{"audit": true})

			raw := "sdn-vnet::zone1/vnet1"
			q := "ref=" + raw
			if name == "encoded" {
				q = "ref=" + url.QueryEscape(raw)
			}
			rec, _ := getHistory(t, r, q)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			if svc.gotRef != want {
				t.Fatalf("the handler parsed %+v, want %+v", svc.gotRef, want)
			}
		})
	}
}

// AC4: the same capability as /audit. A caller without it gets 403, NOT an
// empty list — re-slicing the audit trail by entity must not widen who can
// read it, and an empty list would read as "nothing ever touched this".
func TestEntityHistoryRoute_RequiresTheAuditCapability(t *testing.T) {
	svc := &recordingHistory{entries: []change.EntityHistoryEntry{{Kind: change.HistoryKindAudit, At: 1, Summary: "x"}}}
	r := historyRouter(t, svc, map[string]bool{"netRead": true}) // no audit cap

	rec, _ := getHistory(t, r, "ref=bridge:pve1:vmbr0")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// And the service must not have been consulted at all.
	if !svc.gotRef.IsZero() {
		t.Fatal("the handler reached the service despite the missing capability")
	}
}

func TestEntityHistoryRoute_ValidatesItsInput(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{name: "no ref", query: ""},
		{name: "blank ref", query: "ref=%20%20"},
		{name: "unparseable ref", query: "ref=not-a-ref"},
		{name: "zero limit", query: "ref=bridge:pve1:vmbr0&limit=0"},
		{name: "negative limit", query: "ref=bridge:pve1:vmbr0&limit=-3"},
		{name: "non-numeric limit", query: "ref=bridge:pve1:vmbr0&limit=lots"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := historyRouter(t, &recordingHistory{}, map[string]bool{"audit": true})
			rec, _ := getHistory(t, r, tc.query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// The static /inventory/history segment must win over the /inventory/*
// wildcard, or this route would never be reached. chi resolves static ahead of
// wildcard, but that is a property worth pinning rather than assuming: the
// whole path shape was chosen because of it.
func TestEntityHistoryRoute_WinsOverTheInventoryWildcard(t *testing.T) {
	svc := &recordingHistory{}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"audit": true, "netRead": true}),
		// Both routes mounted, as in production.
		Topology:      fakeTopologyService{},
		EntityHistory: svc,
	})
	rec, _ := getHistory(t, r, "ref=bridge:pve1:vmbr0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d — /inventory/history was swallowed by /inventory/*", rec.Code)
	}
	if svc.gotRef.IsZero() {
		t.Fatal("the history handler never ran; the wildcard took the request")
	}
}

func TestEntityHistoryRoute_NotMountedWithoutAService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"audit": true, "netRead": true}),
		// No EntityHistory. The wildcard handles it and 404s on the ref;
		// what matters is that nothing 500s.
		Topology: fakeTopologyService{},
	})
	rec, _ := getHistory(t, r, "ref=bridge:pve1:vmbr0")
	if rec.Code >= 500 {
		t.Fatalf("status = %d with no history service wired", rec.Code)
	}
}
