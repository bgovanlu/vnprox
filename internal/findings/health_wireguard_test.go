package findings

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// fixedNow is a stable evaluation instant the WireGuard fixtures express
// handshake ages relative to.
var wgNow = time.Unix(1721300100, 0)

func countCheck(fs []Finding, check string) int {
	n := 0
	for _, f := range fs {
		if f.Check == check {
			n++
		}
	}
	return n
}

// TestWgHandshakeStale_HysteresisAndClear is T-1401 AC4's stale-handshake half:
// a stale-handshake scenario raises exactly one wg_handshake_stale finding with
// hysteresis (a single missed check does not fire), and it clears when the
// fixture returns to healthy.
func TestWgHandshakeStale_HysteresisAndClear(t *testing.T) {
	db := newDebouncer()
	stale := []wireguard.ObservedTunnel{wireguard.FixtureStaleHandshake(wgNow)}

	// Cycle 1: a single missed check must NOT fire (rise threshold is 2).
	if got := checkWgHandshakeStale(stale, db, wgNow); len(got) != 0 {
		t.Fatalf("cycle 1: got %d findings, want 0 (single missed check must not fire)", len(got))
	}
	// Cycle 2: now it fires — exactly one.
	got := checkWgHandshakeStale(stale, db, wgNow)
	if len(got) != 1 || got[0].Check != CheckWgHandshakeStale {
		t.Fatalf("cycle 2: got %+v, want exactly one wg_handshake_stale", got)
	}
	if got[0].Source != SourceWireguard {
		t.Errorf("source = %q, want wireguard", got[0].Source)
	}

	// Return to healthy: clears within the fall window (2 cycles).
	healthy := []wireguard.ObservedTunnel{wireguard.FixtureHealthy(wgNow)}
	_ = checkWgHandshakeStale(healthy, db, wgNow)
	if got := checkWgHandshakeStale(healthy, db, wgNow); len(got) != 0 {
		t.Fatalf("after healthy: got %d findings, want 0 (should clear)", len(got))
	}
}

// TestWgEndpointDrift_RaisesAndClears is T-1401 AC4's endpoint-drift half: an
// endpoint-drift scenario raises the wg_endpoint_drift finding (and NOT
// wg_handshake_stale, since its handshake is recent), and it clears when
// healthy.
func TestWgEndpointDrift_RaisesAndClears(t *testing.T) {
	driftDB := newDebouncer()
	staleDB := newDebouncer()
	drift := []wireguard.ObservedTunnel{wireguard.FixtureEndpointDrift(wgNow)}

	// Two cycles to cross the rise threshold.
	_ = checkWgEndpointDrift(drift, driftDB)
	got := checkWgEndpointDrift(drift, driftDB)
	if len(got) != 1 || got[0].Check != CheckWgEndpointDrift {
		t.Fatalf("got %+v, want exactly one wg_endpoint_drift", got)
	}

	// The same scenario must NOT raise a stale-handshake finding (handshake is
	// recent) even after multiple cycles.
	_ = checkWgHandshakeStale(drift, staleDB, wgNow)
	if got := checkWgHandshakeStale(drift, staleDB, wgNow); len(got) != 0 {
		t.Fatalf("endpoint-drift scenario wrongly raised %d stale findings", len(got))
	}

	// Return to healthy: clears.
	healthy := []wireguard.ObservedTunnel{wireguard.FixtureHealthy(wgNow)}
	_ = checkWgEndpointDrift(healthy, driftDB)
	if got := checkWgEndpointDrift(healthy, driftDB); len(got) != 0 {
		t.Fatalf("after healthy: got %d drift findings, want 0", len(got))
	}
}

// TestWgFindings_HealthyNoFindings proves a healthy dual-tunnel state produces
// nothing across several cycles.
func TestWgFindings_HealthyNoFindings(t *testing.T) {
	e := New(Config{WG: staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureHealthy(wgNow)}}, Now: func() time.Time { return wgNow }})
	for i := 0; i < 4; i++ {
		fs := e.Findings()
		if n := countCheck(fs, CheckWgHandshakeStale) + countCheck(fs, CheckWgEndpointDrift); n != 0 {
			t.Fatalf("cycle %d: healthy state produced %d wireguard findings", i, n)
		}
	}
}

// TestWgFindings_EngineWires proves the Engine surfaces wireguard-source
// findings end-to-end through Findings() when a WGProvider is wired.
func TestWgFindings_EngineWires(t *testing.T) {
	e := New(Config{WG: staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureStaleHandshake(wgNow)}}, Now: func() time.Time { return wgNow }})
	_ = e.Findings() // cycle 1: below rise threshold
	fs := e.Findings()
	if countCheck(fs, CheckWgHandshakeStale) != 1 {
		t.Fatalf("engine did not surface wg_handshake_stale: %+v", fs)
	}
	for _, f := range fs {
		if f.Check == CheckWgHandshakeStale && f.Source != SourceWireguard {
			t.Errorf("finding source = %q, want wireguard", f.Source)
		}
	}
}

type staticWG struct{ tunnels []wireguard.ObservedTunnel }

func (s staticWG) WireGuardState() []wireguard.ObservedTunnel { return s.tunnels }
