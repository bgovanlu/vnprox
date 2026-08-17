package pvemock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSupportsSDNFabricsAPI_TableDriven is the pure-function guard for
// PVEVersionProfile.SupportsSDNFabricsAPI. It is table-driven across every
// registered CompatProfiles entry so a new profile added to
// compat_versions.go without a corresponding case here is still asserted
// to make a definite claim about the fabrics family.
//
// It replaced TestValidateSDNZoneType_TableDriven, which asserted that PVE
// 9 added "openfabric"/"ospf" as SDN zone types. That was false — real PVE
// 9.2.4 offers <evpn | faucet | qinq | simple | vlan | vxlan> and rejects
// "openfabric" exactly as 8.2 does. The old test passed on every commit
// because both sides of it were written from the same wrong premise.
func TestSupportsSDNFabricsAPI_TableDriven(t *testing.T) {
	want := map[string]bool{"8.2": false, "9.0": true, "9.2": true}
	for _, p := range CompatProfiles {
		t.Run("PVE "+p.Version, func(t *testing.T) {
			w, ok := want[p.Version]
			if !ok {
				t.Fatalf("profile %q is registered in CompatProfiles but this test states no expectation for it; "+
					"add one rather than letting a new profile go unasserted", p.Version)
			}
			if got := p.SupportsSDNFabricsAPI(); got != w {
				t.Fatalf("PVE %s SupportsSDNFabricsAPI() = %v, want %v", p.Version, got, w)
			}
		})
	}
}

// TestNoProfileClaimsFabricZoneTypes is a regression guard against the
// specific wrong idea this package used to hold. Zone types and fabric
// protocols are different namespaces; nothing in pvemock may gate a zone
// type named "openfabric" or "ospf" again without new hardware evidence
// that contradicts planning/reports/evidence/pve-9.2.4-sdn-schema.txt.
func TestNoProfileClaimsFabricZoneTypes(t *testing.T) {
	h := newCompatTestServer(t, "compat/pve-8.2.yaml", "8.2")
	ticket, csrf := compatLogin(t, h)
	rec := compatCreateZone(t, h, ticket, csrf, "fabric1", "openfabric")
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("PVE 8.2 rejected zone type \"openfabric\" with 400: pvemock has reintroduced a zone-type " +
			"version gate. Real 8.2 and real 9.2 both reject that zone type, so gating it by version " +
			"asserts a divergence that does not exist — see PVEVersionProfile.SDNFabrics.")
	}
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

