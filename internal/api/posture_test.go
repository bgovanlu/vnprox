// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// fakePostureService is a canned PostureService for exercising the posture
// routes without a store.
type fakePostureService struct {
	err    error
	latest posture.Posture
	ok     bool
}

func (f fakePostureService) Latest(_ context.Context) (posture.Posture, bool, error) {
	return f.latest, f.ok, f.err
}

func (f fakePostureService) History(_ context.Context, _ int) ([]posture.Posture, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.ok {
		return nil, nil
	}
	return []posture.Posture{f.latest}, nil
}

func samplePosture() posture.Posture {
	return posture.Posture{
		Overall:    70,
		Qualified:  true,
		ComputedAt: 1_700_000_000,
		Factors: []posture.Factor{
			{Name: posture.FactorSPOF, Weight: 30, ScorePct: 90, Evaluated: true},
			{Name: posture.FactorAnomalyRate, Weight: 15, ScorePct: posture.NotEvaluatedScore, Evaluated: false, Caveat: "cold start"},
		},
	}
}

func newPostureTestRouter(svc PostureService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Posture: svc,
	})
}

func TestPostureRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posture", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}

func TestPostureRoutes_RequiresNetRead(t *testing.T) {
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: false},
	}
	r := newPostureTestRouter(fakePostureService{ok: true, latest: samplePosture()}, auth)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posture", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestPostureRoutes_LatestAndNotFound(t *testing.T) {
	// No computation yet ⇒ 404.
	r := newPostureTestRouter(fakePostureService{ok: false}, fullCapsAuth("alice"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posture", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no score yet)", rec.Code)
	}

	// A computed score ⇒ 200 with named factors.
	r = newPostureTestRouter(fakePostureService{ok: true, latest: samplePosture()}, fullCapsAuth("alice"))
	req = httptest.NewRequest(http.MethodGet, "/api/v1/posture", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var got posture.Posture
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Overall != 70 || !got.Qualified || len(got.Factors) != 2 {
		t.Errorf("decoded posture = %+v, want overall 70, qualified, 2 factors", got)
	}
}

func TestPostureRoutes_History(t *testing.T) {
	r := newPostureTestRouter(fakePostureService{ok: true, latest: samplePosture()}, fullCapsAuth("alice"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/posture/history?limit=10", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Items []posture.Posture `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Items) != 1 {
		t.Errorf("history items = %d, want 1", len(env.Items))
	}
}

func TestPostureRoutes_Export(t *testing.T) {
	r := newPostureTestRouter(fakePostureService{ok: true, latest: samplePosture()}, fullCapsAuth("alice"))

	for _, tc := range []struct {
		format, wantCT, wantSuffix string
	}{
		{"md", "text/markdown", `.md"`},
		{"html", "text/html", `.html"`},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/export/posture?format="+tc.format, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("format=%s: status = %d, want 200, body: %s", tc.format, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tc.wantCT) {
			t.Errorf("format=%s: Content-Type = %q, want %s", tc.format, ct, tc.wantCT)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasSuffix(cd, tc.wantSuffix) {
			t.Errorf("format=%s: Content-Disposition = %q, want suffix %s", tc.format, cd, tc.wantSuffix)
		}
		if !strings.Contains(rec.Body.String(), "Overall: 70 / 100") {
			t.Errorf("format=%s: body missing score line", tc.format)
		}
	}

	// Invalid format ⇒ 400.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/posture?format=pdf", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("format=pdf: status = %d, want 400", rec.Code)
	}
}

func TestPostureRoutes_ExportNotFound(t *testing.T) {
	r := newPostureTestRouter(fakePostureService{ok: false}, fullCapsAuth("alice"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/posture?format=md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no score to export)", rec.Code)
	}
}
