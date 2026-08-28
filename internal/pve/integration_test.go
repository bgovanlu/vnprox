// SPDX-License-Identifier: Apache-2.0

package pve_test

// Integration tests for internal/pve, exercised against a real
// httptest.Server wrapping internal/pvemock (T-004) — the mock PVE API
// server this client is built and tested against, per T-101's task card.
//
// Fixture paths are relative to this file's directory
// (internal/pve/testdata_test.go-adjacent), i.e. two levels up from
// internal/pve to the repo root's testdata/clusters/.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const (
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
	fixtureEvpn       = "../../testdata/clusters/evpn-lab.yaml"
)

func newMockServer(t *testing.T, fixturePath string) *httptest.Server {
	t.Helper()
	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func newTicketClient(t *testing.T, apiURL, username, password string) *pve.Client {
	t.Helper()
	c, err := pve.New(pve.Config{
		APIURL:   apiURL,
		Auth:     pve.AuthTicket,
		Username: username,
		Password: password,
	})
	if err != nil {
		t.Fatalf("pve.New (ticket): %v", err)
	}
	return c
}

// --- ticket auth: full read surface against single-node -------------------

func TestTicketAuth_FullReadSurface(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	status, err := c.ClusterStatus(ctx)
	if err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}
	if len(status) == 0 {
		t.Fatalf("ClusterStatus: expected at least one row")
	}
	foundCluster := false
	for _, s := range status {
		if s.Type == "cluster" {
			foundCluster = true
			if !s.Quorate {
				t.Errorf("expected quorate cluster in fixture, got %+v", s)
			}
		}
	}
	if !foundCluster {
		t.Fatalf("ClusterStatus: no cluster row in %+v", status)
	}

	resources, err := c.ClusterResources(ctx)
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if len(resources) == 0 {
		t.Fatalf("ClusterResources: expected at least the node row")
	}

	ifaces, err := c.ListNodeNetwork(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListNodeNetwork: %v", err)
	}
	var vmbr0 *pve.NetworkInterface
	for i := range ifaces {
		if ifaces[i].Iface == "vmbr0" {
			vmbr0 = &ifaces[i]
		}
	}
	if vmbr0 == nil {
		t.Fatalf("ListNodeNetwork: expected vmbr0 in %+v", ifaces)
	}
	if vmbr0.BridgePorts != "eno1" {
		t.Errorf("vmbr0.BridgePorts = %q, want eno1", vmbr0.BridgePorts)
	}
	if !vmbr0.Autostart {
		// Regression test (T-608, hardware validation): real PVE reports
		// autostart as a 0/1 int over the wire, not a JSON bool; pvemock now
		// mirrors that (NetIface.MarshalJSON), and the client decodes via
		// networkInterfaceWire — this assertion would have caught the bug
		// where the client's plain `bool` field silently decoded wrong (or,
		// against real PVE, failed to decode at all).
		t.Errorf("vmbr0.Autostart = false, want true (wire: PVE/pvemock report this as a 0/1 int)")
	}

	got, err := c.GetNodeNetworkInterface(ctx, "pve1", "vmbr0")
	if err != nil {
		t.Fatalf("GetNodeNetworkInterface: %v", err)
	}
	if got.Iface != "vmbr0" {
		t.Errorf("GetNodeNetworkInterface: iface = %q, want vmbr0", got.Iface)
	}
}

// --- staged write + reload + task wait (success path) ---------------------

func TestTicketAuth_NetworkUpdateReloadAndWait(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	mtu := 9000
	if err := c.UpdateNodeNetworkInterface(ctx, "pve1", "vmbr0", pve.NetworkInterfaceUpdate{MTU: &mtu}); err != nil {
		t.Fatalf("UpdateNodeNetworkInterface: %v", err)
	}

	// Staged, not yet applied: GET should show pending + old netlink MTU
	// unaffected by the reload the next step runs.
	staged, err := c.GetNodeNetworkInterface(ctx, "pve1", "vmbr0")
	if err != nil {
		t.Fatalf("GetNodeNetworkInterface (staged): %v", err)
	}
	if staged.MTU != 9000 {
		t.Fatalf("staged MTU = %d, want 9000", staged.MTU)
	}
	if staged.Pending != pve.PendingChanged {
		t.Fatalf("staged Pending = %q, want %q", staged.Pending, pve.PendingChanged)
	}

	upid, err := c.ReloadNodeNetwork(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReloadNodeNetwork: %v", err)
	}
	if upid == "" {
		t.Fatalf("ReloadNodeNetwork: empty UPID")
	}

	final, err := c.WaitTask(ctx, "pve1", upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("WaitTask: %v", err)
	}
	if final.ExitStatus != "OK" {
		t.Fatalf("final.ExitStatus = %q, want OK", final.ExitStatus)
	}

	live, err := c.GetNodeNetworkInterface(ctx, "pve1", "vmbr0")
	if err != nil {
		t.Fatalf("GetNodeNetworkInterface (live): %v", err)
	}
	if live.Pending != pve.PendingNone {
		t.Fatalf("live.Pending = %q, want none after successful reload", live.Pending)
	}
	if live.MTU != 9000 {
		t.Fatalf("live MTU = %d, want 9000 after successful reload", live.MTU)
	}
}

