package peer

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakeClusterStatus is a minimal ClusterStatusSource stand-in for
// discovery tests.
type fakeClusterStatus struct {
	err     error
	entries []pve.ClusterStatusEntry
}

func (f *fakeClusterStatus) ClusterStatus(context.Context) ([]pve.ClusterStatusEntry, error) {
	return f.entries, f.err
}

func TestClient_PeersDiscoversFromClusterStatus(t *testing.T) {
	source := &fakeClusterStatus{entries: []pve.ClusterStatusEntry{
		{Type: "cluster", Name: "mycluster", Nodes: 3},
		{Type: "node", Name: "pve1", IP: "10.0.0.1", Local: true, Online: true},
		{Type: "node", Name: "pve2", IP: "10.0.0.2", Online: true},
		{Type: "node", Name: "pve3", IP: "10.0.0.3", Online: false},
	}}
	c := NewClient(ClientOptions{
		Secrets:       newStaticSecretStore(testSecret),
		ClusterStatus: source,
		Port:          8007,
		Logger:        discardLogger(),
	})

	peers, err := c.Peers(t.Context())
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("Peers() = %v, want 2 entries (local node and the cluster summary row excluded)", peers)
	}
	want := map[string]string{"pve2": "10.0.0.2:8007", "pve3": "10.0.0.3:8007"}
	for _, p := range peers {
		if want[p.Node] != p.Addr {
			t.Errorf("peer %s addr = %s, want %s", p.Node, p.Addr, want[p.Node])
		}
		delete(want, p.Node)
	}
	if len(want) != 0 {
		t.Errorf("missing expected peers: %v", want)
	}
}

func TestClient_PeersEmptyWhenNoClusterStatusSource(t *testing.T) {
	c := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Logger: discardLogger()})
	peers, err := c.Peers(t.Context())
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("Peers() = %v, want empty (single-node/no-cluster mode)", peers)
	}
}

// TestClient_DeadPeerFastFailsAndRecovers is T-301 AC3: "Circuit breaker:
// dead peer -> fast-fail + recovery on return; caller-visible error
// peer_unreachable."
func TestClient_DeadPeerFastFailsAndRecovers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv, reader, _ := newTestServer(t, func() time.Time { return now })
	reader.interfaces["pve1"] = "content"
	ts := mountedTestServer(t, srv)

	// A dead peer: bind a listener and close it immediately so connections
	// to it are refused deterministically (no reliance on an unroutable IP,
	// which can hang rather than fail fast in some sandboxes).
	deadLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := deadLn.Addr().String()
	_ = deadLn.Close()

	clock := &fixedClock{t: now}
	c := NewClient(ClientOptions{
		Secrets:                 newStaticSecretStore(testSecret),
		Scheme:                  "http",
		Logger:                  discardLogger(),
		Now:                     clock.now,
		RequestTimeout:          500 * time.Millisecond,
		BreakerFailureThreshold: 2,
		BreakerResetTimeout:     50 * time.Millisecond,
	})
	dead := Peer{Node: "dead", Addr: deadAddr}

	var lastErr error
	for i := 0; i < 2; i++ {
		_, lastErr = c.Interfaces(t.Context(), dead, "dead", false)
		if !errors.Is(lastErr, ErrPeerUnreachable) {
			t.Fatalf("call %d: err = %v, want it to wrap ErrPeerUnreachable", i, lastErr)
		}
	}

	// Breaker should now be open: further calls fast-fail without even
	// trying to connect. We can't directly observe "no dial attempted",
	// but a fast-fail is at minimum much faster than RequestTimeout, and
	// still surfaces ErrPeerUnreachable either way.
	start := time.Now()
	_, err = c.Interfaces(t.Context(), dead, "dead", false)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrPeerUnreachable) {
		t.Fatalf("open-breaker call err = %v, want ErrPeerUnreachable", err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("open-breaker call took %s, want a fast-fail well under the %s request timeout", elapsed, 500*time.Millisecond)
	}

	// Recovery: point the *same* Peer.Node's breaker at the live server
	// (as if the dead peer came back up at the same address) and advance
	// the breaker's clock past resetTimeout.
	live := Peer{Node: "dead", Addr: ts.Listener.Addr().String()}
	clock.advance(60 * time.Millisecond) // > BreakerResetTimeout
	content, err := c.Interfaces(t.Context(), live, "pve1", false)
	if err != nil {
		t.Fatalf("Interfaces after recovery: %v", err)
	}
	if content != "content" {
		t.Errorf("content = %q, want %q", content, "content")
	}
}

// TestClient_CheckCompatible_VersionMismatch is T-301 AC4: version mismatch
// surfaces as a coordination-refusal error.
func TestClient_CheckCompatible_VersionMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Version: "9.9.9-future",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	// Force a mismatch by wrapping the handler is not possible from outside
	// the package boundary in a black-box way, so instead this test relies
	// on the real server always answering ProtocolVersion and asserts the
	// matching-version success path, while a second server-less case below
	// exercises the mismatch branch directly against a stub HTTP handler
	// returning a different protocolVersion.
	ts := mountedTestServer(t, srv)
	c := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}
	if err := c.CheckCompatible(t.Context(), p); err != nil {
		t.Fatalf("CheckCompatible with matching protocol versions: %v", err)
	}

	// Now exercise an actual mismatch against a hand-rolled peer that
	// signs nothing (it doesn't need to — the client is the one signing,
	// and this stub just answers /api/peer/version unauthenticated for
	// test simplicity; the auth middleware is covered exhaustively
	// elsewhere) and reports an incompatible protocol version.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/peer/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, VersionInfo{Version: "old", ProtocolVersion: ProtocolVersion + 1})
	})
	stub := httptest.NewServer(mux)
	t.Cleanup(stub.Close)
	stubPeer := Peer{Node: "stub", Addr: stub.Listener.Addr().String()}

	err := c.CheckCompatible(t.Context(), stubPeer)
	if !errors.Is(err, ErrPeerIncompatible) {
		t.Fatalf("CheckCompatible with mismatched protocol versions: err = %v, want it to wrap ErrPeerIncompatible", err)
	}
}

func TestClient_ResponseErrorSurfacesPeerErrorCode(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv, reader, _ := newTestServer(t, func() time.Time { return now })
	_ = reader // no "missing" node seeded: InterfacesFile returns host.ErrNotFound -> 404
	ts := mountedTestServer(t, srv)
	c := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	_, err := c.Interfaces(t.Context(), p, "missing-node", false)
	if err == nil {
		t.Fatal("expected an error for an unknown node")
	}
	var respErr *ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("err = %v (%T), want a *ResponseError", err, err)
	}
	if respErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", respErr.StatusCode)
	}
	if respErr.Code != "not_found" {
		t.Errorf("Code = %q, want %q", respErr.Code, "not_found")
	}
}
