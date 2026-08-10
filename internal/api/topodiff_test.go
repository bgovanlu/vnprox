package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// stubTopologyDiffService lets each HTTP-layer case name exactly what the
// change engine returns, so this file tests the ROUTE (gating, query parsing,
// status mapping, envelope) rather than re-testing the diff — which
// internal/change/topodiff_test.go already covers against a real store.
type stubTopologyDiffService struct {
	err   error
	diff  *change.TopologyDiff
	from  string
	to    string
	calls int
}

func (s *stubTopologyDiffService) TopologyDiff(_ context.Context, from, to string) (*change.TopologyDiff, error) {
	s.calls++
	s.from, s.to = from, to
	return s.diff, s.err
}

func newTopoDiffRouter(svc TopologyDiffService, auth fakeAuthWithCaps) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, TopologyDiff: svc,
	})
}

func topoDiffAuth(caps ...string) fakeAuthWithCaps {
	set := map[string]bool{}
	for _, c := range caps {
		set[c] = true
	}
	return fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             set,
	}
}

func sampleTopoDiff() *change.TopologyDiff {
	return &change.TopologyDiff{
		From: change.DiffPoint{Requested: "snap-1", SnapshotID: "snap-1", Kind: "scheduled", At: 100},
		To:   change.DiffPoint{Requested: "now", Live: true, At: 200},
		Added: []topology.EntityDiff{{
			Ref: "bridge:pve1:vmbr9", Kind: "bridge", Node: "pve1", Name: "vmbr9",
			Change: topology.DiffAdded,
			Fields: []topology.FieldChange{{Field: "MTUDeclared", Before: "", After: "9000"}},
			Attribution: topology.DiffAttribution{
				Attributed: true, ChangesetID: "cs-1", ChangesetTitle: "add vmbr9", Actor: "alice@pve", At: 150,
			},
		}},
		Modified: []topology.EntityDiff{{
			Ref: "bridge:pve1:vmbr0", Kind: "bridge", Node: "pve1", Name: "vmbr0",
			Change: topology.DiffModified,
			Fields: []topology.FieldChange{{Field: "MTUDeclared", Before: "1500", After: "9000"}},
		}},
		Removed:      []topology.EntityDiff{},
		Coverage:     change.DiffCoverage{Nodes: []string{"pve1"}, Paths: []string{"/etc/network/interfaces"}},
		Unattributed: 1,
	}
}

func TestTopologyDiffRoute_ServesTheDiffAndSerialisesAttribution(t *testing.T) {
	stub := &stubTopologyDiffService{diff: sampleTopoDiff()}
	r := newTopoDiffRouter(stub, topoDiffAuth(capNetRead))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology/diff?from=snap-1&to=now", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if stub.from != "snap-1" || stub.to != "now" {
		t.Fatalf("service called with (%q,%q), want (snap-1,now)", stub.from, stub.to)
	}

	// Decoded generically: the JSON shape is the contract other tasks build
	// on, so it is asserted on the wire rather than through the Go types.
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	added, ok := body["added"].([]any)
	if !ok || len(added) != 1 {
		t.Fatalf("added = %v, want one entry", body["added"])
	}
	addedRow, _ := added[0].(map[string]any)
	attr, _ := addedRow["attribution"].(map[string]any)
	if attr["attributed"] != true || attr["changesetId"] != "cs-1" {
		t.Fatalf("added attribution = %v, want attributed to cs-1", attr)
	}

	modified, ok := body["modified"].([]any)
	if !ok || len(modified) != 1 {
		t.Fatalf("modified = %v, want one entry", body["modified"])
	}
	modRow, _ := modified[0].(map[string]any)
	modAttr, _ := modRow["attribution"].(map[string]any)
	// The whole product value: an out-of-band change must be VISIBLY
	// unattributed on the wire, not merely missing a changeset id.
	if v, present := modAttr["attributed"]; !present || v != false {
		t.Fatalf("an unattributed row serialised %v; `attributed: false` must always be present", modAttr)
	}
	fields, _ := modRow["fields"].([]any)
	if len(fields) != 1 {
		t.Fatalf("modified row has %d fields, want field-level before/after", len(fields))
	}
	field, _ := fields[0].(map[string]any)
	if field["before"] != "1500" || field["after"] != "9000" {
		t.Fatalf("field = %v, want before 1500 after 9000", field)
	}
	if body["unattributedCount"] != float64(1) {
		t.Fatalf("unattributedCount = %v, want 1", body["unattributedCount"])
	}
}