// --- 403 denied surfaces as *pve.ErrPVEDenied ------------------------------

func TestTicketAuth_DeniedSurfacesAsErrPVEDenied(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "auditor@pve", "readonly")
	ctx := context.Background()

	mtu := 9000
	err := c.UpdateNodeNetworkInterface(ctx, "pve1", "vmbr0", pve.NetworkInterfaceUpdate{MTU: &mtu})
	if err == nil {
		t.Fatalf("expected an error from a read-only user attempting a network write")
	}

	var denied *pve.ErrPVEDenied
	if !errors.As(err, &denied) {
		t.Fatalf("errors.As(err, &denied) failed; got %#v (%v)", err, err)
	}
	if !strings.Contains(denied.Message, "Sys.Modify") {
		t.Errorf("denied.Message = %q, want it to mention Sys.Modify", denied.Message)
	}

	// Reads must still work for the same user.
	if _, err := c.ListNodeNetwork(ctx, "pve1"); err != nil {
		t.Fatalf("ListNodeNetwork as auditor: %v", err)
	}
}

// --- bad credentials surface as *pve.ErrPVEAuth ----------------------------

func TestTicketAuth_BadCredentialsSurfaceAsErrPVEAuth(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "root@pam", "wrong-password")
	ctx := context.Background()

	_, err := c.ClusterStatus(ctx)
	if err == nil {
		t.Fatalf("expected an auth error with a bad password")
	}
	var authErr *pve.ErrPVEAuth
	if !errors.As(err, &authErr) {
		t.Fatalf("errors.As(err, &authErr) failed; got %#v (%v)", err, err)
	}
}

// --- guest config get/put --------------------------------------------------

func TestTicketAuth_GuestConfigGetAndPut(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	cfg, err := c.GetGuestConfig(ctx, "pve1", pve.GuestQemu, 200)
	if err != nil {
		t.Fatalf("GetGuestConfig: %v", err)
	}
	if cfg["name"] != "app01" {
		t.Fatalf("GetGuestConfig: name = %q, want app01", cfg["name"])
	}
	if cfg["cores"] != "4" {
		// Regression test (T-608, hardware validation): real PVE returns
		// "cores" (and several other guest-config fields) as a JSON number,
		// not a string — GetGuestConfig used to decode straight into
		// map[string]string, which failed outright against real PVE. This
		// asserts the stringified value survives the mock's number-typed
		// wire shape (marshalGuestConfig) exactly as it would from a real
		// node.
		t.Fatalf("GetGuestConfig: cores = %q, want \"4\" (wire: PVE reports several guest-config fields, incl. this one, as JSON numbers)", cfg["cores"])
	}

	err = c.UpdateGuestConfig(ctx, "pve1", pve.GuestQemu, 200, pve.GuestConfigUpdate{
		Set: map[string]string{"description": "updated by test"},
	})
	if err != nil {
		t.Fatalf("UpdateGuestConfig: %v", err)
	}

	cfg2, err := c.GetGuestConfig(ctx, "pve1", pve.GuestQemu, 200)
	if err != nil {
		t.Fatalf("GetGuestConfig (after update): %v", err)
	}
	if cfg2["description"] != "updated by test" {
		t.Fatalf("GetGuestConfig: description = %q, want %q", cfg2["description"], "updated by test")
	}

	guests, err := c.ListGuests(ctx, pve.GuestQemu)
	if err != nil {
		t.Fatalf("ListGuests: %v", err)
	}
	found := false
	for _, g := range guests {
		if g.VMID == 200 && g.Node == "pve1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListGuests(qemu): expected vmid 200 on pve1, got %+v", guests)
	}
}

// --- SDN reads --------------------------------------------------------------

