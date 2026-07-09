package pvemock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, fixtureName string) *Server {
	t.Helper()
	f, err := LoadFixture(fixturePath(t, fixtureName))
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixtureName, err)
	}
	return NewServer(f)
}

// login performs POST /access/ticket and returns the ticket + CSRF token.
func login(t *testing.T, srv *Server, username, password string) (ticket, csrf string) {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/api2/json/access/ticket", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login(%s): status %d body %s", username, rec.Code, rec.Body.String())
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
	if envelope.Data.Ticket == "" {
		t.Fatalf("login(%s): empty ticket in response %s", username, rec.Body.String())
	}
	return envelope.Data.Ticket, envelope.Data.CSRF
}

// authedRequest builds a request carrying the PVEAuthCookie and (for
// mutating methods) the CSRFPreventionToken header, mirroring a real PVE
// client.
func authedRequest(t *testing.T, method, path, ticket, csrf string, body []byte) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	if csrf != "" {
		req.Header.Set("CSRFPreventionToken", csrf)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func doJSON(t *testing.T, srv *Server, req *http.Request) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
		}
	}
	return rec, out
}

// mustStatus performs req and fails the test unless the response status is
// want, returning the decoded body. It exists to keep multi-step walkthrough
// tests (SDN CRUD, network staging, ...) free of `if rec, _ := ...; rec.Code
// != x` shadowing, since `rec`/`body` get reused many times in one test.
func mustStatus(t *testing.T, srv *Server, req *http.Request, want int) map[string]any {
	t.Helper()
	rec, body := doJSON(t, srv, req)
	if rec.Code != want {
		t.Fatalf("%s %s status = %d, want %d (body=%v)", req.Method, req.URL.Path, rec.Code, want, body)
	}
	return body
}

// TestTicketAuth_BadPasswordIs401 covers the /access/ticket bad-password
// path required by the T-004 task card.
func TestTicketAuth_BadPasswordIs401(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	form := url.Values{"username": {"root@pam"}, "password": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/api2/json/access/ticket", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestClusterReads_RequireAuth verifies unauthenticated reads are rejected
// and authenticated ones succeed, exercising the ticket -> authenticated
// reads step of the README walkthrough.
func TestClusterReads_RequireAuth(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")

	req := httptest.NewRequest(http.MethodGet, "/api2/json/cluster/status", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}

	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")
	req2 := authedRequest(t, http.MethodGet, "/api2/json/cluster/status", ticket, "", nil)
	rec2, body := doJSON(t, srv, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200; body=%v", rec2.Code, body)
	}
	if body["data"] == nil {
		t.Fatalf("expected non-nil data, got %v", body)
	}
}

// TestPermissions_ReadOnlyUserGetsForbiddenOnNetworkWrite is acceptance
// criterion 2: a fixture-declared read-only user attempting a network PUT
// gets a real 403, exactly like PVE.
func TestPermissions_ReadOnlyUserGetsForbiddenOnNetworkWrite(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "auditor@pve", "readonly")

	// Read-only user CAN read.
	readReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/network", ticket, "", nil)
	readRec, _ := doJSON(t, srv, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read-only GET /network status = %d, want 200", readRec.Code)
	}

	// Read-only user CANNOT write.
	body, _ := json.Marshal(map[string]any{"mtu": 9000})
	writeReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", ticket, csrf, body)
	writeRec, _ := doJSON(t, srv, writeReq)
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("read-only PUT /network/vmbr0 status = %d, want 403; body=%s", writeRec.Code, writeRec.Body.String())
	}

	// And root CAN.
	rootTicket, rootCSRF := login(t, srv, "root@pam", "vnprox-mock")
	rootReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", rootTicket, rootCSRF, body)
	rootRec, _ := doJSON(t, srv, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("root PUT /network/vmbr0 status = %d, want 200; body=%s", rootRec.Code, rootRec.Body.String())
	}
}