func TestTopologyDiffRoute_ErrorMapping(t *testing.T) {
	tests := []struct {
		err        error
		name       string
		query      string
		wantCode   string
		wantStatus int
		wantCalls  int
	}{
		{
			name:  "missing endpoints are refused before the service is consulted",
			query: "?from=snap-1", wantStatus: http.StatusBadRequest, wantCode: "validation_failed", wantCalls: 0,
		},
		{
			name:  "an uncovered range is 422 no_snapshot_in_range",
			query: "?from=1&to=now",
			err: &change.ErrNoSnapshotForPoint{
				Side: "from", Requested: "1", At: 1,
				Nearest: []change.SnapshotPoint{{SnapshotID: "snap-7", Kind: "scheduled", TakenAt: 900}},
			},
			wantStatus: http.StatusUnprocessableEntity, wantCode: "no_snapshot_in_range", wantCalls: 1,
		},
		{
			name:       "an inverted range is 400 validation_failed",
			query:      "?from=snap-2&to=snap-1",
			err:        &change.ErrDiffRangeInverted{FromAt: 200, ToAt: 100},
			wantStatus: http.StatusBadRequest, wantCode: "validation_failed", wantCalls: 1,
		},
		{
			name:       "an unknown snapshot id is 404",
			query:      "?from=nope&to=now",
			err:        store.ErrNotFound,
			wantStatus: http.StatusNotFound, wantCode: "not_found", wantCalls: 1,
		},
		{
			name:       "no snapshot store on this node is 503",
			query:      "?from=snap-1&to=now",
			err:        &change.ErrApplyNotConfigured{},
			wantStatus: http.StatusServiceUnavailable, wantCode: "apply_unavailable", wantCalls: 1,
		},
		{
			name:       "anything else is a 500 that leaks no internals",
			query:      "?from=snap-1&to=now",
			err:        errors.New("change: the database melted at /var/lib/vnprox/vnprox.db"),
			wantStatus: http.StatusInternalServerError, wantCode: "internal_error", wantCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubTopologyDiffService{err: tc.err}
			r := newTopoDiffRouter(stub, topoDiffAuth(capNetRead))

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology/diff"+tc.query, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if stub.calls != tc.wantCalls {
				t.Fatalf("service calls = %d, want %d", stub.calls, tc.wantCalls)
			}
			var env struct {
				Error struct {
					Details map[string]any `json:"details"`
					Code    string         `json:"code"`
					Message string         `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decoding error envelope: %v", err)
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if tc.wantStatus == http.StatusInternalServerError && env.Error.Message != "could not compute the topology diff" {
				t.Errorf("500 message = %q; it must not echo internal detail", env.Error.Message)
			}
			// AC4's real requirement is that the refusal NAMES the nearest
			// snapshots — an operator has to learn which range to ask for.
			if tc.wantCode == "no_snapshot_in_range" {
				nearest, _ := env.Error.Details["nearest"].([]any)
				if len(nearest) != 1 {
					t.Fatalf("details.nearest = %v, want the one available snapshot", env.Error.Details["nearest"])
				}
				row, _ := nearest[0].(map[string]any)
				if row["snapshotId"] != "snap-7" {
					t.Errorf("details.nearest[0] = %v, want snap-7", row)
				}
			}
		})
	}
}

// The route is a read of captured network configuration plus changeset
// attribution, so it carries the same netRead gate every other topology read
// does — no looser, and never anonymous.
func TestTopologyDiffRoute_RequiresSessionAndNetRead(t *testing.T) {
	t.Run("no capability is 403 and never reaches the service", func(t *testing.T) {
		stub := &stubTopologyDiffService{diff: sampleTopoDiff()}
		r := newTopoDiffRouter(stub, topoDiffAuth())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology/diff?from=a&to=now", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
		}
		if stub.calls != 0 {
			t.Fatalf("service was called %d times behind a denied capability", stub.calls)
		}
	})

	t.Run("with netRead it is served", func(t *testing.T) {
		// The control leg for the assertion above: the same stub, the same
		// request, one capability different.
		stub := &stubTopologyDiffService{diff: sampleTopoDiff()}
		r := newTopoDiffRouter(stub, topoDiffAuth(capNetRead))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology/diff?from=a&to=now", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
		}
		if stub.calls != 1 {
			t.Fatalf("service calls = %d, want 1", stub.calls)
		}
	})
}

// A nil service leaves the route unmounted rather than 500-ing, matching every
// other optional-producer route in this package.
func TestTopologyDiffRoute_NotMountedWithoutAService(t *testing.T) {
	r := newTopoDiffRouter(nil, topoDiffAuth(capNetRead))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology/diff?from=a&to=now", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("route answered 200 with no diff service wired; body: %s", rec.Body.String())
	}
}

// GET /topology must keep working: /topology/diff is a sibling static route,
// not a wildcard that could shadow it.
func TestTopologyDiffRoute_DoesNotShadowTopology(t *testing.T) {
	stub := &stubTopologyDiffService{diff: sampleTopoDiff()}
	r := newTopoDiffRouter(stub, topoDiffAuth(capNetRead))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /topology status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("GET /topology reached the diff service %d times", stub.calls)
	}
}

// Timestamps travel verbatim to the change engine, which owns the parsing —
// the route must not "helpfully" reinterpret them.
func TestTopologyDiffRoute_PassesPointSpellingsThrough(t *testing.T) {
	for _, spelling := range []string{strconv.FormatInt(1_700_000_000, 10), "2023-11-14T22:13:20Z", "01ABCDEF", "live"} {
		stub := &stubTopologyDiffService{diff: sampleTopoDiff()}
		r := newTopoDiffRouter(stub, topoDiffAuth(capNetRead))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/api/v1/topology/diff?from="+spelling+"&to=now", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("from=%q status = %d, body: %s", spelling, rec.Code, rec.Body.String())
		}
		if stub.from != spelling {
			t.Errorf("service received from=%q, want %q", stub.from, spelling)
		}
	}
}