func TestTicketAuth_SDNReads(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	zones, err := c.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones: %v", err)
	}
	if len(zones) != 1 || zones[0].ID != "vlanz" {
		t.Fatalf("ListSDNZones = %+v, want one zone vlanz", zones)
	}

	zone, err := c.GetSDNZone(ctx, "vlanz")
	if err != nil {
		t.Fatalf("GetSDNZone: %v", err)
	}
	if zone.Type != "vlan" {
		t.Errorf("zone.Type = %q, want vlan", zone.Type)
	}

	// T-3701: the real endpoint is per-node, not per-zone — one call per
	// cluster member, each answering every zone assigned to it.
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		st, statusErr := c.ListNodeSDNZoneStatus(ctx, node)
		if statusErr != nil {
			t.Fatalf("ListNodeSDNZoneStatus(%s): %v", node, statusErr)
		}
		if len(st) != 1 || st[0].Zone != "vlanz" || st[0].Status != "ok" {
			t.Fatalf("ListNodeSDNZoneStatus(%s) = %+v, want one ok entry for vlanz", node, st)
		}
		if st[0].Node != node {
			t.Fatalf("ListNodeSDNZoneStatus(%s): entry.Node = %q, want %q (filled in from the request, real PVE's wire shape carries no node field)", node, st[0].Node, node)
		}
	}

	vnets, err := c.ListSDNVnets(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnets: %v", err)
	}
	if len(vnets) != 2 {
		t.Fatalf("ListSDNVnets: expected 2 vnets, got %+v", vnets)
	}

	vnet, err := c.GetSDNVnet(ctx, "vnet100")
	if err != nil {
		t.Fatalf("GetSDNVnet: %v", err)
	}
	if vnet.Tag != 100 {
		t.Errorf("vnet.Tag = %d, want 100", vnet.Tag)
	}

	subnets, err := c.ListSDNSubnets(ctx, "vnet100")
	if err != nil {
		t.Fatalf("ListSDNSubnets: %v", err)
	}
	if len(subnets) != 1 || subnets[0].CIDR != "10.100.0.0/24" {
		t.Fatalf("ListSDNSubnets = %+v, want one subnet 10.100.0.0/24", subnets)
	}

	subnet, err := c.GetSDNSubnet(ctx, "vnet100", "10.100.0.0-24")
	if err != nil {
		t.Fatalf("GetSDNSubnet: %v", err)
	}
	if subnet.Gateway != "10.100.0.1" {
		t.Errorf("subnet.Gateway = %q, want 10.100.0.1", subnet.Gateway)
	}
}

// setMockSDNZonesUnavailable flips pvemock's per-node
// MockOptions.SDNZonesUnavailable via its unauthenticated test/dev control
// plane (POST /mock/nodes/{node}/sdn-zones-unavailable), mirroring
// internal/change's own setSDNZoneStatusFail helper.
func setMockSDNZonesUnavailable(t *testing.T, apiURL, node string, unavailable bool) {
	t.Helper()
	body := fmt.Sprintf(`{"unavailable":%t}`, unavailable)
	resp, err := http.Post(apiURL+"/mock/nodes/"+node+"/sdn-zones-unavailable", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("setMockSDNZonesUnavailable: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setMockSDNZonesUnavailable: status %d", resp.StatusCode)
	}
}

// TestSDNZoneStatus_ReconcileAcrossDivergentNodes proves
// pve.ReconcileSDNZoneStatus against a *real* cross-node divergence served
// by pvemock (not a hand-rolled fake): vlanz names all three nodes as
// members, pve3 is flagged SDNZonesUnavailable (T-3701 — modeling PVE's own
// observed "local sdn network configuration is not yet generated" empty
// response, planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt),
// and pve1/pve2 answer normally. The reconciled result must report pve1/
// pve2 "ok" and pve3 "unknown" — not silently omit pve3, which a reader
// could mistake for "healthy".
func TestSDNZoneStatus_ReconcileAcrossDivergentNodes(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	setMockSDNZonesUnavailable(t, ts.URL, "pve3", true)

	zones, err := c.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones: %v", err)
	}

	byNode := make(map[string][]pve.SDNZoneStatus, 3)
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		st, err := c.ListNodeSDNZoneStatus(ctx, node)
		if err != nil {
			t.Fatalf("ListNodeSDNZoneStatus(%s): %v", node, err)
		}
		byNode[node] = st
	}
	if len(byNode["pve3"]) != 0 {
		t.Fatalf("pve3 (flagged unavailable) = %+v, want an empty response", byNode["pve3"])
	}

	reconciled := pve.ReconcileSDNZoneStatus(zones, []string{"pve1", "pve2", "pve3"}, byNode)
	got := reconciled["vlanz"]
	if len(got) != 3 {
		t.Fatalf("reconciled vlanz status = %+v, want 3 entries (one per member node)", got)
	}
	byGotNode := make(map[string]string, len(got))
	for _, e := range got {
		byGotNode[e.Node] = e.Status
	}
	want := map[string]string{"pve1": "ok", "pve2": "ok", "pve3": "unknown"}
	for node, wantStatus := range want {
		if byGotNode[node] != wantStatus {
			t.Errorf("reconciled vlanz status for %s = %q, want %q (full: %+v)", node, byGotNode[node], wantStatus, got)
		}
	}
}

// --- SDN "?pending=1" reads -------------------------------------------------

