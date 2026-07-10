package pvemock

// Tests for the auth-surface features added after the T-004 baseline:
// API-token authentication, ticket TTL/expiry, ticket-as-password renewal,
// static-code TOTP, and GET /access/permissions. The corresponding
// client-side integration tests live in internal/pve and internal/auth;
// these cover the mock's own semantics directly.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// tokenRequest builds a request authenticated with an API token header
// instead of a ticket cookie.
func tokenRequest(t *testing.T, method, path, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "PVEAPIToken="+token)
	return req
}

func TestAPIToken_FixtureTokenAuthenticates(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")

	// The fixture-declared root@pam!daemon token can read.
	req := tokenRequest(t, http.MethodGet, "/api2/json/cluster/status",
		"root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42")
	rec, body := doJSON(t, srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token GET /cluster/status = %d, want 200 (body=%v)", rec.Code, body)
	}
	if body["data"] == nil {
		t.Fatalf("expected non-nil data, got %v", body)
	}
}

func TestAPIToken_BadValuesAreRejected(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	for name, token := range map[string]string{
		"wrong secret":     "root@pam!daemon=deadbeef",
		"unknown token":    "root@pam!nope=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42",
		"unknown user":     "ghost@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42",
		"malformed (no !)": "root@pam=6b1c0a3e",
		"malformed (no =)": "root@pam!daemon",
	} {
		t.Run(name, func(t *testing.T) {
			req := tokenRequest(t, http.MethodGet, "/api2/json/cluster/status", token)
			rec, _ := doJSON(t, srv, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("token %q status = %d, want 401", token, rec.Code)
			}
		})
	}
}

func TestAPIToken_PrivilegesFollowOwningUser(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Give the read-only auditor a token; it must be able to read but not
	// write, exactly like the auditor's own ticket sessions.
	f.Users[1].Tokens = []TokenSpec{{TokenID: "ro", Secret: "sec-ro"}}
	srv := NewServer(f)

	read := tokenRequest(t, http.MethodGet, "/api2/json/nodes/pve1/network", "auditor@pve!ro=sec-ro")
	if rec, _ := doJSON(t, srv, read); rec.Code != http.StatusOK {
		t.Fatalf("auditor token read status = %d, want 200", rec.Code)
	}

	write := tokenRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", "auditor@pve!ro=sec-ro")
	if rec, _ := doJSON(t, srv, write); rec.Code != http.StatusForbidden {
		t.Fatalf("auditor token write status = %d, want 403", rec.Code)
	}
}

func TestTicketTTL_ExpiredTicketIs401(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f, WithTicketTTL(30*time.Millisecond))
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	// Fresh ticket works.
	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/status", ticket, "", nil)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
		t.Fatalf("fresh ticket status = %d, want 200", rec.Code)
	}

	time.Sleep(60 * time.Millisecond)
	req2 := authedRequest(t, http.MethodGet, "/api2/json/cluster/status", ticket, "", nil)
	if rec, _ := doJSON(t, srv, req2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket status = %d, want 401", rec.Code)
	}
}

func TestTicketTTL_FixtureFieldDrivesExpiry(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f.Mock.TicketTTLMS = 30
	srv := NewServer(f)
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	time.Sleep(60 * time.Millisecond)
	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/status", ticket, "", nil)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket (fixture TTL) status = %d, want 401", rec.Code)
	}
}

func TestTicketAsPassword_ValidTicketRenews(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	// A still-valid ticket is accepted as the password (PVE's renewal
	// shortcut), issuing a brand-new ticket.
	newTicket, _ := login(t, srv, "root@pam", ticket)
	if newTicket == ticket {
		t.Fatalf("renewal returned the same ticket, want a fresh one")
	}
	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/status", newTicket, "", nil)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
		t.Fatalf("renewed ticket status = %d, want 200", rec.Code)
	}
}