// TestPermissions_MissingCSRFIsRejected proves the mock actually enforces
// CSRF tokens on mutating requests, not just the ticket cookie.
func TestPermissions_MissingCSRFIsRejected(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")
	body, _ := json.Marshal(map[string]any{"mtu": 9000})
	req := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", ticket, "", body)
	rec, _ := doJSON(t, srv, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing-CSRF PUT status = %d, want 401", rec.Code)
	}
}

func networkList(t *testing.T, srv *Server, ticket string) []NetIface {
	t.Helper()
	req := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/network", ticket, "", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /network status = %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data []NetIface `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding network list: %v", err)
	}
	return envelope.Data
}

func findIface(ifaces []NetIface, name string) (NetIface, bool) {
	for _, i := range ifaces {
		if i.Iface == name {
			return i, true
		}
	}
	return NetIface{}, false
}

// TestNetworkStaging_WalkthroughSucceeds is the full acceptance-criterion-1
// path: authenticate, stage a network edit, see it marked pending, reload,
// and see it become live with no more pending marker. This is the same
// sequence the package README's curl walkthrough performs.
func TestNetworkStaging_WalkthroughSucceeds(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	before, ok := findIface(networkList(t, srv, ticket), "vmbr0")
	if !ok {
		t.Fatalf("vmbr0 missing before staging")
	}
	if before.Pending != PendingNone {
		t.Fatalf("vmbr0 pending = %q before any edit, want none", before.Pending)
	}
	if before.MTU == 9000 {
		t.Fatalf("test fixture already has mtu 9000, adjust test")
	}

	// Stage an MTU change.
	body, _ := json.Marshal(map[string]any{"mtu": 9000})
	stageReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", ticket, csrf, body)
	stageRec, _ := doJSON(t, srv, stageReq)
	if stageRec.Code != http.StatusOK {
		t.Fatalf("stage PUT status = %d, body=%s", stageRec.Code, stageRec.Body.String())
	}

	staged, ok := findIface(networkList(t, srv, ticket), "vmbr0")
	if !ok {
		t.Fatalf("vmbr0 missing after staging")
	}
	if staged.Pending != PendingChanged {
		t.Fatalf("vmbr0 pending = %q after staging, want %q", staged.Pending, PendingChanged)
	}
	if staged.MTU != 9000 {
		t.Fatalf("staged vmbr0 mtu = %d, want 9000", staged.MTU)
	}

	// Reload (apply) — a task-returning endpoint.
	reloadReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network", ticket, csrf, nil)
	reloadRec, reloadBody := doJSON(t, srv, reloadReq)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("reload PUT status = %d, body=%v", reloadRec.Code, reloadBody)
	}
	upid, _ := reloadBody["data"].(string)
	if upid == "" || !strings.HasPrefix(upid, "UPID:") {
		t.Fatalf("reload response data = %v, want a UPID string", reloadBody["data"])
	}

	// Poll the task to completion (latency is 0 by default so it's already
	// done, but poll anyway to exercise the polling endpoint for real).
	statusReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/tasks/"+upid+"/status", ticket, "", nil)
	statusRec, statusBody := doJSON(t, srv, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("task status GET = %d, body=%v", statusRec.Code, statusBody)
	}
	data, _ := statusBody["data"].(map[string]any)
	if data["status"] != "stopped" || data["exitstatus"] != "OK" {
		t.Fatalf("task status = %v, want stopped/OK", data)
	}

	after, ok := findIface(networkList(t, srv, ticket), "vmbr0")
	if !ok {
		t.Fatalf("vmbr0 missing after reload")
	}
	if after.Pending != PendingNone {
		t.Fatalf("vmbr0 pending = %q after successful reload, want none", after.Pending)
	}
	if after.MTU != 9000 {
		t.Fatalf("live vmbr0 mtu after reload = %d, want 9000", after.MTU)
	}
}

