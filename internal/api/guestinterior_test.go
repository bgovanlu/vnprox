package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// threeNodeVlanHarness mirrors simLabHarness (simulate_verify_test.go)
// against three-node-vlan.yaml instead: a real *pvemock.Server, a real
// *pve.Client, a fully-ingested *inventory.Graph, an on-disk store.DB, and
// a host.FixtureReader over the same mock state — so this test exercises
// both the qemu guest-agent path (app01, pve1/200) and the lxc host-side
// path (cache01, pve2/201) against the exact fixture data
// testdata/clusters/three-node-vlan.yaml's T-1304 comment documents.
type threeNodeVlanHarness struct {
	graph   *inventory.Graph
	client  *pve.Client
	host    *host.FixtureReader
	toggles *store.GuestInteriorToggleRepo
	audit   *store.AuditRepo
}

func newThreeNodeVlanHarness(t *testing.T) *threeNodeVlanHarness {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../testdata/clusters/three-node-vlan.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	fxHost := host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))

	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE: client, Host: fxHost, Graph: graph,
		PVEInterval: time.Hour, HostInterval: time.Hour, LLDPInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	if _, refreshErr := c.RefreshNow(context.Background(), inventory.Scope{}); refreshErr != nil {
		t.Fatalf("RefreshNow: %v", refreshErr)
	}

	db, err := store.Open(context.Background(), t.TempDir()+"/vnprox.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return &threeNodeVlanHarness{
		graph: graph, client: client, host: fxHost,
		toggles: store.NewGuestInteriorToggleRepo(db), audit: store.NewAuditRepo(db),
	}
}

// guestInteriorRouter builds a full router with every guest-interior
// dependency wired from h, plus a fixedProbeClientProvider for the qemu
// path (fixedProbeClientProvider is declared in simulate_verify_test.go).
// peers/ipamSvc/localNode are test-supplied so individual tests can drive
// the peer-routing and IPAM-diff branches without rebuilding the whole
// harness.
func (h *threeNodeVlanHarness) router(peers PeerContainerSource, ipamSvc GuestInteriorIPAMSource, localNode func() string) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:                 fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "root@pam"},
		Simulator:            h.graph,
		ProbeClients:         fixedProbeClientProvider{client: h.client},
		ProbeAudit:           h.audit,
		GuestInteriorToggles: h.toggles,
		GuestInteriorGraph:   h.graph,
		GuestInteriorHost:    h.host,
		GuestInteriorPeers:   peers,
		GuestInteriorIPAM:    ipamSvc,
		LocalNode:            localNode,
	})
}

