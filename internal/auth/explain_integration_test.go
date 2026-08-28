// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// TestIntegration_ExplainAgainstRestrictedPrincipal is T-4105's "test with
// a genuinely restricted principal, not just an admin" requirement, run
// end-to-end against a real pvemock login: three-node-vlan.yaml's
// auditor@pve holds only Sys.Audit/VM.Audit/SDN.Audit — read-only, no
// Sys.Modify, no SDN.Allocate, no VM.Config.Network, no Sys.Console
// (the same fixture user TestIntegration_CapabilityMatrixAgainstMock
// already drives through the genuine GET /access/permissions +
// GET /cluster/status path). It logs in for real, then hits a
// RequireCap(netWrite)-gated route and checks the 403 body's
// `error.details.explanation` names Sys.Modify at /nodes/{node} — proving
// the wiring in middleware.go's RequireCap, not just the pure Explain
// unit tests in explain_test.go.
func TestIntegration_ExplainAgainstRestrictedPrincipal(t *testing.T) {
	ts, _ := newMockServer(t, fixtureThreeNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		svc.MountRoutes(r)
		// A stand-in for any real RequireCap(netWrite)-gated route (e.g.
		// PUT /nodes/{node}/network/{iface}) — GET so CSRF (irrelevant to
		// this card) never enters the picture.
		r.With(svc.SessionMiddleware, svc.RequireCap(auth.CapNetWrite)).
			Get("/nodes/{node}/protected", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
	})
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{"username": "auditor", "password": "readonly", "realm": "pve"})
	loginResp, err := client.Post(daemon.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}
	_ = loginResp.Body.Close()

	resp, err := client.Get(daemon.URL + "/api/v1/nodes/pve1/protected")
	if err != nil {
		t.Fatalf("GET /nodes/pve1/protected: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (auditor holds no Sys.Modify)", resp.StatusCode)
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Explanation auth.Explanation `json:"explanation"`
			} `json:"details"`
		} `json:"error"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&envelope); decodeErr != nil {
		t.Fatalf("decoding error envelope: %v", decodeErr)
	}
	if envelope.Error.Code != "forbidden" {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, "forbidden")
	}

	exp := envelope.Error.Details.Explanation
	if exp.Capability != "netWrite" {
		t.Errorf("explanation.capability = %q, want %q", exp.Capability, "netWrite")
	}
	if exp.Granted {
		t.Error("explanation.granted = true, want false (auditor has no Sys.Modify)")
	}
	want := []auth.PrivilegeRequirement{{Privilege: "Sys.Modify", Path: "/nodes/pve1", Confirmed: true}}
	if len(exp.Missing) != 1 || exp.Missing[0] != want[0] {
		t.Errorf("explanation.missing = %+v, want %+v", exp.Missing, want)
	}
	if exp.Reason != "" {
		t.Errorf("explanation.reason = %q, want empty (netWrite is privilege-derived for an ordinary PVE session)", exp.Reason)
	}
}

// TestIntegration_ExplainDoesNotFireOnPermittedAction: the auditor DOES
// hold netRead (Sys.Audit) — the permitted-action case must be a plain
// 200 with no explanation payload at all, not a denial in disguise.
func TestIntegration_ExplainDoesNotFireOnPermittedAction(t *testing.T) {
	ts, _ := newMockServer(t, fixtureThreeNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		svc.MountRoutes(r)
		r.With(svc.SessionMiddleware, svc.RequireCap(auth.CapNetRead)).
			Get("/nodes/{node}/protected-read", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
	})
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{"username": "auditor", "password": "readonly", "realm": "pve"})
	loginResp, err := client.Post(daemon.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	_ = loginResp.Body.Close()

	resp, err := client.Get(daemon.URL + "/api/v1/nodes/pve1/protected-read")
	if err != nil {
		t.Fatalf("GET /nodes/pve1/protected-read: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (auditor holds Sys.Audit)", resp.StatusCode)
	}
}