// TestNetworkStaging_PartialUpdatePreservesOtherFields proves
// PUT /nodes/{node}/network/{iface} merges the given fields onto the
// existing entry rather than replacing it wholesale (real PVE semantics) —
// a bug caught while writing the README walkthrough: sending only {"mtu":
// 9000} must not wipe out address/gateway/bridge_ports/etc.
func TestNetworkStaging_PartialUpdatePreservesOtherFields(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	before, ok := findIface(networkList(t, srv, ticket), "vmbr0")
	if !ok {
		t.Fatalf("vmbr0 missing")
	}

	body, _ := json.Marshal(map[string]any{"mtu": 9000})
	req := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", ticket, csrf, body)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}

	after, ok := findIface(networkList(t, srv, ticket), "vmbr0")
	if !ok {
		t.Fatalf("vmbr0 missing after update")
	}
	if after.MTU != 9000 {
		t.Fatalf("mtu = %d, want 9000", after.MTU)
	}
	if after.Address != before.Address {
		t.Errorf("address changed: %q -> %q, want unchanged", before.Address, after.Address)
	}
	if after.Gateway != before.Gateway {
		t.Errorf("gateway changed: %q -> %q, want unchanged", before.Gateway, after.Gateway)
	}
	if after.BridgePorts != before.BridgePorts {
		t.Errorf("bridge_ports changed: %q -> %q, want unchanged", before.BridgePorts, after.BridgePorts)
	}
	if after.Type != before.Type {
		t.Errorf("type changed: %q -> %q, want unchanged", before.Type, after.Type)
	}
	if after.Autostart != before.Autostart {
		t.Errorf("autostart changed: %v -> %v, want unchanged", before.Autostart, after.Autostart)
	}
}

// TestNetworkStaging_FailedReloadRollsBack is acceptance criterion 4:
// forcing a reload task to fail must roll the node back to its
// pre-staging state — never half-applied.
func TestNetworkStaging_FailedReloadRollsBack(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	before, _ := findIface(networkList(t, srv, ticket), "vmbr0")
	originalMTU := before.MTU

	body, _ := json.Marshal(map[string]any{"mtu": 9000})
	stageReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network/vmbr0", ticket, csrf, body)
	if rec, _ := doJSON(t, srv, stageReq); rec.Code != http.StatusOK {
		t.Fatalf("stage PUT status = %d", rec.Code)
	}

	// Force this reload to fail via the per-request query override.
	reloadReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network?mock_fail=1&mock_fail_reason=ifupdown2%20error", ticket, csrf, nil)
	reloadRec, reloadBody := doJSON(t, srv, reloadReq)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("reload PUT status = %d (task creation itself should still 200)", reloadRec.Code)
	}
	upid, _ := reloadBody["data"].(string)

	statusReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/tasks/"+upid+"/status", ticket, "", nil)
	_, statusBody := doJSON(t, srv, statusReq)
	data, _ := statusBody["data"].(map[string]any)
	exitStatus, _ := data["exitstatus"].(string)
	if !strings.HasPrefix(exitStatus, "failed") {
		t.Fatalf("task exitstatus = %q, want a failure", exitStatus)
	}
	if !strings.Contains(exitStatus, "ifupdown2 error") {
		t.Fatalf("task exitstatus = %q, want it to include the injected reason", exitStatus)
	}

	after, ok := findIface(networkList(t, srv, ticket), "vmbr0")
	if !ok {
		t.Fatalf("vmbr0 missing after failed reload")
	}
	if after.Pending != PendingNone {
		t.Fatalf("vmbr0 pending = %q after failed reload, want none (rolled back to pre-staging)", after.Pending)
	}
	if after.MTU != originalMTU {
		t.Fatalf("vmbr0 mtu after failed reload = %d, want unchanged original %d", after.MTU, originalMTU)
	}
}

// TestNetworkStaging_FixtureDefaultFailureInjection proves a node can be
// configured (via fixture MockOptions, not just the per-request override)
// to fail its next reload by default.
func TestNetworkStaging_FixtureDefaultFailureInjection(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f.Nodes["pve1"].Mock = &MockOptions{NetworkReloadFail: true}
	srv := NewServer(f)
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	reloadReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network", ticket, csrf, nil)
	_, reloadBody := doJSON(t, srv, reloadReq)
	upid, _ := reloadBody["data"].(string)

	statusReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/tasks/"+upid+"/status", ticket, "", nil)
	_, statusBody := doJSON(t, srv, statusReq)
	data, _ := statusBody["data"].(map[string]any)
	exitStatus, _ := data["exitstatus"].(string)
	if !strings.HasPrefix(exitStatus, "failed") {
		t.Fatalf("task exitstatus = %q, want a failure (fixture default)", exitStatus)
	}
}

