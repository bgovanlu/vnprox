package change_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func TestStaticPeerLocator(t *testing.T) {
	locator := change.StaticPeerLocator{"pve2": peer.Peer{Node: "pve2", Addr: "10.0.0.2:8007"}}

	p, err := locator.Peer(context.Background(), "pve2")
	if err != nil {
		t.Fatalf("Peer(pve2): %v", err)
	}
	if p.Addr != "10.0.0.2:8007" {
		t.Errorf("Peer(pve2).Addr = %q, want 10.0.0.2:8007", p.Addr)
	}

	if _, err := locator.Peer(context.Background(), "pve9"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("Peer(pve9) err = %v, want ErrUnknownPeerNode", err)
	}
}

// fakeClusterStatusSource is a minimal peer.ClusterStatusSource double for
// DiscoveringPeerLocator, avoiding a real PVE/pvemock round trip.
type fakeClusterStatusSource struct {
	err     error
	entries []pve.ClusterStatusEntry
}

func (s fakeClusterStatusSource) ClusterStatus(context.Context) ([]pve.ClusterStatusEntry, error) {
	return s.entries, s.err
}

func TestDiscoveringPeerLocator_ResolvesFromClusterStatus(t *testing.T) {
	client := peer.NewClient(peer.ClientOptions{
		Secrets: newTestSecretStoreForLocator(t),
		ClusterStatus: fakeClusterStatusSource{entries: []pve.ClusterStatusEntry{
			{Type: "node", Name: "pve1", IP: "10.0.0.1", Local: true},
			{Type: "node", Name: "pve2", IP: "10.0.0.2"},
			{Type: "cluster", Name: "my-cluster"},
		}},
	})
	locator := change.NewDiscoveringPeerLocator(client)

	p, err := locator.Peer(context.Background(), "pve2")
	if err != nil {
		t.Fatalf("Peer(pve2): %v", err)
	}
	if p.Node != "pve2" || p.Addr == "" {
		t.Errorf("Peer(pve2) = %+v, want a resolved pve2 peer", p)
	}

	// pve1 is Local (this daemon's own node in a real cluster-status
	// response) — peer.Client.Peers deliberately excludes it, so it must
	// come back as unknown to the locator too.
	if _, err := locator.Peer(context.Background(), "pve1"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("Peer(pve1) err = %v, want ErrUnknownPeerNode (Local nodes are excluded from Peers())", err)
	}
	if _, err := locator.Peer(context.Background(), "pve9"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("Peer(pve9) err = %v, want ErrUnknownPeerNode", err)
	}
}

func TestDiscoveringPeerLocator_ClusterStatusError(t *testing.T) {
	client := peer.NewClient(peer.ClientOptions{
		Secrets:       newTestSecretStoreForLocator(t),
		ClusterStatus: fakeClusterStatusSource{err: errors.New("injected discovery failure")},
	})
	locator := change.NewDiscoveringPeerLocator(client)

	if _, err := locator.Peer(context.Background(), "pve2"); err == nil {
		t.Fatal("Peer(pve2) with a failing ClusterStatusSource: want an error, got nil")
	}
}

// TestClusterTimerAgent_LocalAndUnknownNode covers ClusterTimerAgent's local
// branch (routes straight into a *LocalTimerAgent, no network) and its
// unknown-node branch (locator failure short-circuits before ever touching
// the peer client) — both quick, self-contained, no peer.Server needed.
func TestClusterTimerAgent_LocalAndUnknownNode(t *testing.T) {
	local, _, _, _, clk := newLocalTimerHarness(t)
	client := peer.NewClient(peer.ClientOptions{Secrets: newTestSecretStoreForLocator(t)})
	locator := change.StaticPeerLocator{} // pve1 resolves locally; nothing else is known
	agent := change.NewClusterTimerAgent(func() string { return "pveX" }, local, client, locator)
	ctx := context.Background()

	// Local branch: routes straight to LocalTimerAgent.
	if _, err := agent.ArmTimer(ctx, "cs-1", "pveX", "content", clk.now().Add(time.Minute).Unix()); err != nil {
		t.Fatalf("ArmTimer(local): %v", err)
	}
	rec, err := agent.TimerStatus(ctx, "cs-1", "pveX")
	if err != nil {
		t.Fatalf("TimerStatus(local): %v", err)
	}
	if rec.Status != peer.TimerArmed {
		t.Errorf("TimerStatus(local) = %+v, want armed", rec)
	}
	if _, err := agent.CancelTimer(ctx, "cs-1", "pveX"); err != nil {
		t.Fatalf("CancelTimer(local): %v", err)
	}

	// Unknown-node branch: the locator fails before any network call.
	if _, err := agent.ArmTimer(ctx, "cs-1", "pveUnknown", "content", 0); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("ArmTimer(unknown node) err = %v, want ErrUnknownPeerNode", err)
	}
	if _, err := agent.CancelTimer(ctx, "cs-1", "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("CancelTimer(unknown node) err = %v, want ErrUnknownPeerNode", err)
	}
	if _, err := agent.TimerStatus(ctx, "cs-1", "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("TimerStatus(unknown node) err = %v, want ErrUnknownPeerNode", err)
	}
}

