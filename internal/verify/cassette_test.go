// SPDX-License-Identifier: Apache-2.0

package verify

// cassette_test.go drives this package's PVE-facing seam through T-2502's
// replay server and the real internal/pve client, against recorded traffic
// rather than against a fake of our own.
//
// This is the difference the dependency on T-2502 buys. Every other test in
// this file's package proves that the checks agree with fakes this package
// wrote — which is worth something, and is exactly the property T-2108 found
// four green-but-wrong instances of. Here the interface conversion in
// adapters.go is exercised against bytes somebody observed: if PVE reports
// `autostart` as an int where a hand-written fixture would have written a
// bool, this is the test that notices.
//
// The cassette directory is pinned to one *version* subdirectory on purpose.
// pvecassette.LoadDir walks recursively, so pointing at testdata/cassettes/
// loads every recorded PVE version at once and fails on the first request two
// of them both answer. T-2502's author flagged this; the fix is to name the
// version.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// cassetteDir is one recorded PVE version's cassettes — never the parent.
var cassetteDir = filepath.Join("..", "pvemock", "testdata", "cassettes", "mock-three-node-vlan")

const cassetteToken = "vnprox@pve!verify=00000000-0000-0000-0000-000000000000"

// newCassetteCluster builds a ClusterProbe over the real PVE client, served
// by a replay server. fail controls whether an unmatched request fails the
// test immediately; the mock-detection test deliberately makes a request no
// cassette answers, so it opts out.
func newCassetteCluster(t *testing.T, fail bool) (ClusterProbe, *httptest.Server, *pvemock.ReplayServer) {
	t.Helper()
	opts := []pvemock.ReplayOption{}
	if fail {
		opts = append(opts, pvemock.WithUnmatchedFailer(t))
	}
	replay, err := pvemock.NewReplayServer(cassetteDir, opts...)
	if err != nil {
		t.Fatalf("NewReplayServer(%s): %v", cassetteDir, err)
	}
	ts := httptest.NewServer(replay)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{
		HTTPClient: ts.Client(),
		Logger:     discardLog(),
		APIURL:     ts.URL,
		Auth:       pve.AuthAPIToken,
		TokenValue: cassetteToken,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return PVEAdapter{Client: client}, ts, replay
}

// TestPVEAdapterAgainstRecordedTraffic covers the conversion in adapters.go
// against observed bytes.
func TestPVEAdapterAgainstRecordedTraffic(t *testing.T) {
	cluster, _, replay := newCassetteCluster(t, true)
	ctx := context.Background()

	nodes, err := cluster.Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("the recorded cluster has %d nodes, want 3: %+v", len(nodes), nodes)
	}
	// PVE reports online/local as 0/1 ints on the wire (see
	// internal/pve/types.go's clusterStatusWire, itself the product of a
	// hardware-validation surprise). A conversion that dropped either would
	// make every "is this node online" decision in this package wrong.
	var online, local int
	for _, n := range nodes {
		if n.Online {
			online++
		}
		if n.Local {
			local++
		}
		if n.Address == "" {
			t.Errorf("node %s came back with no address; the SAN-coverage check has nothing to compare against", n.Name)
		}
	}
	if online != 3 {
		t.Errorf("%d of 3 recorded nodes came back online; the 0/1-int conversion is wrong", online)
	}
	if local != 1 {
		t.Errorf("%d nodes came back local, want exactly 1", local)
	}

	ifaces, err := cluster.Interfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("Interfaces: %v", err)
	}
	byName := map[string]Iface{}
	for _, i := range ifaces {
		byName[i.Name] = i
	}
	bond, ok := byName["bond0"]
	if !ok {
		t.Fatalf("the recorded node has no bond0: %v", ifaces)
	}
	if bond.BondMode != "802.3ad" {
		t.Errorf("bond0's mode came back %q; the LACP check selects bonds on this field", bond.BondMode)
	}
	bridge, ok := byName["vmbr0"]
	if !ok {
		t.Fatalf("the recorded node has no vmbr0")
	}
	if !bridge.VlanAware {
		t.Error("vmbr0 came back VLAN-unaware; bridge_vlan_aware is a 0/1 int on the wire and the conversion dropped it")
	}
	if bridge.MTU != 1500 {
		t.Errorf("vmbr0's MTU came back %d", bridge.MTU)
	}

	if u := replay.Unmatched(); len(u) != 0 {
		t.Errorf("the adapter made requests no cassette answers: %v", u)
	}
	if replay.Served() < 2 {
		t.Errorf("the replay server answered only %d requests", replay.Served())
	}
}

