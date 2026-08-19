package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/incident"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeIncidentService records what the routes asked for and returns canned
// answers. The assembly itself is tested in internal/incident; what matters
// here is the HTTP contract — status codes, the error envelope, the
// capability gate, and the export's headers.
//
//nolint:govet // fieldalignment: a test double; readability beats packing.
type fakeIncidentService struct {
	err         error
	timeline    *incident.Timeline
	export      *incident.ExportResult
	gotID       string
	inc         incident.Incident
	gotAnnotate incident.AnnotateRequest
	gotOpen     incident.OpenRequest
	closed      bool
	reopened    bool
}

func (f *fakeIncidentService) List(context.Context) ([]incident.Incident, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []incident.Incident{f.inc}, nil
}

func (f *fakeIncidentService) Open(_ context.Context, req incident.OpenRequest) (incident.Incident, error) {
	f.gotOpen = req
	if f.err != nil {
		return incident.Incident{}, f.err
	}
	return f.inc, nil
}

func (f *fakeIncidentService) Get(_ context.Context, id string) (incident.Incident, error) {
	f.gotID = id
	if f.err != nil {
		return incident.Incident{}, f.err
	}
	return f.inc, nil
}

func (f *fakeIncidentService) Timeline(_ context.Context, id string) (*incident.Timeline, error) {
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.timeline, nil
}

func (f *fakeIncidentService) Annotate(_ context.Context, id string, req incident.AnnotateRequest) (incident.Annotation, error) {
	f.gotID, f.gotAnnotate = id, req
	if f.err != nil {
		return incident.Annotation{}, f.err
	}
	return incident.Annotation{ID: "an-1", Author: req.Author, Body: req.Body, At: req.At}, nil
}

func (f *fakeIncidentService) Close(_ context.Context, id string) (incident.Incident, error) {
	f.gotID, f.closed = id, true
	if f.err != nil {
		return incident.Incident{}, f.err
	}
	return f.inc, nil
}

func (f *fakeIncidentService) Reopen(_ context.Context, id string) (incident.Incident, error) {
	f.gotID, f.reopened = id, true
	if f.err != nil {
		return incident.Incident{}, f.err
	}
	return f.inc, nil
}

func (f *fakeIncidentService) Export(_ context.Context, id string, _ incident.ExportOptions) (*incident.ExportResult, error) {
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.export, nil
}

type recordingIncidentAuditor struct{ rows []store.AuditEntry }

func (r *recordingIncidentAuditor) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	r.rows = append(r.rows, e)
	return int64(len(r.rows)), nil
}

func incidentRouter(svc IncidentService, audit incidentAuditor, authed bool) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:          fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: authed}, username: "brian@pam"},
		Topology:      fakeTopologyService{},
		Incidents:     svc,
		IncidentAudit: audit,
	})
}

func newFakeIncidentService() *fakeIncidentService {
	return &fakeIncidentService{
		inc: incident.Incident{
			ID: "inc-1", Title: "vmbr0 down", Status: "open", OpenedBy: "brian@pam",
			OpenedAt: 1000, StartedAt: 1000, Annotations: []incident.Annotation{},
		},
		timeline: &incident.Timeline{
			Incident: incident.Incident{ID: "inc-1", Title: "vmbr0 down", Status: "open"},
			Window:   incident.Window{From: 1000, To: 2000},
			Events: []incident.Event{
				{ID: "finding:1", At: 1100, Source: incident.SourceFinding, Kind: "new", Summary: "finding x new"},
			},
			Sources: []incident.SourceStatus{{Source: incident.SourceFinding, Status: incident.StatusOK, Count: 1}},
			Caveats: []string{"the point-in-time diff compared /etc/network/interfaces only"},
		},
	}
}

func TestIncidentRoutes_Unauthenticated401(t *testing.T) {
	r := incidentRouter(newFakeIncidentService(), nil, false)
	for _, path := range []string{"/api/v1/incidents", "/api/v1/incidents/inc-1", "/api/v1/incidents/inc-1/timeline"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s: status = %d, want 401", path, rec.Code)
		}
	}
}