// TestSDNZoneStatus_ReportsErrorForUnrealizedNode proves the SDN
// pending-vs-applied status endpoint surfaces real per-node problems using
// the messy-brownfield fixture's zone-legacy scenario (bridge missing on
// pve3).
func TestSDNZoneStatus_ReportsErrorForUnrealizedNode(t *testing.T) {
	srv := newTestServer(t, "messy-brownfield.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/zone-legacy/status", ticket, "", nil)
	rec, body := doJSON(t, srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("zone status GET = %d, body=%v", rec.Code, body)
	}
	entries, _ := body["data"].([]any)
	found := false
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m["node"] == "pve3" {
			found = true
			if m["status"] != "error" {
				t.Fatalf("pve3 zone-legacy status = %v, want error", m["status"])
			}
		}
	}
	if !found {
		t.Fatalf("no status entry for pve3 in %v", entries)
	}
}

// TestNetworkStaging_CreateDeleteRevert exercises creating a brand-new
// staged iface, then reverting all pending changes (DELETE
// /nodes/{node}/network), and separately deleting a staged iface outright.
func TestNetworkStaging_CreateDeleteRevert(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	createBody, _ := json.Marshal(map[string]any{"iface": "vmbr9", "type": "bridge", "autostart": true})
	createReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/network", ticket, csrf, createBody)
	if rec, _ := doJSON(t, srv, createReq); rec.Code != http.StatusOK {
		t.Fatalf("create iface status = %d", rec.Code)
	}

	staged, ok := findIface(networkList(t, srv, ticket), "vmbr9")
	if !ok || staged.Pending != PendingNew {
		t.Fatalf("vmbr9 = %+v, want present with pending=new", staged)
	}

	// Creating the same iface again should fail.
	dupReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/network", ticket, csrf, createBody)
	if rec, _ := doJSON(t, srv, dupReq); rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create status = %d, want 400", rec.Code)
	}

	// Revert: vmbr9 should disappear entirely (it was never live).
	revertReq := authedRequest(t, http.MethodDelete, "/api2/json/nodes/pve1/network", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, revertReq); rec.Code != http.StatusOK {
		t.Fatalf("revert status = %d", rec.Code)
	}
	if _, stillPresent := findIface(networkList(t, srv, ticket), "vmbr9"); stillPresent {
		t.Fatalf("vmbr9 still present after revert")
	}

	// Delete an existing live iface (staged as a delete).
	delReq := authedRequest(t, http.MethodDelete, "/api2/json/nodes/pve1/network/eno1", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, delReq); rec.Code != http.StatusOK {
		t.Fatalf("delete iface status = %d", rec.Code)
	}
	deleted, ok := findIface(networkList(t, srv, ticket), "eno1")
	if !ok || deleted.Pending != PendingDeleted {
		t.Fatalf("eno1 = %+v, want present with pending=deleted", deleted)
	}

	// Deleting a nonexistent iface 404s.
	missingReq := authedRequest(t, http.MethodDelete, "/api2/json/nodes/pve1/network/doesnotexist", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, missingReq); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing iface status = %d, want 404", rec.Code)
	}
}

// TestTaskLog_ReturnsLines exercises GET /nodes/{node}/tasks/{upid}/log.
func TestTaskLog_ReturnsLines(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	reloadReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network", ticket, csrf, nil)
	_, body := doJSON(t, srv, reloadReq)
	upid, _ := body["data"].(string)

	logReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/tasks/"+upid+"/log", ticket, "", nil)
	rec, logBody := doJSON(t, srv, logReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("task log status = %d", rec.Code)
	}
	lines, _ := logBody["data"].([]any)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log lines (start + completion), got %d", len(lines))
	}

	missing := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/tasks/UPID:bogus/log", ticket, "", nil)
	if rec, _ := doJSON(t, srv, missing); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task log status = %d, want 404", rec.Code)
	}
}