// TestChecksRunAgainstRecordedTraffic drives real checks with the recorded
// cluster underneath them, so the seam between a check and PVE's actual wire
// shape is exercised rather than assumed.
func TestChecksRunAgainstRecordedTraffic(t *testing.T) {
	cluster, _, _ := newCassetteCluster(t, true)
	ctx := context.Background()
	nodes, err := cluster.Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}

	deps := healthyDeps()
	deps.Cluster = cluster
	deps.Nodes = nodes

	// The LACP check is the one that reads PVE and the kernel and compares
	// them, so it is the one worth running against recorded PVE bytes.
	out := runOne(ctx, checkByID(t, "iface.lacp_partner_observed"), deps, discardLog())
	if out.Status != StatusPass {
		t.Fatalf("iface.lacp_partner_observed against recorded traffic: %s — %s", out.Status, out.Detail)
	}
	if !strings.Contains(out.Detail, "bond") {
		t.Errorf("the verdict does not mention the bond it checked: %s", out.Detail)
	}

	// And the same check, with the kernel disagreeing with the recorded PVE
	// response: the bond PVE calls 802.3ad has negotiated with nobody.
	hostOf(&deps).files["/proc/net/bonding/bond0"] = strings.Replace(healthyBonding, "00:11:22:33:44:55", "00:00:00:00:00:00", 1)
	out = runOne(ctx, checkByID(t, "iface.lacp_partner_observed"), deps, discardLog())
	if out.Status != StatusFail {
		t.Errorf("with an unnegotiated bond under recorded PVE traffic, the check reported %s: %s", out.Status, out.Detail)
	}
}

// TestReplayServerIsDetectedAsAMock is the property T-2502's author asked for
// explicitly, and it is not a formality: cassettes are recorded from real
// PVE, so a replay run looks more like hardware than internal/pvemock does.
// It is still recorded traffic, and a report produced against it is not
// evidence about a live cluster.
func TestReplayServerIsDetectedAsAMock(t *testing.T) {
	// No failer: the detection probe deliberately asks for an endpoint no
	// cassette answers, which is itself part of what identifies the server.
	_, ts, _ := newCassetteCluster(t, false)

	verdict, err := DetectMock(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("DetectMock: %v", err)
	}
	if !verdict.IsMock {
		t.Fatal("a cassette replay server was not detected as a mock, so a replay run could be filed as hardware validation")
	}
	if !strings.Contains(verdict.Reason, "replay") && !strings.Contains(verdict.Reason, "pvemock") {
		t.Errorf("the reason does not name the signal that fired: %q", verdict.Reason)
	}
}

// TestFixtureServerIsDetectedAsAMock covers the other mock in the tree.
func TestFixtureServerIsDetectedAsAMock(t *testing.T) {
	fixture, err := pvemock.LoadFixture(filepath.Join("..", "..", "testdata", "clusters", "single-node.yaml"))
	if err != nil {
		t.Skipf("no single-node fixture to load: %v", err)
	}
	ts := httptest.NewServer(pvemock.NewServer(fixture, pvemock.WithLogger(discardLog())))
	defer ts.Close()

	verdict, err := DetectMock(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("DetectMock: %v", err)
	}
	if !verdict.IsMock {
		t.Fatal("internal/pvemock's fixture server was not detected as a mock")
	}
}

// TestARealPVEShapedEndpointIsNotDetectedAsAMock is the control.
//
// Without it, a DetectMock that returned true unconditionally would pass
// every test above — and would make the suite unusable on the hardware it
// exists for.
func TestARealPVEShapedEndpointIsNotDetectedAsAMock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The shape a real pveproxy answers GET /api2/json/version with.
		_, _ = w.Write([]byte(`{"data":{"version":"9.2.4","release":"9.2","repoid":"abc123"}}`))
	}))
	defer ts.Close()

	verdict, err := DetectMock(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("DetectMock: %v", err)
	}
	if verdict.IsMock {
		t.Fatalf("a real-PVE-shaped endpoint was refused as a mock (%s), which would make the suite unusable on hardware", verdict.Reason)
	}
	if verdict.Version != "9.2.4/9.2/abc123" {
		t.Errorf("the endpoint's version was read as %q", verdict.Version)
	}
}

// TestUnreachableEndpointIsAnErrorNotANonMock: failing open here would let an
// unreachable endpoint sail past the guard, and every check would then skip
// while the report's environment claimed real hardware.
func TestUnreachableEndpointIsAnErrorNotANonMock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	if _, err := DetectMock(context.Background(), client, url); err == nil {
		t.Error("DetectMock reported a verdict for an endpoint it could not reach")
	}
}

func checkByID(t *testing.T, id string) Check {
	t.Helper()
	for _, c := range Checks() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check with id %q", id)
	return Check{}
}