func putToggle(t *testing.T, r http.Handler, ref string, enabled bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(guestInteriorTogglePutRequest{Enabled: enabled})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/guests/"+ref+"/interior-toggle", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func getInterior(t *testing.T, r http.Handler, ref string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+ref+"/interior", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const app01Ref = "guest:pve1:200"
const cache01Ref = "guest:pve2:201"

func localNodePve1() string { return "pve1" }

// TestGuestInteriorToggle_DefaultOffAndRoundTrip exercises the toggle
// GET/PUT routes end to end: off by default, PUT flips it, GET reflects
// the change.
func TestGuestInteriorToggle_DefaultOffAndRoundTrip(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	r := h.router(nil, nil, localNodePve1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+app01Ref+"/interior-toggle", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET toggle status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got guestInteriorToggleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Enabled {
		t.Errorf("Enabled = true, want false (default off)")
	}

	if putRec := putToggle(t, r, app01Ref, true); putRec.Code != http.StatusOK {
		t.Fatalf("PUT toggle status = %d, body: %s", putRec.Code, putRec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+app01Ref+"/interior-toggle", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.Enabled {
		t.Errorf("Enabled = false after PUT true, want true")
	}
}

// TestGetGuestInterior_ToggleOff404 is AC3: a guest with the toggle off
// returns 404/an explicit not-enabled response, never silently reaching
// into the guest.
func TestGetGuestInterior_ToggleOff404(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	r := h.router(nil, nil, localNodePve1)

	rec := getInterior(t, r, app01Ref)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if envelope.Error.Code != guestInteriorErrNotEnabled {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, guestInteriorErrNotEnabled)
	}
}

// TestGetGuestInterior_QEMU_LocalNode exercises the qemu path end to end
// against app01 (pve1/200): guest-agent-reported interfaces/addresses,
// exec-based routes/DNS/sockets, and the scripted icmp probe backing
// default-gateway reachability, source "qemu-ga".
func TestGetGuestInterior_QEMU_LocalNode(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	r := h.router(nil, nil, localNodePve1)
	putToggle(t, r, app01Ref, true)

	rec := getInterior(t, r, app01Ref)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got guestInteriorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Source != "qemu-ga" {
		t.Errorf("source = %q, want qemu-ga", got.Source)
	}
	if len(got.Addresses) != 1 || got.Addresses[0].IP != "10.10.0.200" {
		t.Errorf("addresses = %+v, want 10.10.0.200", got.Addresses)
	}
	if len(got.Routes) != 2 {
		t.Errorf("routes = %+v, want 2 entries", got.Routes)
	}
	if len(got.DNS.Nameservers) != 1 {
		t.Errorf("dns = %+v, want one nameserver", got.DNS)
	}
	if len(got.ListeningSockets) != 1 || got.ListeningSockets[0].LocalPort != 22 {
		t.Errorf("listeningSockets = %+v, want one entry on port 22", got.ListeningSockets)
	}
	if !got.DefaultGatewayReachable {
		t.Errorf("defaultGatewayReachable = false, want true")
	}

	// Every request that passes input validation and reaches a source
	// produces exactly one guest.interior_read audit row.
	rows, _, err := h.audit.ListPage(context.Background(), store.AuditFilter{Action: "guest.interior_read"}, "", 10)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].Result != "ok" || !rows[0].Target.Valid || rows[0].Target.String != app01Ref {
		t.Errorf("audit row = %+v, want result=ok target=%s", rows[0], app01Ref)
	}
}

// TestGetGuestInterior_LXC_LocalNode exercises the lxc host-side path
// against cache01 (pve2/201), same response shape as the qemu case, with
// source "lxc-host" (AC2's "same response shape" assertion at the API
// layer, on top of internal/guestinterior's own package-level coverage).
func TestGetGuestInterior_LXC_LocalNode(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	// cache01's node (pve2) is "local" for this test.
	r := h.router(nil, nil, func() string { return "pve2" })
	putToggle(t, r, cache01Ref, true)

	rec := getInterior(t, r, cache01Ref)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got guestInteriorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Source != "lxc-host" {
		t.Errorf("source = %q, want lxc-host", got.Source)
	}
	if len(got.Addresses) != 1 || got.Addresses[0].IP != "10.10.0.201" {
		t.Errorf("addresses = %+v, want 10.10.0.201", got.Addresses)
	}
	if len(got.Routes) != 2 {
		t.Errorf("routes = %+v, want 2 entries", got.Routes)
	}
	if !got.DefaultGatewayReachable {
		t.Errorf("defaultGatewayReachable = false, want true")
	}
}

// fakePeerContainerSource is a Go-level PeerContainerSource test double —
// no real peer HMAC round trip, just direct method scripting — for
// TestGetGuestInterior_LXC_PeerNode's cluster-awareness assertion
// (docs/architecture.md §1/§5: an lxc guest on a peer node must be
// inspectable exactly the way one on this node is).
type fakePeerContainerSource struct {
	peers        []peer.Peer
	interiorCall struct {
		node string
		vmid int
	}
	raw host.ContainerInteriorRaw
}

func (f *fakePeerContainerSource) Peers(context.Context) ([]peer.Peer, error) { return f.peers, nil }

func (f *fakePeerContainerSource) ContainerInterior(_ context.Context, _ peer.Peer, node string, vmid int) (host.ContainerInteriorRaw, error) {
	f.interiorCall.node, f.interiorCall.vmid = node, vmid
	return f.raw, nil
}

func (f *fakePeerContainerSource) ContainerPing(context.Context, peer.Peer, string, int, string) (bool, error) {
	return true, nil
}

// TestGetGuestInterior_LXC_PeerNode is the cluster-awareness case: cache01
// lives on pve2, but this daemon's own node (per localNode) is pve1 — the
// lxc read must route through the peer, not fail or silently read the
// wrong node.
func TestGetGuestInterior_LXC_PeerNode(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	peers := &fakePeerContainerSource{
		peers: []peer.Peer{{Node: "pve2", Addr: "10.10.0.12:8007"}},
		raw: host.ContainerInteriorRaw{
			AddrJSON:  []byte(`[{"ifname":"eth0","flags":["UP"],"addr_info":[{"family":"inet","local":"10.10.0.201","prefixlen":24}]}]`),
			RouteJSON: []byte(`[{"dst":"10.10.0.0/24","dev":"eth0"}]`),
		},
	}
	r := h.router(peers, nil, localNodePve1) // localNode is pve1; cache01 is on pve2
	putToggle(t, r, cache01Ref, true)

	rec := getInterior(t, r, cache01Ref)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got guestInteriorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Source != "lxc-host" {
		t.Errorf("source = %q, want lxc-host", got.Source)
	}
	if len(got.Addresses) != 1 || got.Addresses[0].IP != "10.10.0.201" {
		t.Errorf("addresses = %+v, want the peer-routed 10.10.0.201", got.Addresses)
	}
	if peers.interiorCall.node != "pve2" || peers.interiorCall.vmid != 201 {
		t.Errorf("peer ContainerInterior called with (%s, %d), want (pve2, 201)", peers.interiorCall.node, peers.interiorCall.vmid)
	}
}

// TestGetGuestInterior_LXC_NoPeerRoute503: cache01's node is not local and
// no peer route reaches it — an honest 503, never silently returning
// nothing or panicking.
func TestGetGuestInterior_LXC_NoPeerRoute503(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	r := h.router(nil, nil, localNodePve1) // no peers wired, cache01 is on pve2 (not local)
	putToggle(t, r, cache01Ref, true)

	rec := getInterior(t, r, cache01Ref)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body: %s", rec.Code, rec.Body.String())
	}
}

// fakeGuestInteriorIPAM is a minimal GuestInteriorIPAMSource test double.
type fakeGuestInteriorIPAM struct {
	byCIDR map[string][]ipam.Allocation
}

func (f fakeGuestInteriorIPAM) AllAllocations(context.Context) (map[string][]ipam.Allocation, error) {
	return f.byCIDR, nil
}

// TestGetGuestInterior_IPAMDiff is AC4: a guest-claimed address matching
// an IPAM allocation -> matches: true; app01 claims 10.10.0.200, which
// this test's fake IPAM source declares allocated, so it must round-trip
// as matches:true; a second, unallocated address must be absent from
// IPAM's records and so match:false were it present — exercised via
// TestGetGuestInterior_IPAMDiff_NoMatch below for the negative case.
func TestGetGuestInterior_IPAMDiff(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	ipamSvc := fakeGuestInteriorIPAM{byCIDR: map[string][]ipam.Allocation{
		"10.10.0.0/24": {{IP: "10.10.0.200", MAC: "BC:24:11:AA:02:C8"}},
	}}
	r := h.router(nil, ipamSvc, localNodePve1)
	putToggle(t, r, app01Ref, true)

	rec := getInterior(t, r, app01Ref)
	var got guestInteriorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.IPAMDiff) != 1 {
		t.Fatalf("ipamDiff = %+v, want exactly one entry", got.IPAMDiff)
	}
	d := got.IPAMDiff[0]
	if d.IP != "10.10.0.200" || !d.Claimed || !d.Allocated || !d.Matches {
		t.Errorf("ipamDiff[0] = %+v, want {ip:10.10.0.200 claimed:true allocated:true matches:true}", d)
	}
}

