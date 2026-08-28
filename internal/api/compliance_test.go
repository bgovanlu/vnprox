// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/compliance"
	"github.com/bgovanlu/vnprox/internal/docexport"
)

// compliance_test.go proves T-2706's contract at the HTTP boundary: the
// safety property survives serialization (AC2 for the JSON surface the UI
// and any integrator actually read), and an out-of-window as-of request is
// refused with the earliest available date rather than answered with a
// thinner report (AC5).

//nolint:govet // fieldalignment: a test fake; a legible field list beats packing sixteen bytes.
type fakeComplianceService struct {
	report   compliance.Report
	err      error
	profiles []compliance.ProfileSummary
	// lastAsOf records what the route parsed, so the query-parameter
	// contract is asserted rather than assumed.
	lastAsOf *time.Time
}

func (f *fakeComplianceService) ListProfiles() []compliance.ProfileSummary { return f.profiles }

func (f *fakeComplianceService) Report(_ context.Context, profileID string, asOf time.Time) (compliance.Report, error) {
	f.lastAsOf = &asOf
	if f.err != nil {
		return compliance.Report{}, f.err
	}
	rep := f.report
	rep.ProfileID = profileID
	return rep, nil
}

func sampleAPIReport() compliance.Report {
	return compliance.Report{
		ProductVersion: "test", ProfileID: "general-network-hygiene", ProfileTitle: "General network hygiene",
		ProfileVersion: "1.0.0", Notice: "This report is not a certification.",
		GeneratedAt: 1_700_000_000, CheckUniverse: "test catalog",
		Summary: compliance.Summary{Pass: 1, Unmapped: 1, Total: 2},
		Controls: []compliance.ControlResult{
			{ID: "P1", Title: "Passes", Statement: "s", Stat: compliance.StatusPass,
				Evidence: []compliance.EvidenceResult{{Kind: compliance.EvidenceCheck, Name: "mgmt_single_path", Stat: compliance.EvidenceSatisfied, Detail: "clean"}}},
			{ID: "U1", Title: "Unmapped", Statement: "s", Stat: compliance.StatusUnmapped, UnmappedReason: "vnprox observes none of this"},
		},
		UnmappedChecks: []string{"wan_degraded"},
	}
}

func newComplianceTestRouter(svc ComplianceService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Compliance: svc,
	})
}

func doGet(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestComplianceRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	if rec := doGet(t, r, "/api/v1/compliance"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}

func TestComplianceRoutes_RequireNetRead(t *testing.T) {
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: false},
	}
	r := newComplianceTestRouter(&fakeComplianceService{report: sampleAPIReport()}, auth)
	for _, path := range []string{
		"/api/v1/compliance",
		"/api/v1/compliance/general-network-hygiene",
		"/api/v1/export/compliance/general-network-hygiene?format=md",
	} {
		if rec := doGet(t, r, path); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s status = %d, want 403", path, rec.Code)
		}
	}
}

// TestComplianceRoute_UnmappedControlSerializesAsUnmapped is AC2 at the
// wire: the JSON a client actually reads must carry `unmapped`, and must not
// attribute evidence to a control that has none.
func TestComplianceRoute_UnmappedControlSerializesAsUnmapped(t *testing.T) {
	r := newComplianceTestRouter(&fakeComplianceService{report: sampleAPIReport()}, fullCapsAuth("alice"))
	rec := doGet(t, r, "/api/v1/compliance/general-network-hygiene")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var got compliance.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, c := range got.Controls {
		if c.ID != "U1" {
			continue
		}
		found = true
		if c.Stat != compliance.StatusUnmapped || c.Stat.IsPassing() {
			t.Errorf("unmapped control serialized as %q", c.Stat)
		}
		if len(c.Evidence) != 0 {
			t.Errorf("unmapped control carries evidence %+v", c.Evidence)
		}
		if c.UnmappedReason == "" {
			t.Error("unmapped control carries no stated reason")
		}
	}
	if !found {
		t.Error("the unmapped control did not survive serialization")
	}
	if got.Notice == "" {
		t.Error("the response dropped the profile's no-certification notice")
	}
}

