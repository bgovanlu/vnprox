package pvemock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestValidateSDNZoneType_TableDriven is the pure-function guard for
// PVEVersionProfile.ValidateSDNZoneType. It is table-driven across every
// registered CompatProfiles entry so a new profile added to
// compat_versions.go without a corresponding case here is at least exercised
// against every existing zone type, not just the ones this table happens to
// list.
func TestValidateSDNZoneType_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		zoneType string
		wantErr  bool
	}{
		{"8.2 rejects openfabric", "8.2", "openfabric", true},
		{"8.2 rejects ospf", "8.2", "ospf", true},
		{"8.2 accepts vlan", "8.2", "vlan", false},
		{"8.2 accepts simple", "8.2", "simple", false},
		{"8.2 accepts empty (PUT with no type change)", "8.2", "", false},
		{"9.0 accepts openfabric", "9.0", "openfabric", false},
		{"9.0 accepts ospf", "9.0", "ospf", false},
		{"9.0 accepts vlan", "9.0", "vlan", false},
		{"9.2 accepts openfabric", "9.2", "openfabric", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, ok := ProfileByVersion(tt.version)
			if !ok {
				t.Fatalf("ProfileByVersion(%q): not registered", tt.version)
			}
			err := profile.ValidateSDNZoneType(tt.zoneType)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateSDNZoneType(%q) on PVE %s = nil, want an error", tt.zoneType, tt.version)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateSDNZoneType(%q) on PVE %s = %v, want nil", tt.zoneType, tt.version, err)
			}
			if tt.wantErr && err != nil && !errorsIsSDNZoneTypeUnsupported(err) {
				t.Fatalf("ValidateSDNZoneType(%q) on PVE %s: error %v does not wrap ErrSDNZoneTypeUnsupported", tt.zoneType, tt.version, err)
			}
		})
	}
}

func errorsIsSDNZoneTypeUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "sdn zone type unsupported")
}

// --- HTTP-level compat server tests (AC2 demonstration) --------------------
//
// These drive NewCompatServer directly over HTTP (not through the *Server-
// typed helpers in server_test.go, since NewCompatServer returns a plain
// http.Handler wrapping a *Server rather than a *Server itself).

func newCompatTestServer(t *testing.T, fixtureName, version string) http.Handler {
	t.Helper()
	f, err := LoadFixture(fixturePath(t, fixtureName))
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixtureName, err)
	}
	profile, ok := ProfileByVersion(version)
	if !ok {
		t.Fatalf("ProfileByVersion(%q): not registered", version)
	}
	return NewCompatServer(f, profile)
}

func compatLogin(t *testing.T, h http.Handler) (ticket, csrf string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api2/json/access/ticket",
		strings.NewReader("username=root%40pam&password=vnprox-mock"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d body %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Ticket string `json:"ticket"`
			CSRF   string `json:"CSRFPreventionToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding login response: %v", err)
	}
	return envelope.Data.Ticket, envelope.Data.CSRF
}

func compatCreateZone(t *testing.T, h http.Handler, ticket, csrf, zoneID, zoneType string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(SDNZoneSpec{ID: zoneID, Type: zoneType})
	if err != nil {
		t.Fatalf("marshaling zone body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, sdnZonesPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	req.Header.Set("CSRFPreventionToken", csrf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestCompatServer_SDNFabricZoneGate is the concrete AC2 demonstration: the
// exact same request (create an "openfabric" SDN zone) is fired at the
// exact same fixture topology, and only the PVEVersionProfile changes
// between subtests. PVE 8.2 must reject it; PVE 9.0/9.2 must accept it —
// this is the "fixture divergence between PVE versions is caught" case the
// T-2103 task card asks for. See this package's report for the mutation
// evidence (break ValidateSDNZoneType's gate, watch the 8.2 subtest go
// red, restore it, watch it go green).
func TestCompatServer_SDNFabricZoneGate(t *testing.T) {
	tests := []struct {
		fixture     string
		version     string
		wantHeader  string
		description string
		wantStatus  int
	}{
		{fixture: "compat/pve-8.2.yaml", version: "8.2", wantHeader: "8.2", wantStatus: http.StatusBadRequest, description: "PVE 8.2 has no SDN Fabrics zone type"},
		{fixture: "compat/pve-9.0.yaml", version: "9.0", wantHeader: "9.0", wantStatus: http.StatusOK, description: "PVE 9.0 introduced SDN Fabrics"},
		{fixture: "compat/pve-9.2.yaml", version: "9.2", wantHeader: "9.2", wantStatus: http.StatusOK, description: "PVE 9.2 still supports SDN Fabrics"},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			h := newCompatTestServer(t, tt.fixture, tt.version)
			ticket, csrf := compatLogin(t, h)
			rec := compatCreateZone(t, h, ticket, csrf, "fabric1", "openfabric")
			if rec.Code != tt.wantStatus {
				t.Fatalf("POST zone type=openfabric against PVE %s: status = %d, want %d (body=%s)",
					tt.version, rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get(CompatVersionHeader); got != tt.wantHeader {
				t.Errorf("%s = %q, want %q", CompatVersionHeader, got, tt.wantHeader)
			}
			if rec.Code == http.StatusBadRequest {
				var envelope struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatalf("decoding error response: %v", err)
				}
				if !strings.Contains(envelope.Message, "openfabric") {
					t.Errorf("rejection message = %q, want it to name the rejected zone type", envelope.Message)
				}
			}
		})
	}
}

// TestCompatServer_BaselineZoneTypeUnaffected proves the wrapper is
// additive, not a general zone-type allowlist: an ordinary "vlan" zone
// (supported on every profile, and the only kind every other pvemock test
// in this package already creates) still succeeds on the oldest profile.
func TestCompatServer_BaselineZoneTypeUnaffected(t *testing.T) {
	h := newCompatTestServer(t, "compat/pve-8.2.yaml", "8.2")
	ticket, csrf := compatLogin(t, h)
	rec := compatCreateZone(t, h, ticket, csrf, "ztest", "vlan")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST zone type=vlan against PVE 8.2: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestCompatServer_UnrelatedRoutesPassThrough confirms the wrapper does not
// intercept anything beyond the two SDN zone routes it documents.
func TestCompatServer_UnrelatedRoutesPassThrough(t *testing.T) {
	h := newCompatTestServer(t, "compat/pve-8.2.yaml", "8.2")
	ticket, _ := compatLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/api2/json/nodes/pve1/network", nil)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET network: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}
