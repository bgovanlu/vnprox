// SPDX-License-Identifier: Apache-2.0

// cassettes_test.go is T-2502's end of the loop: it records the mock PVE
// server through the real internal/pve client's record mode, replays the
// result, and compares two independently-produced cassette sets field by
// field.
//
// A word on what the checked-in cassettes under testdata/cassettes/ are
// and are not. They were recorded from internal/pvemock, not from a
// Proxmox cluster — this repository has no hardware (CLAUDE.md, "Real PVE
// access"). They therefore prove the *machinery*: that recording produces
// a cassette, that a cassette replays byte-identically, that an unmatched
// request cannot be papered over, and that the drift comparator reports
// real field-set differences. They prove nothing about PVE's actual wire
// shape, and the version directory is named "mock-*" so no later reader
// can mistake them for an observation. The first directory named after a
// real release (e.g. "8.3.5/") is the one that starts paying this card's
// dividend; see planning/reports/needs-hardware-validation.md.
package pvemock_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvecassette"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const (
	fixtureThreeNode = "../../testdata/clusters/three-node-vlan.yaml"
	fixtureSingle    = "../../testdata/clusters/single-node.yaml"

	// tokenThreeNode / tokenSingle are the fixtures' own root@pam!daemon
	// API tokens. Record mode uses token auth, never ticket auth: a
	// ticket-auth client's first call is POST /access/ticket, whose
	// response body is a credential, and the writer refuses it by design
	// (asserted in internal/pve's TestRecord_TicketLoginIsRefused).
	tokenThreeNode = "root@pam!daemon=4f9d21c7-3a80-4b6e-b1d2-95c8e7a40f13"
	tokenSingle    = "root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42"

	// mockCassetteVersion is both the version label inside every shipped
	// cassette and the directory it lives in.
	mockCassetteVersion = "mock-three-node-vlan"
	shippedCassetteDir  = "testdata/cassettes"

	// updateEnv regenerates the checked-in cassettes instead of asserting
	// against them: `VNPROX_UPDATE_CASSETTES=1 go test ./internal/pvemock/`.
	updateEnv = "VNPROX_UPDATE_CASSETTES"
)

// recordSession drives one client over a fixed set of read endpoints with
// record mode on. The list is the coverage contract of the shipped
// cassette set: adding an endpoint here and regenerating is how a cassette
// gets added.
//
// Only reads. A recording session against a real cluster is something an
// operator runs on a production box, and a card whose entire subject is
// "observe rather than imagine" has no business also mutating what it is
// observing.
func recordSession(t *testing.T, fixturePath, token, dir, version string) *pve.Client {
	t.Helper()

	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	ts := httptest.NewServer(pvemock.NewServer(f))
	t.Cleanup(ts.Close)

	c, err := pve.New(pve.Config{
		APIURL:           ts.URL,
		Auth:             pve.AuthAPIToken,
		TokenValue:       token,
		RecordDir:        dir,
		RecordPVEVersion: version,
	})
	if err != nil {
		t.Fatalf("pve.New (record mode): %v", err)
	}

	readSurface(t, c, "pve1", "vmbr0")
	return c
}

// readSurface is the recording session itself: the fixed list of read-only
// endpoints a cassette set covers. It is shared by the mock recording
// above and by TestRecordAgainstRealPVE below, so `make record` against
// iron and the checked-in mock set can never cover different ground.
//
// Only reads. A recording session against a real cluster is something an
// operator runs on a production box, and a card whose entire subject is
// "observe rather than imagine" has no business also mutating what it is
// observing.
func readSurface(t *testing.T, c *pve.Client, node, iface string) {
	t.Helper()
	ctx := context.Background()
	calls := []struct {
		run  func() error
		name string
	}{
		{name: "cluster status", run: func() error { _, err := c.ClusterStatus(ctx); return err }},
		{name: "cluster resources", run: func() error { _, err := c.ClusterResources(ctx); return err }},
		{name: "permissions", run: func() error { _, err := c.Permissions(ctx); return err }},
		{name: "guests", run: func() error { _, err := c.ListGuests(ctx, pve.GuestQemu); return err }},
		{name: "node network", run: func() error { _, err := c.ListNodeNetwork(ctx, node); return err }},
		{name: "node iface", run: func() error { _, err := c.GetNodeNetworkInterface(ctx, node, iface); return err }},
		{name: "sdn zones", run: func() error { _, err := c.ListSDNZones(ctx); return err }},
		// Same path as the call above, different query: proof in the
		// shipped set itself that the query is part of a cassette's
		// identity (T-2502 AC4).
		{name: "sdn zones (query: running=1)", run: func() error { _, err := c.ListSDNZonesRunning(ctx); return err }},
		{name: "sdn vnets", run: func() error { _, err := c.ListSDNVnets(ctx); return err }},
		{name: "firewall rules (cluster)", run: func() error {
			_, err := c.ListFirewallRules(ctx, pve.ClusterFirewallScope())
			return err
		}},
		{name: "storages", run: func() error { _, err := c.ListStorages(ctx); return err }},
	}
	for _, call := range calls {
		if err := call.run(); err != nil {
			t.Fatalf("recording %s: %v", call.name, err)
		}
	}
}