// TestComplianceExport_EveryRegisteredFormatIsServed drives the export route
// from docexport's own registry, so a format added there is exercised here
// without editing this test.
func TestComplianceExport_EveryRegisteredFormatIsServed(t *testing.T) {
	r := newComplianceTestRouter(&fakeComplianceService{report: sampleAPIReport()}, fullCapsAuth("alice"))
	for _, renderer := range docexport.ComplianceRenderers() {
		rec := doGet(t, r, "/api/v1/export/compliance/general-network-hygiene?format="+renderer.Format)
		if rec.Code != http.StatusOK {
			t.Fatalf("format %s: status = %d, body: %s", renderer.Format, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != renderer.ContentType {
			t.Errorf("format %s: Content-Type = %q, want %q", renderer.Format, ct, renderer.ContentType)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "vnprox-compliance-general-network-hygiene-") ||
			!strings.HasSuffix(cd, "."+renderer.Extension+`"`) {
			t.Errorf("format %s: Content-Disposition = %q", renderer.Format, cd)
		}
		digest, err := renderer.Parse(rec.Body.String())
		if err != nil {
			t.Fatalf("format %s: the served document does not parse: %v", renderer.Format, err)
		}
		for _, c := range digest.Controls {
			if c.ID == "U1" && c.Status.IsPassing() {
				t.Errorf("format %s served the unmapped control as passing (%q)", renderer.Format, c.Status)
			}
		}
	}

	if rec := doGet(t, r, "/api/v1/export/compliance/general-network-hygiene?format=pdf"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown format status = %d, want 400", rec.Code)
	}
}

// TestComplianceRoute_OutsideRetentionNamesTheEarliestDate is AC5 at the
// wire: a stated error carrying the earliest available date, never a 200
// with a thinner report.
func TestComplianceRoute_OutsideRetentionNamesTheEarliestDate(t *testing.T) {
	earliest := time.Unix(1_700_000_000, 0)
	svc := &fakeComplianceService{err: &compliance.ErrOutsideRetention{
		Requested: time.Unix(1_600_000_000, 0), Earliest: earliest, HasEarliest: true,
	}}
	r := newComplianceTestRouter(svc, fullCapsAuth("alice"))

	for _, path := range []string{
		"/api/v1/compliance/general-network-hygiene?asOf=1600000000",
		"/api/v1/export/compliance/general-network-hygiene?format=md&asOf=1600000000",
	} {
		rec := doGet(t, r, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400, body: %s", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Error struct {
				Details map[string]any `json:"details"`
				Code    string         `json:"code"`
				Message string         `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Error.Code != "outside_retention_window" {
			t.Errorf("error code = %q", env.Error.Code)
		}
		want := earliest.UTC().Format(time.RFC3339)
		if !strings.Contains(env.Error.Message, want) {
			t.Errorf("message %q does not name the earliest available date %q", env.Error.Message, want)
		}
		if env.Error.Details["earliestAvailable"] != want {
			t.Errorf("details.earliestAvailable = %v, want %q", env.Error.Details["earliestAvailable"], want)
		}
		// The refusal must not smuggle a report through alongside the error.
		if strings.Contains(rec.Body.String(), `"controls"`) {
			t.Errorf("a refused as-of response carried a report body: %s", rec.Body.String())
		}
	}
}

func TestComplianceRoute_AsOfParsing(t *testing.T) {
	tests := []struct {
		query    string
		wantCode int
		wantUnix int64
	}{
		{query: "", wantCode: http.StatusOK, wantUnix: 0},
		{query: "?asOf=1700000000", wantCode: http.StatusOK, wantUnix: 1_700_000_000},
		{query: "?asOf=2023-11-14T22:13:20Z", wantCode: http.StatusOK, wantUnix: 1_700_000_000},
		{query: "?asOf=yesterday", wantCode: http.StatusBadRequest},
		{query: "?asOf=-5", wantCode: http.StatusBadRequest},
	}
	for _, tc := range tests {
		svc := &fakeComplianceService{report: sampleAPIReport()}
		r := newComplianceTestRouter(svc, fullCapsAuth("alice"))
		rec := doGet(t, r, "/api/v1/compliance/general-network-hygiene"+tc.query)
		if rec.Code != tc.wantCode {
			t.Errorf("asOf=%q: status = %d, want %d (body: %s)", tc.query, rec.Code, tc.wantCode, rec.Body.String())
			continue
		}
		if tc.wantCode != http.StatusOK {
			continue
		}
		if svc.lastAsOf == nil {
			t.Fatalf("asOf=%q: the service was never called", tc.query)
		}
		got := int64(0)
		if !svc.lastAsOf.IsZero() {
			got = svc.lastAsOf.Unix()
		}
		if got != tc.wantUnix {
			t.Errorf("asOf=%q parsed to %d, want %d", tc.query, got, tc.wantUnix)
		}
	}
}

func TestComplianceRoute_UnknownProfileNamesWhatExists(t *testing.T) {
	svc := &fakeComplianceService{err: &compliance.ErrUnknownProfile{ID: "nope", Available: []string{"general-network-hygiene"}}}
	r := newComplianceTestRouter(svc, fullCapsAuth("alice"))
	rec := doGet(t, r, "/api/v1/compliance/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "general-network-hygiene") {
		t.Errorf("404 body does not name the installed profiles: %s", rec.Body.String())
	}
}

func TestComplianceRoute_ListsProfiles(t *testing.T) {
	svc := &fakeComplianceService{profiles: []compliance.ProfileSummary{
		{ID: "general-network-hygiene", Title: "General network hygiene", Version: "1.0.0",
			Notice: "not a certification", ControlCount: 14, MappedChecks: 30, UnmappedCount: 2},
	}}
	r := newComplianceTestRouter(svc, fullCapsAuth("alice"))
	rec := doGet(t, r, "/api/v1/compliance")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Items []compliance.ProfileSummary `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Items) != 1 || env.Items[0].ID != "general-network-hygiene" || env.Items[0].Notice == "" {
		t.Errorf("items = %+v", env.Items)
	}
}