// TestTicketAuth_SDNPendingReads pins ListSDNZonesPending/ListSDNVnetsPending/
// ListSDNSubnetsPending's decode of real PVE's actual pending mechanism
// (planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt) — NOT
// SDNZone/SDNVnet/SDNSubnet.Pending, which the default (no-query-param) list
// view these functions deliberately avoid never carries against real PVE
// (debt sweep 2026-08-19, "internal/pve.SDNZone.Pending assumes a marker
// real PVE does not emit"). evpn-lab.yaml stages vlanz as pending=changed
// (mtu 1600 vs a running 1500), vnet-qinq1 and its subnet as pending=new,
// and leaves every other zone/vnet/subnet in sync.
func TestTicketAuth_SDNPendingReads(t *testing.T) {
	ts := newMockServer(t, fixtureEvpn)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	zones, err := c.ListSDNZonesPending(ctx)
	if err != nil {
		t.Fatalf("ListSDNZonesPending: %v", err)
	}
	zoneByID := map[string]pve.SDNPendingEntry{}
	for _, z := range zones {
		zoneByID[z.ID] = z
	}
	vlanz, ok := zoneByID["vlanz"]
	if !ok || vlanz.State != pve.PendingChanged {
		t.Fatalf("vlanz pending entry = %+v (ok=%v), want state=changed", vlanz, ok)
	}
	if vlanz.Fields["mtu"] != float64(1600) {
		t.Errorf("vlanz.Fields[mtu] = %v, want 1600 (the staged value, JSON-decoded as float64)", vlanz.Fields["mtu"])
	}
	// An in-sync object still appears in the "?pending=1" list (real PVE's
	// own behaviour, confirmed live against pvecube's labz — the evidence
	// file's §3) — it just carries no "state"/"pending" keys, decoding to
	// State == PendingNone and Fields == nil.
	simplez, simplezOK := zoneByID["simplez"]
	if !simplezOK || simplez.State != pve.PendingNone || simplez.Fields != nil {
		t.Errorf("simplez pending entry = %+v (ok=%v), want present with state=none and no fields", simplez, simplezOK)
	}

	vnets, err := c.ListSDNVnetsPending(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnetsPending: %v", err)
	}
	vnetByID := map[string]pve.SDNPendingEntry{}
	for _, v := range vnets {
		vnetByID[v.ID] = v
	}
	qinqVnet, ok := vnetByID["vnet-qinq1"]
	if !ok || qinqVnet.State != pve.PendingNew {
		t.Fatalf("vnet-qinq1 pending entry = %+v (ok=%v), want state=new", qinqVnet, ok)
	}
	if vlanVnet, ok := vnetByID["vnet-vlan1"]; !ok || vlanVnet.State != pve.PendingNone || vlanVnet.Fields != nil {
		t.Errorf("vnet-vlan1 pending entry = %+v (ok=%v), want present with state=none and no fields", vlanVnet, ok)
	}

	subnets, err := c.ListSDNSubnetsPending(ctx, "vnet-qinq1")
	if err != nil {
		t.Fatalf("ListSDNSubnetsPending: %v", err)
	}
	if len(subnets) != 1 || subnets[0].ID != "10.70.0.0-24" || subnets[0].State != pve.PendingNew {
		t.Fatalf("ListSDNSubnetsPending(vnet-qinq1) = %+v, want one subnet 10.70.0.0-24 state=new", subnets)
	}

	inSyncSubnets, err := c.ListSDNSubnetsPending(ctx, "vnet-vlan1")
	if err != nil {
		t.Fatalf("ListSDNSubnetsPending: %v", err)
	}
	if len(inSyncSubnets) != 1 || inSyncSubnets[0].ID != "10.60.0.0-24" || inSyncSubnets[0].State != pve.PendingNone {
		t.Fatalf("ListSDNSubnetsPending(vnet-vlan1) = %+v, want one in-sync subnet 10.60.0.0-24", inSyncSubnets)
	}
}

