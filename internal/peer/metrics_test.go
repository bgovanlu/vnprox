package peer

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// fakePeerMetricsRecorder records every ObservePeerCall invocation, for
// T-1903's peer-RPC self-observability tests.
type fakePeerMetricsRecorder struct {
	calls []peerCallObservation
	mu    sync.Mutex
}

type peerCallObservation struct {
	node, endpoint, outcome string
	dur                     time.Duration
}

func (f *fakePeerMetricsRecorder) ObservePeerCall(node, endpoint, outcome string, dur time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, peerCallObservation{node: node, endpoint: endpoint, outcome: outcome, dur: dur})
}

func (f *fakePeerMetricsRecorder) outcomesFor(endpoint string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.endpoint == endpoint {
			out = append(out, c.outcome)
		}
	}
	return out
}

// TestClient_Do_RecordsOKOutcome covers a successful RPC: outcome "ok",
// endpoint stripped of its query string (never the raw path with its
// ?node=... query parameter — that's the whole point of AC1-style
// cardinality discipline applied to this package's own metrics).
func TestClient_Do_RecordsOKOutcome(t *testing.T) {
	fixedNow := func() time.Time { return time.Unix(1_700_000_000, 0) }
	srv, reader, _ := newTestServer(t, fixedNow)
	reader.interfaces["pve1"] = "content"
	ts := mountedTestServer(t, srv)

	rec := &fakePeerMetricsRecorder{}
	c := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret),
		Scheme:  "http",
		Logger:  discardLogger(),
		Now:     fixedNow,
		Metrics: rec,
	})
	live := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	if _, err := c.Interfaces(t.Context(), live, "pve1", false); err != nil {
		t.Fatalf("Interfaces: %v", err)
	}

	got := rec.outcomesFor("/api/peer/host/interfaces")
	if len(got) != 1 || got[0] != "ok" {
		t.Fatalf("outcomes for /api/peer/host/interfaces = %v, want exactly one \"ok\"", got)
	}
}

// TestClient_Do_RecordsUnreachableOutcome covers T-1903's failure-rate
// series for a dead peer, including the circuit breaker's own fast-fail
// path (no network attempt at all) — both must still surface as
// "unreachable" observations, not be silently dropped.
func TestClient_Do_RecordsUnreachableOutcome(t *testing.T) {
	deadLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := deadLn.Addr().String()
	_ = deadLn.Close()

	rec := &fakePeerMetricsRecorder{}
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := NewClient(ClientOptions{
		Secrets:                 newStaticSecretStore(testSecret),
		Scheme:                  "http",
		Logger:                  discardLogger(),
		Now:                     clock.now,
		RequestTimeout:          200 * time.Millisecond,
		BreakerFailureThreshold: 2,
		BreakerResetTimeout:     50 * time.Millisecond,
		Metrics:                 rec,
	})
	dead := Peer{Node: "dead", Addr: deadAddr}

	// Two real (connection-refused) attempts, then a third that fast-fails
	// via the now-open breaker.
	for i := 0; i < 3; i++ {
		if _, err := c.Interfaces(t.Context(), dead, "dead", false); !errors.Is(err, ErrPeerUnreachable) {
			t.Fatalf("call %d: err = %v, want ErrPeerUnreachable", i, err)
		}
	}

	got := rec.outcomesFor("/api/peer/host/interfaces")
	if len(got) != 3 {
		t.Fatalf("outcomes for /api/peer/host/interfaces = %v, want 3 observations (2 real + 1 fast-fail)", got)
	}
	for i, outcome := range got {
		if outcome != "unreachable" {
			t.Errorf("observation %d outcome = %q, want \"unreachable\"", i, outcome)
		}
	}
}

// TestClient_Do_RecordsUntrustedOutcome covers T-1906's headline
// distinction (a peer presenting a certificate from the wrong CA is
// "untrusted", not "unreachable") reaching T-1903's own metrics: the
// outcome label must be able to tell the two apart, mirroring
// TestTrust_AC5_UntrustedAndUnreachableAreDistinguishable's fixture.
func TestClient_Do_RecordsUntrustedOutcome(t *testing.T) {
	dir := t.TempDir()
	clusterCA := newTestCA(t, "vnprox test cluster CA")
	rogueCA := newTestCA(t, "rogue CA")
	caPath := clusterCA.writePEM(t, dir, "pve-root-ca.pem")

	impostor := newTLSPeerServer(t, rogueCA.issue(t, "127.0.0.1"))

	trust, err := NewTrust(TrustOptions{CAFile: caPath, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	rec := &fakePeerMetricsRecorder{}
	c := NewClient(ClientOptions{
		Secrets:                 newStaticSecretStore(testSecret),
		Trust:                   trust,
		Logger:                  discardLogger(),
		Scheme:                  "https",
		RequestTimeout:          5 * time.Second,
		BreakerFailureThreshold: 100,
		Metrics:                 rec,
	})

	if err := c.Health(t.Context(), impostor.peer("impostor")); !errors.Is(err, ErrPeerUntrusted) {
		t.Fatalf("Health: want ErrPeerUntrusted, got %v", err)
	}

	got := rec.outcomesFor("/api/peer/health")
	if len(got) != 1 || got[0] != "untrusted" {
		t.Fatalf("outcomes for /api/peer/health = %v, want exactly one \"untrusted\"", got)
	}
}

// TestClient_Do_NilMetricsIsNoOp proves a Client built without
// ClientOptions.Metrics (every pre-T-1903 caller) behaves exactly as
// before.
func TestClient_Do_NilMetricsIsNoOp(t *testing.T) {
	fixedNow := func() time.Time { return time.Unix(1_700_000_000, 0) }
	srv, reader, _ := newTestServer(t, fixedNow)
	reader.interfaces["pve1"] = "content"
	ts := mountedTestServer(t, srv)

	c := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http", Logger: discardLogger(), Now: fixedNow})
	live := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}
	if _, err := c.Interfaces(t.Context(), live, "pve1", false); err != nil {
		t.Fatalf("Interfaces with no Metrics configured: %v", err)
	}
}