// TestClusterResources_ListsNodesAndGuests exercises GET /cluster/resources.
func TestClusterResources_ListsNodesAndGuests(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")
	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/resources", ticket, "", nil)
	rec, body := doJSON(t, srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	entries, _ := body["data"].([]any)
	kinds := map[string]bool{}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		kinds[m["type"].(string)] = true
	}
	for _, want := range []string{"node", "qemu", "lxc"} {
		if !kinds[want] {
			t.Errorf("cluster/resources missing an entry of type %q in %v", want, entries)
		}
	}
}

// TestUserSpec_HasPrivilege covers the fixture-level convenience method
// directly, independent of the session/auth machinery.
func TestUserSpec_HasPrivilege(t *testing.T) {
	admin := UserSpec{UserID: "root@pam", Privileges: []string{"*"}}
	if !admin.HasPrivilege("Sys.Modify") {
		t.Errorf("wildcard privilege should grant Sys.Modify")
	}
	limited := UserSpec{UserID: "auditor@pve", Privileges: []string{"Sys.Audit"}}
	if !limited.HasPrivilege("Sys.Audit") {
		t.Errorf("expected Sys.Audit to be granted")
	}
	if limited.HasPrivilege("Sys.Modify") {
		t.Errorf("expected Sys.Modify to be denied")
	}
}

// TestGuestConfig_GetPutRoundTrip exercises qemu/lxc config GET/PUT.
func TestGuestConfig_GetPutRoundTrip(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	getReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/qemu/100/config", ticket, "", nil)
	rec, body := doJSON(t, srv, getReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET guest config = %d, body=%v", rec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["name"] != "web01" {
		t.Fatalf("guest config name = %v, want web01", data["name"])
	}

	putBody, _ := json.Marshal(map[string]string{"memory": "4096"})
	putReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/qemu/100/config", ticket, csrf, putBody)
	putRec, _ := doJSON(t, srv, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT guest config = %d", putRec.Code)
	}

	rec2, body2 := doJSON(t, srv, authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/qemu/100/config", ticket, "", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET guest config after update = %d", rec2.Code)
	}
	data2, _ := body2["data"].(map[string]any)
	if data2["memory"] != "4096" {
		t.Fatalf("guest config memory after update = %v, want 4096", data2["memory"])
	}
}

// TestFirewall_ClusterRuleCRUD exercises the cluster-scope firewall CRUD.
func TestFirewall_ClusterRuleCRUD(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	createBody, _ := json.Marshal(FwRuleSpec{Enabled: true, Type: "in", Action: "ACCEPT", Proto: "tcp", Dport: "8006"})
	createReq := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/rules", ticket, csrf, createBody)
	if rec, _ := doJSON(t, srv, createReq); rec.Code != http.StatusOK {
		t.Fatalf("create cluster rule status = %d", rec.Code)
	}

	listReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/rules", ticket, "", nil)
	rec, body := doJSON(t, srv, listReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("list cluster rules status = %d", rec.Code)
	}
	rules, _ := body["data"].([]any)
	if len(rules) == 0 {
		t.Fatalf("expected at least one cluster firewall rule after create")
	}
}

// TestMockMess_ExposesDocumentedDrift ensures the /mock/mess introspection
// endpoint surfaces the messy-brownfield fixture's documented mess so tests
// (and humans) can enumerate exactly what it encodes.
func TestMockMess_ExposesDocumentedDrift(t *testing.T) {
	srv := newTestServer(t, "messy-brownfield.yaml")
	req := httptest.NewRequest(http.MethodGet, "/mock/mess", nil)
	rec, body := doJSON(t, srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /mock/mess status = %d", rec.Code)
	}
	mess, _ := body["data"].([]any)
	if len(mess) < 5 {
		t.Fatalf("expected >=5 documented mess items, got %d", len(mess))
	}
}