// TestClusterNodeAgent_UnknownNode covers ClusterNodeAgent's four methods'
// locator-error short-circuit, mirroring the timer-agent test above.
func TestClusterNodeAgent_UnknownNode(t *testing.T) {
	client := peer.NewClient(peer.ClientOptions{Secrets: newTestSecretStoreForLocator(t)})
	locator := change.StaticPeerLocator{}
	local := newMinimalNodeAgent()
	agent := change.NewClusterNodeAgent(func() string { return "pveX" }, local, client, locator)
	ctx := context.Background()

	if _, err := agent.ReadInterfaces(ctx, "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("ReadInterfaces(unknown) err = %v, want ErrUnknownPeerNode", err)
	}
	if err := agent.StageInterfaces(ctx, "pveUnknown", "x"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("StageInterfaces(unknown) err = %v, want ErrUnknownPeerNode", err)
	}
	if err := agent.ReloadInterfaces(ctx, "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("ReloadInterfaces(unknown) err = %v, want ErrUnknownPeerNode", err)
	}
	if err := agent.DiscardStaged(ctx, "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("DiscardStaged(unknown) err = %v, want ErrUnknownPeerNode", err)
	}

	// Local branch: routes straight to the local NodeAgent.
	if err := agent.StageInterfaces(ctx, "pveX", "staged content"); err != nil {
		t.Fatalf("StageInterfaces(local): %v", err)
	}
	if err := agent.ReloadInterfaces(ctx, "pveX"); err != nil {
		t.Fatalf("ReloadInterfaces(local): %v", err)
	}
	if got, err := agent.ReadInterfaces(ctx, "pveX"); err != nil || got != "staged content" {
		t.Errorf("ReadInterfaces(local) = (%q, %v), want (staged content, nil)", got, err)
	}
	if err := agent.DiscardStaged(ctx, "pveX"); err != nil {
		t.Fatalf("DiscardStaged(local): %v", err)
	}
}

// TestClusterNodeAgent_CheckNodeCompatible covers PeerCompatibilityChecker's
// three branches per docs/architecture.md §5: the local node never leaves
// the process (always compatible), a peer advertising this build's
// peer.ProtocolVersion is compatible, and a peer advertising a different
// one is refused with an error wrapping peer.ErrPeerIncompatible — this is
// the same building block beginApply (apply.go) uses for the mixed-version
// apply-refusal path exercised end to end in
// TestApply_RefusesWhenPeerProtocolIncompatible (mixedversion_test.go).
func TestClusterNodeAgent_CheckNodeCompatible(t *testing.T) {
	secrets := newTestSecretStoreForLocator(t)
	client := peer.NewClient(peer.ClientOptions{Secrets: secrets, Scheme: "http"})
	local := newMinimalNodeAgent()

	// A real peer.Server always advertises the current build's
	// ProtocolVersion, so it's the "compatible peer" fixture.
	compatSrv := peer.NewServer(peer.ServerOptions{Secrets: secrets, Version: "test"})
	compatRouter := chi.NewRouter()
	compatSrv.MountRoutes(compatRouter)
	compatTS := httptest.NewServer(compatRouter)
	t.Cleanup(compatTS.Close)

	// A bare stub answering only /api/peer/version, with a mismatched
	// protocolVersion — the same technique internal/peer's own
	// TestClient_CheckCompatible_VersionMismatch uses, since ProtocolVersion
	// is a package constant a real peer.Server can't be told to override.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/peer/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"old","protocolVersion":` + strconv.Itoa(peer.ProtocolVersion+1) + `}`))
	})
	incompatTS := httptest.NewServer(mux)
	t.Cleanup(incompatTS.Close)

	locator := change.StaticPeerLocator{
		"pve-compat":   peer.Peer{Node: "pve-compat", Addr: compatTS.Listener.Addr().String()},
		"pve-incompat": peer.Peer{Node: "pve-incompat", Addr: incompatTS.Listener.Addr().String()},
	}
	agent := change.NewClusterNodeAgent(func() string { return "pveX" }, local, client, locator)
	ctx := context.Background()

	if err := agent.CheckNodeCompatible(ctx, "pveX"); err != nil {
		t.Errorf("CheckNodeCompatible(local node) = %v, want nil", err)
	}
	if err := agent.CheckNodeCompatible(ctx, "pve-compat"); err != nil {
		t.Errorf("CheckNodeCompatible(matching-protocol peer) = %v, want nil", err)
	}
	if err := agent.CheckNodeCompatible(ctx, "pve-incompat"); !errors.Is(err, peer.ErrPeerIncompatible) {
		t.Errorf("CheckNodeCompatible(mismatched-protocol peer) = %v, want it to wrap peer.ErrPeerIncompatible", err)
	}
	if _, err := (change.StaticPeerLocator{}).Peer(ctx, "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("sanity: unknown-node lookup err = %v, want ErrUnknownPeerNode", err)
	}
	if err := agent.CheckNodeCompatible(ctx, "pveUnknown"); !errors.Is(err, change.ErrUnknownPeerNode) {
		t.Errorf("CheckNodeCompatible(unknown node) = %v, want ErrUnknownPeerNode", err)
	}
}

func newTestSecretStoreForLocator(t *testing.T) *peer.SecretStore {
	t.Helper()
	path := t.TempDir() + "/cluster.secret"
	secrets, err := peer.LoadOrGenerateSecret(path, nil)
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}
	return secrets
}
