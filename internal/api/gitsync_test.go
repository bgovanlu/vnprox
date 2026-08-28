// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

type stubGitSyncStatus struct{ st gitsync.Status }

func (s stubGitSyncStatus) Status() gitsync.Status { return s.st }

func gitSyncRouter(svc GitSyncStatusService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, GitSync: svc,
	})
}

// TestGitSyncStatus_ReportsTheDaemonsState covers the happy path and the
// unconfigured one. The second case is the load-bearing half: the route is
// mounted whether or not the subsystem exists, because "gitsync is off" is
// the answer `vnproxctl gitsync status` needs, and because a route that
// disappears with configuration falls outside T-2405's completeness gate.
func TestGitSyncStatus_ReportsTheDaemonsState(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name        string
		svc         GitSyncStatusService
		wantEnabled bool
		wantSHA     string
	}{
		{
			name: "a running sync reports its last fetch and open draft",
			svc: stubGitSyncStatus{st: gitsync.Status{
				Enabled: true, Remote: "https://github.com/org/infra (github)",
				LastFetchedSHA: "abc123", OpenChangesetID: "01JDRAFT",
			}},
			wantEnabled: true, wantSHA: "abc123",
		},
		{name: "no service wired at all still answers", svc: nil},
		{name: "a disabled service answers enabled:false", svc: stubGitSyncStatus{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := gitSyncRouter(tc.svc, fullCapsAuth("alice"))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/gitsync/status", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			var got gitsync.Status
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not a gitsync.Status: %v (%s)", err, rec.Body.String())
			}
			if got.Enabled != tc.wantEnabled {
				t.Errorf("enabled = %v, want %v", got.Enabled, tc.wantEnabled)
			}
			if got.LastFetchedSHA != tc.wantSHA {
				t.Errorf("lastFetchedSha = %q, want %q", got.LastFetchedSHA, tc.wantSHA)
			}
		})
	}
}

// TestGitSyncStatus_RequiresASession: it is an ordinary netRead-gated read,
// with no special case for being "just status".
func TestGitSyncStatus_RequiresASession(t *testing.T) {
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := gitSyncRouter(stubGitSyncStatus{st: gitsync.Status{Enabled: true}}, auth)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/gitsync/status", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("GET /gitsync/status without a session = 200")
	}
}

// TestGitSyncHasNoMutatingRoute is the API half of the stage-only invariant:
// no verb here triggers a sync or applies its draft. A future edit adding one
// fails this test rather than shipping quietly.
func TestGitSyncHasNoMutatingRoute(t *testing.T) {
	r := gitSyncRouter(stubGitSyncStatus{st: gitsync.Status{Enabled: true}}, fullCapsAuth("alice"))
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/gitsync/sync"},
		{http.MethodPost, "/api/v1/gitsync/apply"},
		{http.MethodPost, "/api/v1/gitsync/status"},
		{http.MethodDelete, "/api/v1/gitsync/status"},
		{http.MethodPut, "/api/v1/gitsync/status"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(probe.method, probe.path, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s %s is served; the gitsync API surface is exactly one read", probe.method, probe.path)
		}
	}
}