// TestTicketAuth_SDNControllerFabricPendingReads pins ListSDNControllersPending/
// ListSDNFabricsPending's decode of the real pve.Client wire path — the
// controller/fabric counterpart of TestTicketAuth_SDNPendingReads above,
// added by the debt-sweep 2026-08-19 follow-up ("SDNController.Pending and
// SDNFabric.Pending have the same gap [as SDNZone.Pending]", confirmed
// against pvecube's own perl source, planning/reports/evidence/
// pve-9.2.4-sdn-pending-state.txt §6). evpn-lab.yaml declares no
// controllers/fabrics at all, so this test creates one of each live through
// the real client first (mirroring how a caller actually stages one), then
// reads it back through both pending readers — proving the sdnPendingWire
// decode's controller/fabric identity fields (added alongside
// zone/vnet/subnet's) round-trip correctly, not just that the mock's raw
// JSON shape is right (internal/pvemock's own
// TestSDNPending_ControllerAndFabric already pins that).
func TestTicketAuth_SDNControllerFabricPendingReads(t *testing.T) {
	ts := newMockServer(t, fixtureEvpn)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	if err := c.CreateSDNController(ctx, pve.SDNController{ID: "pctl", Type: "faucet"}); err != nil {
		t.Fatalf("CreateSDNController: %v", err)
	}
	if err := c.CreateSDNFabric(ctx, pve.SDNFabric{ID: "pfab", Protocol: "bgp"}); err != nil {
		t.Fatalf("CreateSDNFabric: %v", err)
	}

	controllers, err := c.ListSDNControllersPending(ctx)
	if err != nil {
		t.Fatalf("ListSDNControllersPending: %v", err)
	}
	ctlByID := map[string]pve.SDNPendingEntry{}
	for _, ce := range controllers {
		ctlByID[ce.ID] = ce
	}
	pctl, ok := ctlByID["pctl"]
	if !ok || pctl.State != pve.PendingNew {
		t.Fatalf("pctl pending entry = %+v (ok=%v), want state=new", pctl, ok)
	}
	if pctl.Fields["type"] != "faucet" {
		t.Errorf("pctl.Fields[type] = %v, want %q", pctl.Fields["type"], "faucet")
	}

	fabrics, err := c.ListSDNFabricsPending(ctx)
	if err != nil {
		t.Fatalf("ListSDNFabricsPending: %v", err)
	}
	fabByID := map[string]pve.SDNPendingEntry{}
	for _, fe := range fabrics {
		fabByID[fe.ID] = fe
	}
	pfab, ok := fabByID["pfab"]
	if !ok || pfab.State != pve.PendingNew {
		t.Fatalf("pfab pending entry = %+v (ok=%v), want state=new", pfab, ok)
	}
	if pfab.Fields["protocol"] != "bgp" {
		t.Errorf("pfab.Fields[protocol] = %v, want %q", pfab.Fields["protocol"], "bgp")
	}

	// ApplySDN clears both, mirroring TestTicketAuth_SDNPendingReads'
	// zone/vnet/subnet precedent (real PVE clears pending state through
	// exactly the same PUT /cluster/sdn commit for every SDN family).
	if _, applyErr := c.ApplySDN(ctx); applyErr != nil {
		t.Fatalf("ApplySDN: %v", applyErr)
	}
	controllers, err = c.ListSDNControllersPending(ctx)
	if err != nil {
		t.Fatalf("ListSDNControllersPending after apply: %v", err)
	}
	for _, ce := range controllers {
		if ce.ID == "pctl" && ce.State != pve.PendingNone {
			t.Errorf("pctl still pending after apply: %+v", ce)
		}
	}
	fabrics, err = c.ListSDNFabricsPending(ctx)
	if err != nil {
		t.Fatalf("ListSDNFabricsPending after apply: %v", err)
	}
	for _, fe := range fabrics {
		if fe.ID == "pfab" && fe.State != pve.PendingNone {
			t.Errorf("pfab still pending after apply: %+v", fe)
		}
	}
}

// --- firewall reads across all three scopes --------------------------------

func TestTicketAuth_FirewallReadsAllScopes(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	// Cluster scope.
	clusterRules, err := c.ListFirewallRules(ctx, pve.ClusterFirewallScope())
	if err != nil {
		t.Fatalf("ListFirewallRules (cluster): %v", err)
	}
	if len(clusterRules) != 1 || clusterRules[0].Dport != "22" {
		t.Fatalf("ListFirewallRules (cluster) = %+v, want one SSH rule", clusterRules)
	}

	rule, err := c.GetFirewallRule(ctx, pve.ClusterFirewallScope(), 0)
	if err != nil {
		t.Fatalf("GetFirewallRule (cluster): %v", err)
	}
	if rule.Action != "ACCEPT" {
		t.Errorf("rule.Action = %q, want ACCEPT", rule.Action)
	}

	opts, err := c.GetFirewallOptions(ctx, pve.ClusterFirewallScope())
	if err != nil {
		t.Fatalf("GetFirewallOptions (cluster): %v", err)
	}
	if !opts.Enable || opts.PolicyIn != "DROP" {
		t.Errorf("GetFirewallOptions (cluster) = %+v, want enabled with policy_in DROP", opts)
	}

	aliases, err := c.ListFirewallAliases(ctx, pve.ClusterFirewallScope())
	if err != nil {
		t.Fatalf("ListFirewallAliases (cluster): %v", err)
	}
	if len(aliases) == 0 {
		t.Fatalf("ListFirewallAliases (cluster): expected at least one alias")
	}

	alias, err := c.GetFirewallAlias(ctx, pve.ClusterFirewallScope(), aliases[0].Name)
	if err != nil {
		t.Fatalf("GetFirewallAlias: %v", err)
	}
	if alias.Name != aliases[0].Name {
		t.Errorf("GetFirewallAlias name = %q, want %q", alias.Name, aliases[0].Name)
	}

	// Node scope: reads must succeed even with an empty ruleset.
	if _, nodeErr := c.ListFirewallRules(ctx, pve.NodeFirewallScope("pve1")); nodeErr != nil {
		t.Fatalf("ListFirewallRules (node): %v", nodeErr)
	}
	// Node scope has no aliases/ipset endpoint at all on real PVE (hardware
	// validation, T-608) — a node's own host firewall can only reference
	// cluster-defined aliases/ipsets, never define its own. This must keep
	// erroring, not silently start succeeding again.
	if _, nodeErr := c.ListFirewallIPSets(ctx, pve.NodeFirewallScope("pve1")); nodeErr == nil {
		t.Fatalf("ListFirewallIPSets (node): want an error (real PVE has no node-scoped ipset endpoint), got success")
	}

	// Guest scope.
	if _, guestErr := c.ListFirewallRules(ctx, pve.GuestFirewallScope("pve1", pve.GuestQemu, 200)); guestErr != nil {
		t.Fatalf("ListFirewallRules (guest): %v", guestErr)
	}

	// Cluster-only security groups.
	groups, err := c.ListFirewallGroups(ctx)
	if err != nil {
		t.Fatalf("ListFirewallGroups: %v", err)
	}
	_ = groups // fixture may or may not define groups; just confirm the call succeeds.
}