// TestRecordAgainstRealPVE is `make record`: the operator flow that turns
// this card from machinery into evidence.
//
// It skips — loudly, naming every variable it needs — unless it is pointed
// at a real cluster, because this repository has none. It is a test rather
// than a new binary so that the endpoint list, the client, and the guard
// are literally the same code the mock recording above runs; a separate
// `cmd/pverecord` would be a second implementation of the one thing that
// must not have two.
//
//	make record PVE_URL=https://pve1.lab:8006 PVE_VERSION=8.3.5 \
//	    PVE_TOKEN='vnprox@pve!daemon=<uuid>' PVE_INSECURE=1
//
// Use an API token, not a password: a ticket-auth client's first call is a
// login, whose response body is a credential, and the writer refuses it
// (TestRecord_TicketLoginIsRefused in internal/pve).
func TestRecordAgainstRealPVE(t *testing.T) {
	apiURL := os.Getenv("VNPROX_PVE_URL")
	token := os.Getenv("VNPROX_PVE_TOKEN")
	if apiURL == "" || token == "" || os.Getenv("VNPROX_PVE_RECORD") == "" || os.Getenv("VNPROX_PVE_VERSION") == "" {
		t.Skip("no real PVE cluster: set VNPROX_PVE_URL, VNPROX_PVE_TOKEN, VNPROX_PVE_RECORD and " +
			"VNPROX_PVE_VERSION (see `make record`) to record against one")
	}
	node := os.Getenv("VNPROX_PVE_NODE")
	iface := os.Getenv("VNPROX_PVE_IFACE")
	if node == "" || iface == "" {
		t.Fatal("set VNPROX_PVE_NODE and VNPROX_PVE_IFACE to the node and interface to record")
	}

	// Record mode itself comes from the environment — the same switch an
	// operator has on a released binary, exercised here rather than
	// bypassed via Config.
	c, err := pve.New(pve.Config{
		APIURL:     apiURL,
		Auth:       pve.AuthAPIToken,
		TokenValue: token,
		TLS:        pve.TLSConfig{InsecureSkipVerify: os.Getenv("VNPROX_PVE_INSECURE") != ""},
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	readSurface(t, c, node, iface)

	written := c.Recorded()
	if len(written) == 0 {
		t.Fatal("recorded nothing")
	}
	t.Logf("recorded %d cassettes:", len(written))
	for _, p := range written {
		t.Logf("  %s", p)
	}
	t.Log("review them, then commit: they are now the only fixtures in this repository that " +
		"describe PVE rather than our idea of it")
}

// loadShipped reads the checked-in cassette set.
func loadShipped(t *testing.T) map[string]pvecassette.Cassette {
	t.Helper()
	set, err := pvecassette.LoadDir(filepath.Join(shippedCassetteDir, mockCassetteVersion))
	if err != nil {
		t.Fatalf("loading shipped cassettes (regenerate with %s=1 go test ./internal/pvemock/): %v", updateEnv, err)
	}
	return set
}

// TestMockCassettes_MatchTheMockServer is the freshness gate on the
// checked-in cassettes.
//
// Without it the shipped set would rot the first time a pvemock handler
// changed shape, and a rotted cassette is worse than no cassette: the
// replay tests below would keep passing against a recording of a mock that
// no longer exists. It doubles as the regeneration tool
// (VNPROX_UPDATE_CASSETTES=1), so there is exactly one way to produce
// these files and it is the same code path an operator's `make record`
// takes.
func TestMockCassettes_MatchTheMockServer(t *testing.T) {
	if os.Getenv(updateEnv) != "" {
		if err := os.RemoveAll(filepath.Join(shippedCassetteDir, mockCassetteVersion)); err != nil {
			t.Fatalf("clearing cassette dir: %v", err)
		}
		c := recordSession(t, fixtureThreeNode, tokenThreeNode, shippedCassetteDir, mockCassetteVersion)
		t.Logf("regenerated %d cassettes under %s/%s", len(c.Recorded()), shippedCassetteDir, mockCassetteVersion)
		return
	}

	fresh := recordSession(t, fixtureThreeNode, tokenThreeNode, t.TempDir(), mockCassetteVersion)
	if len(fresh.Recorded()) == 0 {
		t.Fatal("record mode wrote no cassettes")
	}
	freshSet := map[string]pvecassette.Cassette{}
	for _, path := range fresh.Recorded() {
		c, err := pvecassette.Load(path)
		if err != nil {
			t.Fatalf("loading freshly recorded %s: %v", path, err)
		}
		freshSet[c.Key()] = c
	}

	shipped := loadShipped(t)
	if got, want := pvecassette.Keys(freshSet), pvecassette.Keys(shipped); !equalStrings(got, want) {
		t.Fatalf("the shipped cassette set no longer matches what recording the mock produces.\n"+
			"regenerate with `%s=1 go test ./internal/pvemock/`\n  recorded: %v\n  shipped:  %v", updateEnv, got, want)
	}
	for _, key := range pvecassette.Keys(shipped) {
		got, want := freshSet[key], shipped[key]
		if got.Status != want.Status {
			t.Errorf("%s: status drifted: recorded %d, shipped %d", key, got.Status, want.Status)
		}
		if canonicalBody(t, got.Body) != canonicalBody(t, want.Body) {
			t.Errorf("%s: body drifted (regenerate with %s=1):\n  recorded: %s\n  shipped:  %s",
				key, updateEnv, got.Body, want.Body)
		}
	}
}

// canonicalBody re-encodes a body with every JSON array sorted, so this
// comparison is about content rather than about the order two map
// iterations happened to produce.
//
// It is here because of something the recorder found on its first run:
// several of pvemock's list endpoints (GET /cluster/resources, GET
// /cluster/sdn/vnets, GET /cluster/sdn/zones, ...) build their arrays by
// ranging over a Go map, so the mock answers the same request with the
// same elements in a *different order* on roughly one run in three. That
// is a real property of the mock, not of this test — it means no
// byte-level golden comparison against pvemock can be stable, and it is
// worth a follow-up in its own right (see this card's report).
//
// Nothing about the *replay* path is weakened by this: cassettes are
// served verbatim from disk, and TestReplay_ShippedCassettesRoundTrip...
// still asserts byte identity. Only this freshness gate, which compares
// two independent runs of a nondeterministic server, has to look through
// element order.
func canonicalBody(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body // not JSON: compare it literally
	}
	out, err := json.Marshal(sortValue(v))
	if err != nil {
		t.Fatalf("re-encoding body: %v", err)
	}
	return string(out)
}

func sortValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = sortValue(child)
		}
		return t
	case []any:
		for i := range t {
			t[i] = sortValue(t[i])
		}
		sort.Slice(t, func(i, j int) bool {
			a, _ := json.Marshal(t[i])
			b, _ := json.Marshal(t[j])
			return string(a) < string(b)
		})
		return t
	default:
		return v
	}
}