// TestGetGuestInterior_IPAMDiff_NoMatch is AC4's negative case: a claimed
// address absent from IPAM -> matches:false, surfaced without mutating
// IPAM (GuestInteriorIPAMSource has exactly one method, AllAllocations — a
// read — so there is structurally no write path this handler could take;
// stated in the report as the regression evidence).
func TestGetGuestInterior_IPAMDiff_NoMatch(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	ipamSvc := fakeGuestInteriorIPAM{byCIDR: map[string][]ipam.Allocation{
		"10.10.0.0/24": {{IP: "10.10.0.99"}}, // does not include app01's claimed 10.10.0.200
	}}
	r := h.router(nil, ipamSvc, localNodePve1)
	putToggle(t, r, app01Ref, true)

	rec := getInterior(t, r, app01Ref)
	var got guestInteriorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.IPAMDiff) != 1 {
		t.Fatalf("ipamDiff = %+v, want exactly one entry", got.IPAMDiff)
	}
	d := got.IPAMDiff[0]
	if !d.Claimed || d.Allocated || d.Matches {
		t.Errorf("ipamDiff[0] = %+v, want claimed:true allocated:false matches:false", d)
	}
}

// TestGuestInteriorRoutes_NotMountedWithoutToggleStore mirrors Layouts/
// Annotations' own precedent: a nil GuestInteriorToggles skips mounting
// the whole route family.
func TestGuestInteriorRoutes_NotMountedWithoutToggleStore(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "root@pam"},
	})
	rec := getInterior(t, r, app01Ref)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

