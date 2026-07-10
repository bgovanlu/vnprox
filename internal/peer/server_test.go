package peer

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/host"
)

// mountedTestServer wires a Server's routes onto a fresh chi.Router and
// returns an httptest.Server serving it, so tests exercise the exact same
// routing/middleware chain production wiring uses (internal/api's
// router.go mounts Server.MountRoutes the same way).
func mountedTestServer(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	srv.MountRoutes(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// twoDaemonHarness stands up two independent Server instances (each with
// its own fixture-backed HostReader/HostWriter, as distinct "hosts" per
// T-301 AC1) sharing one cluster secret, and a Client configured to talk to
// both via discovery-free explicit Peer values (Client.Peers'
// PVE-cluster-status discovery is exercised separately in client_test.go).
type twoDaemonHarness struct {
	now     time.Time
	readerA *spyHostReader
	readerB *spyHostReader
	writerA *spyHostWriter
	writerB *spyHostWriter
	client  *Client
	nodeA   Peer
	nodeB   Peer
	secret  []byte
}

func newTwoDaemonHarness(t *testing.T) *twoDaemonHarness {
	t.Helper()
	h := &twoDaemonHarness{secret: testSecret, now: time.Unix(1_700_000_000, 0)}

	srvA, readerA, writerA := newTestServer(t, func() time.Time { return h.now })
	srvB, readerB, writerB := newTestServer(t, func() time.Time { return h.now })
	h.readerA, h.writerA = readerA, writerA
	h.readerB, h.writerB = readerB, writerB

	tsA := mountedTestServer(t, srvA)
	tsB := mountedTestServer(t, srvB)

	h.nodeA = Peer{Node: "pve1", Addr: tsA.Listener.Addr().String()}
	h.nodeB = Peer{Node: "pve2", Addr: tsB.Listener.Addr().String()}

	h.client = NewClient(ClientOptions{
		Secrets: newStaticSecretStore(h.secret),
		Scheme:  "http", // httptest.Server is plain HTTP; TLS is net/http's own concern, not this package's
		Logger:  discardLogger(),
		Now:     func() time.Time { return h.now },
	})
	return h
}

// TestTwoDaemonHarness_ReadEndpoints is T-301 AC1: "Two daemon instances
// (test harness, distinct fixture-backed hosts) exchange all read endpoints
// successfully."
func TestTwoDaemonHarness_ReadEndpoints(t *testing.T) {
	h := newTwoDaemonHarness(t)

	h.readerA.interfaces["pve1"] = "auto lo\niface lo inet loopback\n"
	h.readerA.lldp["pve1"] = []byte(`[{"chassisId":"aa:bb:cc:dd:ee:ff"}]`)
	h.readerA.stats["pve1"] = map[string]host.IfaceStats{"eno1": {RxBytes: 1234}}

	h.readerB.interfaces["pve2"] = "auto lo\niface lo inet loopback\n# node B\n"
	h.readerB.lldp["pve2"] = []byte(`[{"chassisId":"11:22:33:44:55:66"}]`)
	h.readerB.stats["pve2"] = map[string]host.IfaceStats{"eno1": {RxBytes: 5678}}

	ctx := t.Context()

	content, err := h.client.Interfaces(ctx, h.nodeA, "pve1", false)
	if err != nil {
		t.Fatalf("Interfaces(nodeA): %v", err)
	}
	if content != h.readerA.interfaces["pve1"] {
		t.Errorf("Interfaces(nodeA) = %q, want %q", content, h.readerA.interfaces["pve1"])
	}

	lldp, err := h.client.LLDP(ctx, h.nodeA, "pve1")
	if err != nil {
		t.Fatalf("LLDP(nodeA): %v", err)
	}
	if string(lldp) != string(h.readerA.lldp["pve1"]) {
		t.Errorf("LLDP(nodeA) = %s, want %s", lldp, h.readerA.lldp["pve1"])
	}

	stats, err := h.client.Stats(ctx, h.nodeA, "pve1")
	if err != nil {
		t.Fatalf("Stats(nodeA): %v", err)
	}
	if stats["eno1"].RxBytes != 1234 {
		t.Errorf("Stats(nodeA)[eno1].RxBytes = %d, want 1234", stats["eno1"].RxBytes)
	}

	// Same three reads against the second, distinct daemon/fixture.
	content, err = h.client.Interfaces(ctx, h.nodeB, "pve2", false)
	if err != nil {
		t.Fatalf("Interfaces(nodeB): %v", err)
	}
	if content != h.readerB.interfaces["pve2"] {
		t.Errorf("Interfaces(nodeB) = %q, want %q", content, h.readerB.interfaces["pve2"])
	}

	lldp, err = h.client.LLDP(ctx, h.nodeB, "pve2")
	if err != nil {
		t.Fatalf("LLDP(nodeB): %v", err)
	}
	if string(lldp) != string(h.readerB.lldp["pve2"]) {
		t.Errorf("LLDP(nodeB) = %s, want %s", lldp, h.readerB.lldp["pve2"])
	}

	stats, err = h.client.Stats(ctx, h.nodeB, "pve2")
	if err != nil {
		t.Fatalf("Stats(nodeB): %v", err)
	}
	if stats["eno1"].RxBytes != 5678 {
		t.Errorf("Stats(nodeB)[eno1].RxBytes = %d, want 5678", stats["eno1"].RxBytes)
	}

	if h.readerA.interfacesCalls != 1 || h.readerA.lldpCalls != 1 || h.readerA.statsCalls != 1 {
		t.Errorf("readerA call counts = %+v, want one of each", h.readerA)
	}
	if h.readerB.interfacesCalls != 1 || h.readerB.lldpCalls != 1 || h.readerB.statsCalls != 1 {
		t.Errorf("readerB call counts = %+v, want one of each", h.readerB)
	}
}

func TestTwoDaemonHarness_WriteEndpoints(t *testing.T) {
	h := newTwoDaemonHarness(t)
	ctx := t.Context()

	if err := h.client.StageInterfaces(ctx, h.nodeA, "pve1", "new content"); err != nil {
		t.Fatalf("StageInterfaces: %v", err)
	}
	if got := h.writerA.staged["pve1"]; got != "new content" {
		t.Errorf("staged content = %q, want %q", got, "new content")
	}

	if err := h.client.Ifreload(ctx, h.nodeA, "pve1"); err != nil {
		t.Fatalf("Ifreload: %v", err)
	}
	if len(h.writerA.reloaded) != 1 || h.writerA.reloaded[0] != "pve1" {
		t.Errorf("reloaded = %v, want [pve1]", h.writerA.reloaded)
	}

	if err := h.client.Restore(ctx, h.nodeA, "pve1", "restored content"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := h.writerA.restored["pve1"]; got != "restored content" {
		t.Errorf("restored content = %q, want %q", got, "restored content")
	}
}

func TestTwoDaemonHarness_HealthAndVersion(t *testing.T) {
	h := newTwoDaemonHarness(t)
	ctx := t.Context()

	if err := h.client.Health(ctx, h.nodeA); err != nil {
		t.Fatalf("Health: %v", err)
	}

	v, err := h.client.Version(ctx, h.nodeA)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v.ProtocolVersion != ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", v.ProtocolVersion, ProtocolVersion)
	}
	if v.Version != "test" {
		t.Errorf("Version = %q, want %q", v.Version, "test")
	}
}

// TestServer_UnconfiguredReaderWriter503s covers the nil-Reader/Writer
// guard paths (a peer server built without full wiring, e.g. a future
// caller that only wants health/version) rather than panicking.
func TestServer_UnconfiguredReaderWriter503s(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Version: "test",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	p := Peer{Node: "solo", Addr: ts.Listener.Addr().String()}

	if _, err := client.Interfaces(t.Context(), p, "solo", false); err == nil {
		t.Fatal("expected an error with no Reader configured")
	}
	if err := client.StageInterfaces(t.Context(), p, "solo", "x"); err == nil {
		t.Fatal("expected an error with no Writer configured")
	}
}