// TestCompatServer_SDNFabricsAPIGate is the concrete AC2 demonstration:
// the exact same request (GET /cluster/sdn/fabrics/all) is fired at the
// exact same fixture topology, and only the PVEVersionProfile changes
// between subtests. PVE 8.2 must answer 501; PVE 9.0/9.2 must answer 200
// with the shape real hardware returns — this is the "fixture divergence
// between PVE versions is caught" case the T-2103 task card asks for.
//
// The mutation proof for this gate: flip SDNFabrics to true for 8.2 in
// CompatProfiles and the 8.2 subtest goes red on the status assertion;
// drop the body assertion's key check and the 9.x subtests stop noticing a
// wrapper that answers 200 with nothing in it.
func TestCompatServer_SDNFabricsAPIGate(t *testing.T) {
	tests := []struct {
		fixture     string
		version     string
		description string
		wantStatus  int
	}{
		{fixture: "compat/pve-8.2.yaml", version: "8.2", wantStatus: http.StatusNotImplemented, description: "PVE 8.2 has no /cluster/sdn/fabrics"},
		{fixture: "compat/pve-9.0.yaml", version: "9.0", wantStatus: http.StatusOK, description: "PVE 9.0 introduced SDN Fabrics"},
		{fixture: "compat/pve-9.2.yaml", version: "9.2", wantStatus: http.StatusOK, description: "PVE 9.2 still serves SDN Fabrics"},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			h := newCompatTestServer(t, tt.fixture, tt.version)
			ticket, _ := compatLogin(t, h)
			req := httptest.NewRequest(http.MethodGet, sdnFabricsAllPath, nil)
			req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("GET %s against PVE %s: status = %d, want %d (body=%s)",
					sdnFabricsAllPath, tt.version, rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get(CompatVersionHeader); got != tt.version {
				t.Errorf("%s = %q, want %q", CompatVersionHeader, got, tt.version)
			}

			var envelope struct {
				Message string `json:"message"`
				Data    struct {
					Fabrics []json.RawMessage `json:"fabrics"`
					Nodes   []json.RawMessage `json:"nodes"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decoding response: %v (body=%s)", err, rec.Body.String())
			}

			if tt.wantStatus == http.StatusNotImplemented {
				if !strings.Contains(envelope.Message, "/cluster/sdn/fabrics") {
					t.Errorf("rejection message = %q, want it to name the unsupported path", envelope.Message)
				}
				return
			}
			// A supporting profile must return the real shape — both keys
			// present, not an empty object. "fabrics" and "nodes" are the
			// two keys pvecube (9.2.4) returned; asserting on them is what
			// keeps a 200 with an empty body from counting as support.
			if envelope.Data.Fabrics == nil || envelope.Data.Nodes == nil {
				t.Errorf("PVE %s GET %s: body = %s, want both \"fabrics\" and \"nodes\" keys "+
					"(the shape planning/reports/evidence/pve-9.2.4-sdn-schema.txt captured)",
					tt.version, sdnFabricsAllPath, rec.Body.String())
			}
		})
	}
}

// TestCompatServer_FabricCRUDPathFallsThroughOnSupportingProfile is
// T-3101's replacement for the old TestCompatServer_
// UnmodeledFabricRouteIsNotSilentlyOK: until T-3101 the base *Server had no
// fabric routes at all, so every fabrics path beyond .../all necessarily
// 501'd even on a supporting profile — this test used to pin exactly that
// gap. Fabric CRUD is modeled now (sdn_fabric.go), so the same request
// (GET /cluster/sdn/fabrics/fabric on PVE 9.2) must fall through to the
// base Server's real handler and succeed with the real "no fabrics
// configured yet" empty-list shape, not a 501.
func TestCompatServer_FabricCRUDPathFallsThroughOnSupportingProfile(t *testing.T) {
	h := newCompatTestServer(t, "compat/pve-9.2.yaml", "9.2")
	ticket, _ := compatLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, SDNFabricsPath+"/fabric", nil)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s/fabric on PVE 9.2: status = %d, want 200 (body=%s) — fabric CRUD is modeled now, "+
			"this path should no longer 501 on a supporting profile", SDNFabricsPath, rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding response: %v (body=%s)", err, rec.Body.String())
	}
	if envelope.Data == nil {
		t.Errorf("data = %s, want an empty array (no fabrics configured), not null", rec.Body.String())
	}
}

// TestCompatServer_FabricCRUDPathStillGatedOnPredatingProfile proves the
// version gate T-3101 kept ("PVE 8.2 must still look like it has no
// fabrics family at all") applies to every fabrics path, not just .../all:
// the same GET .../fabrics/fabric request that succeeds on PVE 9.2 above
// must still 501 on PVE 8.2.
func TestCompatServer_FabricCRUDPathStillGatedOnPredatingProfile(t *testing.T) {
	h := newCompatTestServer(t, "compat/pve-8.2.yaml", "8.2")
	ticket, _ := compatLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, SDNFabricsPath+"/fabric", nil)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("GET %s/fabric on PVE 8.2: status = %d, want 501", SDNFabricsPath, rec.Code)
	}
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !strings.Contains(envelope.Message, "/cluster/sdn/fabrics") {
		t.Errorf("rejection message = %q, want it to name the unsupported path", envelope.Message)
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