// TestGuestInteriorInterior_NotMountedWithoutUsernameLookup proves GET
// /guests/{ref}/interior (audited) isn't mounted when AuthService can't
// resolve a username, mirroring TestLayoutsRoutes_NotMountedWithoutUsernameLookup's
// precedent — the toggle GET route, which needs no username, still is.
func TestGuestInteriorInterior_NotMountedWithoutUsernameLookup(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:                 fakeAuth{authenticated: true},
		GuestInteriorToggles: h.toggles,
		GuestInteriorGraph:   h.graph,
	})
	rec := getInterior(t, r, app01Ref)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /interior status = %d, want 404 (not mounted without UsernameLookup)", rec.Code)
	}
	toggleReq := httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+app01Ref+"/interior-toggle", nil)
	toggleRec := httptest.NewRecorder()
	r.ServeHTTP(toggleRec, toggleReq)
	if toggleRec.Code != http.StatusOK {
		t.Errorf("GET /interior-toggle status = %d, want 200 (mounted even without UsernameLookup)", toggleRec.Code)
	}
}

// TestGuestInteriorRoutes_PercentEncodedRef pins the encoding the SPA actually
// uses.
//
// Every other test in this file spells the ref raw ("guest:pve1:200"), which is
// what curl sends and what chi hands back unchanged. The browser does not: the
// frontend builds these URLs with encodeURIComponent, so the wire form is
// "guest%3Apve1%3A200". chi routes on r.URL.RawPath when it is non-empty, so
// URLParam returned the still-encoded string and ParseRef rejected it — every
// request the SPA has ever made to these three routes got a 400, and the guest
// interior feature was unreachable from the UI while this package's own tests
// stayed green. Found via the e2e suite (T-2108).
func TestGuestInteriorRoutes_PercentEncodedRef(t *testing.T) {
	h := newThreeNodeVlanHarness(t)
	r := h.router(nil, nil, localNodePve1)

	// encodeURIComponent's encoding, not Go's url.PathEscape: Go leaves ":"
	// alone in a path segment, the browser does not. Reproducing the browser's
	// form exactly is the whole point — the raw form already worked, which is
	// why every other test here passed while the feature was broken.
	encoded := strings.ReplaceAll(app01Ref, ":", "%3A")
	if encoded == app01Ref {
		t.Fatalf("app01Ref %q contains no ':' to escape, so this test proves nothing", app01Ref)
	}
	if strings.Contains(encoded, ":") {
		t.Fatalf("encoded ref %q still contains a raw ':'", encoded)
	}

	// GET the toggle with the encoded ref.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+encoded+"/interior-toggle", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET toggle with percent-encoded ref = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// PUT the toggle with the encoded ref.
	body := strings.NewReader(`{"enabled":true}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/guests/"+encoded+"/interior-toggle", body)
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	r.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT toggle with percent-encoded ref = %d, want 200; body: %s", putRec.Code, putRec.Body.String())
	}

	// And the state actually changed — a 200 that did nothing would be worse
	// than the 400 it replaced.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+encoded+"/interior-toggle", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var got guestInteriorToggleResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !got.Enabled {
		t.Error("toggle still reports disabled after a PUT with a percent-encoded ref")
	}
	if got.Ref != app01Ref {
		t.Errorf("Ref = %q, want the decoded %q", got.Ref, app01Ref)
	}

	// The interior read must accept the same encoding. With the toggle now on,
	// a 400 here would mean the third route still rejects the SPA's form.
	intReq := httptest.NewRequest(http.MethodGet, "/api/v1/guests/"+encoded+"/interior", nil)
	intRec := httptest.NewRecorder()
	r.ServeHTTP(intRec, intReq)
	if intRec.Code == http.StatusBadRequest {
		t.Errorf("GET interior with percent-encoded ref = 400 (ref rejected); body: %s", intRec.Body.String())
	}
}