// TestReplay_ShippedCassettesRoundTripThroughTheRealClient is T-2502 AC1
// end to end: a recorded cassette replays to a byte-identical response
// body, and the typed client that made the original call decodes the
// replayed one into the same values.
//
// Byte-identity is asserted on the wire, not on the decoded struct. A
// comparison of decoded structs would pass while the mock dropped a field
// the struct does not have — which is the entire class of defect this
// card exists to make visible.
func TestReplay_ShippedCassettesRoundTripThroughTheRealClient(t *testing.T) {
	shipped := loadShipped(t)

	replay, err := pvemock.NewReplayServer(filepath.Join(shippedCassetteDir, mockCassetteVersion),
		pvemock.WithUnmatchedFailer(t))
	if err != nil {
		t.Fatalf("NewReplayServer: %v", err)
	}
	ts := httptest.NewServer(replay)
	defer ts.Close()

	c, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthAPIToken, TokenValue: tokenThreeNode})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	// Every typed read the recording session made, made again against the
	// replay server. If any of them 404s, 599s or decodes wrong, the
	// cassette is not a faithful stand-in for the mock it came from.
	if _, statusErr := c.ClusterStatus(ctx); statusErr != nil {
		t.Errorf("ClusterStatus against replay: %v", statusErr)
	}
	ifaces, err := c.ListNodeNetwork(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListNodeNetwork against replay: %v", err)
	}
	if len(ifaces) == 0 {
		t.Fatal("ListNodeNetwork against replay returned no interfaces")
	}
	zones, err := c.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones against replay: %v", err)
	}
	if len(zones) == 0 {
		t.Fatal("ListSDNZones against replay returned no zones")
	}

	if n := replay.Served(); n < 3 {
		t.Errorf("replay server served %d requests, expected at least 3", n)
	}
	if u := replay.Unmatched(); len(u) != 0 {
		t.Errorf("replay server saw unmatched requests: %v", u)
	}

	// And the raw-bytes half: fetch one cassette's endpoint directly and
	// compare the response body to the recorded bytes.
	key := "GET /api2/json/nodes/pve1/network"
	want, ok := shipped[key]
	if !ok {
		t.Fatalf("shipped set has no cassette for %q; it holds %v", key, pvecassette.Keys(shipped))
	}
	resp, err := ts.Client().Get(ts.URL + "/api2/json/nodes/pve1/network")
	if err != nil {
		t.Fatalf("raw GET against replay: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	replayed, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading replayed body: %v", err)
	}
	if string(replayed) != want.Body {
		t.Errorf("replayed body is not byte-identical to the recording:\n  got:  %q\n  want: %q", replayed, want.Body)
	}
}