func TestIncidentRoutes_NotMountedWithoutAService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestIncidentRoutes_OpenAnnotateCloseReopen(t *testing.T) {
	svc := newFakeIncidentService()
	r := incidentRouter(svc, nil, true)

	// Open, retroactively.
	body := `{"title":"last Tuesday","startedAt":900,"endedAt":1100}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/incidents", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /incidents: status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if svc.gotOpen.Title != "last Tuesday" || svc.gotOpen.StartedAt != 900 || svc.gotOpen.EndedAt != 1100 {
		t.Errorf("open request = %+v, want the window from the body", svc.gotOpen)
	}
	if svc.gotOpen.Actor != "brian@pam" {
		t.Errorf("open actor = %q, want the session's own username", svc.gotOpen.Actor)
	}

	// Annotate.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-1/annotations",
		strings.NewReader(`{"body":"pulled the cable","at":1045}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST annotations: status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if svc.gotAnnotate.Body != "pulled the cable" || svc.gotAnnotate.At != 1045 || svc.gotAnnotate.Author != "brian@pam" {
		t.Errorf("annotate request = %+v", svc.gotAnnotate)
	}

	// Close, then reopen.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-1/close", nil))
	if rec.Code != http.StatusOK || !svc.closed {
		t.Errorf("POST close: status = %d, closed = %v", rec.Code, svc.closed)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-1/reopen", nil))
	if rec.Code != http.StatusOK || !svc.reopened {
		t.Errorf("POST reopen: status = %d, reopened = %v", rec.Code, svc.reopened)
	}
}

func TestIncidentRoutes_TimelineShape(t *testing.T) {
	svc := newFakeIncidentService()
	r := incidentRouter(svc, nil, true)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incidents/inc-1/timeline", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	type wireEvent struct {
		Source string `json:"source"`
		At     int64  `json:"at"`
	}
	type wireSource struct {
		Source string `json:"source"`
		Status string `json:"status"`
	}
	type wireWindow struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	}
	var got struct {
		Events  []wireEvent  `json:"events"`
		Sources []wireSource `json:"sources"`
		Caveats []string     `json:"caveats"`
		Window  wireWindow   `json:"window"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Window.From != 1000 || got.Window.To != 2000 {
		t.Errorf("window = %+v", got.Window)
	}
	if len(got.Events) != 1 || got.Events[0].Source != "finding" {
		t.Errorf("events = %+v", got.Events)
	}
	if len(got.Sources) != 1 || got.Sources[0].Status != "ok" {
		t.Errorf("sources = %+v", got.Sources)
	}
	if len(got.Caveats) != 1 {
		t.Errorf("caveats = %v", got.Caveats)
	}
	if svc.gotID != "inc-1" {
		t.Errorf("the route asked for incident %q", svc.gotID)
	}
}

func TestIncidentRoutes_ErrorEnvelope(t *testing.T) {
	//nolint:govet // fieldalignment: a table-driven case struct; field order documents the case.
	cases := []struct {
		name     string
		err      error
		method   string
		path     string
		body     string
		wantCode int
		wantJSON string
	}{
		{"unknown incident", store.ErrNotFound, http.MethodGet, "/api/v1/incidents/nope", "", http.StatusNotFound, "not_found"},
		{"blank title", incident.ErrTitleRequired, http.MethodPost, "/api/v1/incidents", `{"title":""}`, http.StatusBadRequest, "validation_failed"},
		{"inverted window", incident.ErrWindowInverted, http.MethodPost, "/api/v1/incidents", `{"title":"t","startedAt":9,"endedAt":1}`, http.StatusBadRequest, "validation_failed"},
		{"already closed", incident.ErrAlreadyClosed, http.MethodPost, "/api/v1/incidents/inc-1/close", "", http.StatusConflict, "incident_closed"},
		{"already open", incident.ErrAlreadyOpen, http.MethodPost, "/api/v1/incidents/inc-1/reopen", "", http.StatusConflict, "incident_open"},
		{"export not configured", incident.ErrExportUnavailable, http.MethodGet, "/api/v1/incidents/inc-1/export", "", http.StatusServiceUnavailable, "export_unavailable"},
		{"anything else", errors.New("boom"), http.MethodGet, "/api/v1/incidents/inc-1/timeline", "", http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeIncidentService()
			svc.err = tc.err
			r := incidentRouter(svc, nil, true)

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, body))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decoding error envelope: %v", err)
			}
			if env.Error.Code != tc.wantJSON {
				t.Errorf("error code = %q, want %q", env.Error.Code, tc.wantJSON)
			}
		})
	}
}

func TestIncidentRoutes_ExportStreamsAndAudits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vnprox-incident-inc-1-20260101-000000.tar.gz")
	payload := []byte("not really a tarball, but bytes on the wire all the same")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("writing fixture artifact: %v", err)
	}

	svc := newFakeIncidentService()
	svc.export = &incident.ExportResult{
		Path: path, Filename: filepath.Base(path), Bytes: int64(len(payload)), Timeline: svc.timeline,
	}
	audit := &recordingIncidentAuditor{}
	r := incidentRouter(svc, audit, true)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incidents/inc-1/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Errorf("the artifact was not streamed verbatim")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "vnprox-incident-inc-1") {
		t.Errorf("Content-Disposition = %q, want the incident artifact's own name", cd)
	}

	// The temporary directory the export wrote into is removed once served,
	// so an artifact never accumulates on disk.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the exported artifact is still on disk after being served (%v)", err)
	}

	if len(audit.rows) != 1 || audit.rows[0].Action != "incident.export" {
		t.Fatalf("audit rows = %+v, want one incident.export row", audit.rows)
	}
	if audit.rows[0].Username != "brian@pam" || audit.rows[0].Target.String != "inc-1" {
		t.Errorf("audit row = %+v", audit.rows[0])
	}
}
