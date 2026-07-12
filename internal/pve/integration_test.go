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

	zoneStatus, err := c.GetSDNZoneStatus(ctx, "vlanz")
	if err != nil {
		t.Fatalf("GetSDNZoneStatus: %v", err)
	}
	if len(zoneStatus) != 3 {
		t.Fatalf("GetSDNZoneStatus: expected 3 node rows, got %+v", zoneStatus)
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

	statusEntries, err := c.GetSDNStatus(ctx)
	if err != nil {
		t.Fatalf("GetSDNStatus: %v", err)
	}
	if len(statusEntries) != 1+2+2 { // zones + vnets + subnets
		t.Fatalf("GetSDNStatus: expected 5 entries, got %d: %+v", len(statusEntries), statusEntries)
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
	if _, nodeErr := c.ListFirewallIPSets(ctx, pve.NodeFirewallScope("pve1")); nodeErr != nil {
		t.Fatalf("ListFirewallIPSets (node): %v", nodeErr)
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