// TestTicketAuth_VNetFirewallReadWriteAndNoObjects is T-3103's own version
// of TestTicketAuth_FirewallReadsAllScopes: vnet scope is a fourth firewall
// scope (/cluster/sdn/vnets/{vnet}/firewall), hardware-captured to expose
// only rules+options — no aliases/ipset endpoint at all, the same gap node
// scope has (T-608). This proves both the positive (rules/options CRUD
// round-trips, including the "forward" direction and policy_forward/
// log_level_forward — real fields nothing else in this scope's options has)
// and the negative (aliases/ipset must keep 404ing, not silently start
// succeeding).
func TestTicketAuth_VNetFirewallReadWriteAndNoObjects(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()
	scope := pve.VnetFirewallScope("vnet100")

	// Starts empty — the fixture doesn't seed vnet100's firewall.
	rules, err := c.ListFirewallRules(ctx, scope)
	if err != nil {
		t.Fatalf("ListFirewallRules (vnet): %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("ListFirewallRules (vnet) = %+v, want empty", rules)
	}

	if createErr := c.CreateFirewallRule(ctx, scope, pve.FirewallRule{
		Type: "forward", Action: "ACCEPT", Source: "10.100.0.0/24", Comment: "T-3103", Enabled: true,
	}); createErr != nil {
		t.Fatalf("CreateFirewallRule (vnet, forward): %v", createErr)
	}
	rules, err = c.ListFirewallRules(ctx, scope)
	if err != nil {
		t.Fatalf("ListFirewallRules (vnet) after create: %v", err)
	}
	if len(rules) != 1 || rules[0].Type != "forward" || rules[0].Action != "ACCEPT" {
		t.Fatalf("ListFirewallRules (vnet) = %+v, want one forward/ACCEPT rule", rules)
	}

	policyForward, logLevelForward := "DROP", "debug"
	if updErr := c.UpdateFirewallOptions(ctx, scope, pve.FirewallOptionsUpdate{
		PolicyForward: &policyForward, LogLevelForward: &logLevelForward,
	}); updErr != nil {
		t.Fatalf("UpdateFirewallOptions (vnet): %v", updErr)
	}
	opts, err := c.GetFirewallOptions(ctx, scope)
	if err != nil {
		t.Fatalf("GetFirewallOptions (vnet): %v", err)
	}
	if opts.PolicyForward != "DROP" || opts.LogLevelForward != "debug" {
		t.Fatalf("GetFirewallOptions (vnet) = %+v, want policy_forward=DROP log_level_forward=debug", opts)
	}

	// No aliases/ipset endpoint at vnet scope (hardware-captured — see
	// FwScopeVNet's doc comment). Must keep erroring, mirroring node scope's
	// own assertion above.
	if _, err := c.ListFirewallAliases(ctx, scope); err == nil {
		t.Fatalf("ListFirewallAliases (vnet): want an error (real PVE has no vnet-scoped aliases endpoint), got success")
	}
	if _, err := c.ListFirewallIPSets(ctx, scope); err == nil {
		t.Fatalf("ListFirewallIPSets (vnet): want an error (real PVE has no vnet-scoped ipset endpoint), got success")
	}
}

// --- IPAM reads --------------------------------------------------------------

func TestIPAMReads_ThreeNodeVlan(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	ipams, err := c.ListIPAMs(ctx)
	if err != nil {
		t.Fatalf("ListIPAMs: %v", err)
	}
	if len(ipams) != 1 || ipams[0].ID != "pve" || ipams[0].Type != "pve" {
		t.Fatalf("ListIPAMs = %+v, want exactly the built-in pve instance", ipams)
	}

	entries, err := c.GetIPAMStatus(ctx, "pve")
	if err != nil {
		t.Fatalf("GetIPAMStatus: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("GetIPAMStatus: %d entries, want 3 (fixture)", len(entries))
	}
	byIP := map[string]pve.IPAMEntry{}
	for _, e := range entries {
		byIP[e.IP] = e
	}
	gw, ok := byIP["10.100.0.1"]
	if !ok || !gw.Gateway || gw.Vnet != "vnet100" || gw.Subnet != "10.100.0.0/24" || gw.Zone != "vlanz" {
		t.Errorf("gateway entry = %+v, want gateway=true vnet100 10.100.0.0/24 vlanz", gw)
	}
	guest, ok := byIP["10.100.0.50"]
	if !ok || guest.Gateway || guest.Hostname != "app01" || guest.VMID != 200 || guest.MAC != "BC:24:11:AA:02:C8" {
		t.Errorf("guest entry = %+v, want app01/vmid 200 with its net0 MAC, not a gateway", guest)
	}
	if _, ok := byIP["10.200.0.1"]; !ok {
		t.Errorf("missing the vnet200 gateway entry in %+v", entries)
	}

	// Unknown instance -> typed request error, not a decode failure.
	if _, err := c.GetIPAMStatus(ctx, "netbox"); err == nil {
		t.Fatalf("GetIPAMStatus(netbox): expected an error for an unconfigured instance")
	}
}

func TestIPAMReads_EvpnLab(t *testing.T) {
	ts := newMockServer(t, fixtureEvpn)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	ipams, err := c.ListIPAMs(ctx)
	if err != nil {
		t.Fatalf("ListIPAMs: %v", err)
	}
	if len(ipams) != 1 || ipams[0].ID != "pve" {
		t.Fatalf("ListIPAMs = %+v, want the pve instance", ipams)
	}

	entries, err := c.GetIPAMStatus(ctx, "pve")
	if err != nil {
		t.Fatalf("GetIPAMStatus: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("GetIPAMStatus: %d entries, want 3 (gateway + two DHCP testers)", len(entries))
	}
	var gateways, allocations int
	for _, e := range entries {
		if e.Zone != "evpnz" || e.Vnet != "vnet-tenant-a" || e.Subnet != "192.168.50.0/24" {
			t.Errorf("entry %+v not scoped to the evpn tenant subnet", e)
		}
		if e.Gateway {
			gateways++
		} else {
			allocations++
			if e.VMID == 0 {
				t.Errorf("allocation %+v has no vmid", e)
			}
		}
	}
	if gateways != 1 || allocations != 2 {
		t.Fatalf("gateways=%d allocations=%d, want 1 and 2", gateways, allocations)
	}
}

// --- GET /access/permissions -------------------------------------------------

func TestPermissions_AgainstMock(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "auditor@pve", "readonly")

	perms, err := c.Permissions(context.Background())
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	root, ok := perms["/"]
	if !ok {
		t.Fatalf("Permissions = %+v, want a \"/\" path entry", perms)
	}
	for _, priv := range []string{"Sys.Audit", "VM.Audit", "SDN.Audit"} {
		if !root[priv] {
			t.Errorf("Permissions[/][%s] = false, want true (fixture auditor grant)", priv)
		}
	}
	if root["Sys.Modify"] {
		t.Errorf("Permissions[/] grants Sys.Modify to the read-only auditor: %+v", root)
	}
}

// --- GET /nodes/{node}/tasks/{upid}/log ---------------------------------------

func TestGetTaskLog_AgainstMock(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	upid, err := c.ReloadNodeNetwork(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReloadNodeNetwork: %v", err)
	}
	if _, waitErr := c.WaitTask(ctx, "pve1", upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 2 * time.Second}); waitErr != nil {
		t.Fatalf("WaitTask: %v", waitErr)
	}

	lines, err := c.GetTaskLog(ctx, "pve1", upid)
	if err != nil {
		t.Fatalf("GetTaskLog: %v", err)
	}
	if len(lines) < 2 {
		t.Fatalf("GetTaskLog: %d lines, want at least 2 (start + completion)", len(lines))
	}
	for i, l := range lines {
		if l.N != i+1 {
			t.Errorf("line %d has n=%d, want sequential numbering from 1", i, l.N)
		}
		if l.T == "" {
			t.Errorf("line %d has empty text", i)
		}
	}

	if _, err := c.GetTaskLog(ctx, "pve1", "UPID:bogus"); err == nil {
		t.Fatalf("GetTaskLog(bogus): expected an error for an unknown task")
	}
}

// --- IPSet entries: exercised against whichever fixture defines one --------

func TestTicketAuth_IPSetEntries(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	ctx := context.Background()

	sets, err := c.ListFirewallIPSets(ctx, pve.ClusterFirewallScope())
	if err != nil {
		t.Fatalf("ListFirewallIPSets: %v", err)
	}
	if len(sets) == 0 {
		t.Skip("fixture defines no cluster-scope ipsets")
	}
	entries, err := c.ListFirewallIPSetEntries(ctx, pve.ClusterFirewallScope(), sets[0].Name)
	if err != nil {
		t.Fatalf("ListFirewallIPSetEntries: %v", err)
	}
	_ = entries
}

// --- API-token auth ---------------------------------------------------------
//
// internal/pvemock implements PVE API-token authentication driven by
// fixture-declared tokens (users[].tokens in testdata/clusters/*.yaml),
// with token privileges following the owning user. The success path is
// integration tested against the mock below; the header-shaping stub test
// additionally pins the exact Authorization header bytes independent of
// the mock's parser. Whether real PVE accepts exactly this shape (and how
// token privilege separation, which the mock does not model, affects the
// effective privileges) needs hardware validation.

func TestAPIToken_SendsDocumentedAuthorizationHeader(t *testing.T) {
	var gotAuth string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.Contains(r.Header.Get("Cookie"), "PVEAuthCookie") {
			t.Errorf("token-mode request unexpectedly carried a PVEAuthCookie cookie")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"type":"cluster","name":"stub"}]}`))
	}))
	defer stub.Close()

	c, err := pve.New(pve.Config{
		APIURL:     stub.URL,
		Auth:       pve.AuthAPIToken,
		TokenValue: "vnprox@pve!daemon=11111111-2222-3333-4444-555555555555",
	})
	if err != nil {
		t.Fatalf("pve.New (token): %v", err)
	}

	status, err := c.ClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}
	if len(status) != 1 || status[0].Name != "stub" {
		t.Fatalf("ClusterStatus = %+v, want one stub entry", status)
	}

	want := "PVEAPIToken=vnprox@pve!daemon=11111111-2222-3333-4444-555555555555"
	if gotAuth != want {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, want)
	}
}

// TestAPIToken_FullReadSurfaceAgainstMock is T-101 acceptance criterion
// 1's token-mode half: the fixture-declared root@pam!daemon token performs
// the daemon read-poll surface (cluster, node network, guests, SDN, IPAM,
// firewall, permissions) against the mock, all over genuine
// Authorization-header auth — no ticket, no cookie, no CSRF.
func TestAPIToken_FullReadSurfaceAgainstMock(t *testing.T) {
	ts := newMockServer(t, fixtureThreeNode)
	c, err := pve.New(pve.Config{
		APIURL:     ts.URL,
		Auth:       pve.AuthAPIToken,
		TokenValue: "root@pam!daemon=4f9d21c7-3a80-4b6e-b1d2-95c8e7a40f13",
	})
	if err != nil {
		t.Fatalf("pve.New (token): %v", err)
	}
	ctx := context.Background()

	status, err := c.ClusterStatus(ctx)
	if err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}
	if len(status) != 4 { // cluster row + 3 nodes
		t.Fatalf("ClusterStatus rows = %d, want 4", len(status))
	}
	if _, resErr := c.ClusterResources(ctx); resErr != nil {
		t.Fatalf("ClusterResources: %v", resErr)
	}
	ifaces, err := c.ListNodeNetwork(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListNodeNetwork: %v", err)
	}
	if len(ifaces) == 0 {
		t.Fatalf("ListNodeNetwork: expected interfaces")
	}
	if _, guestErr := c.GetGuestConfig(ctx, "pve1", pve.GuestQemu, 200); guestErr != nil {
		t.Fatalf("GetGuestConfig: %v", guestErr)
	}
	zones, err := c.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones: %v", err)
	}
	if len(zones) != 1 {
		t.Fatalf("ListSDNZones = %+v, want one zone", zones)
	}
	ipams, err := c.ListIPAMs(ctx)
	if err != nil {
		t.Fatalf("ListIPAMs: %v", err)
	}
	if len(ipams) != 1 || ipams[0].ID != "pve" {
		t.Fatalf("ListIPAMs = %+v, want the fixture's pve instance", ipams)
	}
	if _, fwErr := c.ListFirewallRules(ctx, pve.ClusterFirewallScope()); fwErr != nil {
		t.Fatalf("ListFirewallRules (cluster): %v", fwErr)
	}
	perms, err := c.Permissions(ctx)
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if !perms["/"]["*"] {
		t.Fatalf("Permissions = %+v, want the root wildcard grant at \"/\"", perms)
	}
}

// TestAPIToken_InvalidTokenRejected proves a token the fixture does not
// declare is rejected with a 401 mapped to *pve.ErrPVEAuth.
func TestAPIToken_InvalidTokenRejected(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	c, err := pve.New(pve.Config{
		APIURL:     ts.URL,
		Auth:       pve.AuthAPIToken,
		TokenValue: "root@pam!anything=deadbeef",
	})
	if err != nil {
		t.Fatalf("pve.New (token): %v", err)
	}

	_, err = c.ClusterStatus(context.Background())
	if err == nil {
		t.Fatalf("expected an invalid API token to be rejected, got no error")
	}
	var authErr *pve.ErrPVEAuth
	if !errors.As(err, &authErr) {
		t.Fatalf("errors.As(err, &authErr) failed; got %#v (%v)", err, err)
	}
}