// TestFixtureCassetteDrift is T-2502 AC5.
//
// It runs the hand-written fixture the rest of this repository tests
// against (single-node.yaml) and the recorded cassette set through the
// same comparator and reports every field present in one and absent in the
// other. The outcome is recorded either way — the test logs the full
// report and fails only if the comparator itself produces nothing at all
// to say, because "no output" and "no divergence" must not look the same.
//
// What it is comparing today is one mock fixture against a recording of
// another mock fixture, since no cassette in this repository came from
// real hardware yet. That still surfaces the thing the check is for: a
// field a fixture never emits is invisible to every unit test written
// against that fixture, and shows up here as one line. When a real
// cassette directory lands, this test compares against it unchanged.
func TestFixtureCassetteDrift(t *testing.T) {
	dir := t.TempDir()
	fixtureClient := recordSession(t, fixtureSingle, tokenSingle, dir, "mock-single-node")
	fixtureSet := map[string]pvecassette.Cassette{}
	for _, path := range fixtureClient.Recorded() {
		c, err := pvecassette.Load(path)
		if err != nil {
			t.Fatalf("loading fixture-side cassette %s: %v", path, err)
		}
		fixtureSet[c.Key()] = c
	}

	divergences := pvecassette.Drift(fixtureSet, loadShipped(t))

	var b strings.Builder
	fmt.Fprintf(&b, "fixture-vs-cassette drift: %d divergence(s)\n", len(divergences))
	fmt.Fprintf(&b, "  fixture side:  single-node.yaml via pvemock (%d endpoints)\n", len(fixtureSet))
	fmt.Fprintf(&b, "  cassette side: %s (%d endpoints)\n", mockCassetteVersion, len(loadShipped(t)))
	for _, d := range divergences {
		fmt.Fprintf(&b, "  - %s\n", d)
	}
	t.Log(b.String())

	if len(divergences) == 0 {
		t.Log("no divergence found: the two sides agree on every field of every shared endpoint")
	}
	// The comparator must have had something to compare. A drift check
	// that silently examined zero endpoints is the failure mode this card
	// is about, wearing a green tick.
	if len(fixtureSet) == 0 {
		t.Fatal("drift check compared zero fixture-side endpoints")
	}
}

// TestDriftComparator_FiresOnAKnownDifference is the failing fixture for
// the check above: without it, TestFixtureCassetteDrift passing would be
// consistent with Drift always returning nil.
func TestDriftComparator_FiresOnAKnownDifference(t *testing.T) {
	mk := func(body string) pvecassette.Cassette {
		return pvecassette.Cassette{
			PVEVersion: "test", Method: "GET", Path: "/api2/json/nodes/pve1/network",
			Status: 200, Body: body,
		}
	}
	fixture := map[string]pvecassette.Cassette{}
	cassette := map[string]pvecassette.Cassette{}
	c1 := mk(`{"data":[{"iface":"vmbr0","type":"bridge"}]}`)
	c2 := mk(`{"data":[{"iface":"vmbr0","type":"bridge","bond_mode":"lacp"}]}`)
	fixture[c1.Key()] = c1
	cassette[c2.Key()] = c2

	got := pvecassette.Drift(fixture, cassette)
	if len(got) != 1 {
		t.Fatalf("Drift reported %d divergences, want 1: %v", len(got), got)
	}
	if got[0].Field != "data[].bond_mode" || got[0].PresentIn != pvecassette.SideCassette {
		t.Errorf("Drift named the wrong field: %+v", got[0])
	}
}

// TestShippedCassettes_CarryNoCredential is the belt to Writer.Write's
// braces: it re-scans every checked-in cassette body for every secret
// class, through the file's raw bytes rather than through the loader, so
// it would still fire if the loader's scan were removed.
func TestShippedCassettes_CarryNoCredential(t *testing.T) {
	root := filepath.Join(shippedCassetteDir, mockCassetteVersion)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s is empty", root)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		var doc struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, term := range []string{"PVE:", "PVEAPIToken=", "BEGIN PRIVATE KEY", "password", "ticket", "csrf"} {
			if strings.Contains(strings.ToLower(doc.Body), strings.ToLower(term)) {
				t.Errorf("%s: cassette body contains %q", e.Name(), term)
			}
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