func TestTicketAsPassword_ForeignOrExpiredTicketRejected(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f, WithTicketTTL(30*time.Millisecond))

	rootTicket, _ := login(t, srv, "root@pam", "vnprox-mock")

	// Another user's ticket is not a valid password for auditor@pve.
	form := url.Values{"username": {"auditor@pve"}, "password": {rootTicket}}
	req := httptest.NewRequest(http.MethodPost, "/api2/json/access/ticket", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("foreign-ticket login status = %d, want 401", rec.Code)
	}

	// An expired ticket is not a valid password either.
	time.Sleep(60 * time.Millisecond)
	form2 := url.Values{"username": {"root@pam"}, "password": {rootTicket}}
	req2 := httptest.NewRequest(http.MethodPost, "/api2/json/access/ticket", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expired-ticket login status = %d, want 401", rec2.Code)
	}
}

func TestTOTP_RequiredUserFlows(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")

	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api2/json/access/ticket", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	// Missing otp → 401.
	if rec := post(url.Values{"username": {"totp-user@pve"}, "password": {"with-2fa"}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing-otp login status = %d, want 401", rec.Code)
	}
	// Wrong otp → 401.
	if rec := post(url.Values{"username": {"totp-user@pve"}, "password": {"with-2fa"}, "otp": {"000000"}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-otp login status = %d, want 401", rec.Code)
	}
	// Correct otp → normal ticket.
	rec := post(url.Values{"username": {"totp-user@pve"}, "password": {"with-2fa"}, "otp": {"246810"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("correct-otp login status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	// Users without a totp field are unaffected by a stray otp param.
	if rec := post(url.Values{"username": {"root@pam"}, "password": {"vnprox-mock"}, "otp": {"junk"}}); rec.Code != http.StatusOK {
		t.Fatalf("no-totp user with stray otp status = %d, want 200", rec.Code)
	}
}

func TestAccessPermissions_ReturnsFixturePrivilegesAtRoot(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, _ := login(t, srv, "auditor@pve", "readonly")

	req := authedRequest(t, http.MethodGet, "/api2/json/access/permissions", ticket, "", nil)
	rec, body := doJSON(t, srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /access/permissions = %d, want 200", rec.Code)
	}
	data, _ := body["data"].(map[string]any)
	root, _ := data["/"].(map[string]any)
	if root == nil {
		t.Fatalf("expected a \"/\" path entry, got %v", data)
	}
	for _, priv := range []string{"Sys.Audit", "VM.Audit", "SDN.Audit"} {
		if v, _ := root[priv].(float64); v != 1 {
			t.Errorf("permissions[/][%s] = %v, want 1", priv, root[priv])
		}
	}
	if _, present := root["Sys.Modify"]; present {
		t.Errorf("auditor unexpectedly granted Sys.Modify: %v", root)
	}

	// Unauthenticated → 401.
	anon := httptest.NewRequest(http.MethodGet, "/api2/json/access/permissions", nil)
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, anon)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /access/permissions = %d, want 401", rec2.Code)
	}

	// Token identities can read their permissions too.
	tokenReq := tokenRequest(t, http.MethodGet, "/api2/json/access/permissions",
		"root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42")
	rec3, body3 := doJSON(t, srv, tokenReq)
	if rec3.Code != http.StatusOK {
		t.Fatalf("token GET /access/permissions = %d, want 200", rec3.Code)
	}
	data3, _ := body3["data"].(map[string]any)
	root3, _ := data3["/"].(map[string]any)
	if v, _ := root3["*"].(float64); v != 1 {
		t.Errorf("token permissions[/][*] = %v, want 1 (root wildcard)", root3["*"])
	}
}

func TestIPAM_ListGetAndStatus(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	listReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams", ticket, "", nil)
	rec, body := doJSON(t, srv, listReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /cluster/sdn/ipams = %d, want 200", rec.Code)
	}
	ipams, _ := body["data"].([]any)
	if len(ipams) != 1 {
		t.Fatalf("ipams = %v, want exactly one (pve)", body["data"])
	}
	first, _ := ipams[0].(map[string]any)
	if first["ipam"] != "pve" || first["type"] != "pve" {
		t.Fatalf("ipam row = %v, want ipam=pve type=pve", first)
	}

	getReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/pve", ticket, "", nil)
	if rec, _ := doJSON(t, srv, getReq); rec.Code != http.StatusOK {
		t.Fatalf("GET /cluster/sdn/ipams/pve = %d, want 200", rec.Code)
	}

	statusReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/pve/status", ticket, "", nil)
	rec2, body2 := doJSON(t, srv, statusReq)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /cluster/sdn/ipams/pve/status = %d, want 200", rec2.Code)
	}
	entries, _ := body2["data"].([]any)
	if len(entries) != 3 {
		t.Fatalf("ipam status entries = %d, want 3 (fixture)", len(entries))
	}
	var sawGateway, sawGuest bool
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m["ip"] == "10.100.0.1" {
			sawGateway = true
			if gw, _ := m["gateway"].(float64); gw != 1 {
				t.Errorf("gateway entry gateway = %v, want 1 (0/1 int wire convention)", m["gateway"])
			}
		}
		if m["ip"] == "10.100.0.50" {
			sawGuest = true
			if m["hostname"] != "app01" || m["vmid"] != float64(200) {
				t.Errorf("guest entry = %v, want hostname=app01 vmid=200", m)
			}
			if _, present := m["gateway"]; present {
				t.Errorf("non-gateway entry carries a gateway field: %v", m)
			}
		}
	}
	if !sawGateway || !sawGuest {
		t.Fatalf("missing expected entries (gateway=%v guest=%v) in %v", sawGateway, sawGuest, entries)
	}

	// Unknown ipam → 404.
	missing := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/netbox/status", ticket, "", nil)
	if rec, _ := doJSON(t, srv, missing); rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown ipam status = %d, want 404", rec.Code)
	}
}

func TestFixtureValidation_TokensAndIpams(t *testing.T) {
	base := func(mutate func(f *Fixture)) error {
		f := &Fixture{
			Cluster: ClusterSpec{Name: "t", Nodes: []ClusterNodeSpec{{Name: "n1", IP: "10.0.0.1", Online: true}}},
			Nodes:   map[string]*NodeSpec{"n1": {}},
			Users:   []UserSpec{{UserID: "root@pam", Password: "x", Privileges: []string{"*"}}},
		}
		mutate(f)
		return f.Validate()
	}

	if err := base(func(f *Fixture) {
		f.Users[0].Tokens = []TokenSpec{{TokenID: "a", Secret: "s"}, {TokenID: "a", Secret: "s2"}}
	}); err == nil || !strings.Contains(err.Error(), "duplicate tokenid") {
		t.Errorf("duplicate tokenid: err = %v, want duplicate-tokenid failure", err)
	}
	if err := base(func(f *Fixture) {
		f.Users[0].Tokens = []TokenSpec{{TokenID: "a"}}
	}); err == nil || !strings.Contains(err.Error(), "secret must not be empty") {
		t.Errorf("empty secret: err = %v, want empty-secret failure", err)
	}
	if err := base(func(f *Fixture) {
		f.SDN.Ipams = []SDNIpamSpec{{ID: "pve", Type: "pve", Entries: []IPAMEntrySpec{{IP: "10.0.0.5", Vnet: "ghost"}}}}
	}); err == nil || !strings.Contains(err.Error(), "unknown vnet") {
		t.Errorf("dangling ipam vnet: err = %v, want unknown-vnet failure", err)
	}
	if err := base(func(f *Fixture) {
		f.SDN.Ipams = []SDNIpamSpec{{ID: "pve", Type: "pve"}, {ID: "pve", Type: "pve"}}
	}); err == nil || !strings.Contains(err.Error(), "duplicate ipam id") {
		t.Errorf("duplicate ipam id: err = %v, want duplicate-id failure", err)
	}
}
