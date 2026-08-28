// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// --- T-4106: overlay-readiness preflight ------------------------------------

// evpnZoneCreateOp builds a minimal sdn.zone.create op for an evpn zone,
// MTU as given (0 = unset). Nodes is deliberately left unset — real PVE
// semantics treat that as "no restriction, every cluster node" (validate_
// sdn.go's own doc comment, and TestValidate_Golden's "clean: sdn.zone.
// create with no nodes restriction" case), so it needs no referential
// node-coverage fixture and never short-circuits ValidateWithSafety before
// reaching the overlay-readiness class this file tests.
func evpnZoneCreateOp(zoneID string, mtu int) (inventory.Ref, Op) {
	ref := testRef(inventory.KindSDNZone, "", zoneID)
	return ref, mkOp(OpSdnZoneCreate, ref, &SdnZoneCreateParams{Type: "evpn", MTU: mtu})
}

func TestOverlayReadinessValidate_AllSignalsGood(t *testing.T) {
	_, op := evpnZoneCreateOp("zone1", 1450)
	overlay := map[string]ZoneOverlaySignals{
		"zone1": {
			BGP:  OverlaySignal{State: OverlayGood},
			VTEP: OverlaySignal{State: OverlayGood},
			MTU:  OverlayMTUSignal{Node: "node1", Measured: 1500, HasValue: true},
		},
	}
	findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})
	for _, f := range findings {
		if f.Code == codeSDNOverlayReadiness {
			t.Errorf("unexpected overlay readiness finding when every signal is good: %+v", f)
		}
	}
}

func TestOverlayReadinessValidate_SignalsFailIndividually(t *testing.T) {
	good := ZoneOverlaySignals{
		BGP:  OverlaySignal{State: OverlayGood},
		VTEP: OverlaySignal{State: OverlayGood},
		MTU:  OverlayMTUSignal{Node: "node1", Measured: 1500, HasValue: true},
	}

	// Field order is densest-pointer-first: the strings precede the struct,
	// since govet's fieldalignment counts bytes up to the final pointer.
	tests := []struct {
		name       string
		wantSev    Severity
		wantSubstr string
		signals    ZoneOverlaySignals
	}{
		{
			name: "bgp down blocks with a named reason",
			signals: func() ZoneOverlaySignals {
				s := good
				s.BGP = OverlaySignal{State: OverlayBad, Detail: "session node1<->10.0.0.2 is Idle, not Established"}
				return s
			}(),
			wantSev:    SeverityError,
			wantSubstr: "bgp down: session node1<->10.0.0.2 is Idle, not Established",
		},
		{
			name: "vtep unreachable blocks with a named reason",
			signals: func() ZoneOverlaySignals {
				s := good
				s.VTEP = OverlaySignal{State: OverlayBad, Detail: "no response from 10.0.0.2"}
				return s
			}(),
			wantSev:    SeverityError,
			wantSubstr: "vtep unreachable: no response from 10.0.0.2",
		},
		{
			name: "mtu headroom insufficient against the measured value warns",
			signals: func() ZoneOverlaySignals {
				s := good
				s.MTU = OverlayMTUSignal{Node: "node1", Measured: 1470, HasValue: true} // safe = 1420, zone mtu 1450 below breaches
				return s
			}(),
			wantSev:    SeverityWarning,
			wantSubstr: "measured 1470-byte underlay path MTU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, op := evpnZoneCreateOp("zone1", 1450)
			overlay := map[string]ZoneOverlaySignals{"zone1": tt.signals}
			findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})

			var found *Finding
			for i := range findings {
				if findings[i].Code == codeSDNOverlayReadiness {
					found = &findings[i]
				}
			}
			if found == nil {
				t.Fatalf("no %s finding in %+v", codeSDNOverlayReadiness, findings)
			}
			if found.Severity != tt.wantSev {
				t.Errorf("severity = %s, want %s", found.Severity, tt.wantSev)
			}
			if !strings.Contains(found.Message, tt.wantSubstr) {
				t.Errorf("message = %q, want substring %q", found.Message, tt.wantSubstr)
			}
		})
	}
}

func TestOverlayReadinessValidate_SignalsUnavailableIndividually(t *testing.T) {
	good := ZoneOverlaySignals{
		BGP:  OverlaySignal{State: OverlayGood},
		VTEP: OverlaySignal{State: OverlayGood},
		MTU:  OverlayMTUSignal{Node: "node1", Measured: 1500, HasValue: true},
	}

	tests := []struct {
		name       string
		wantSubstr string
		signals    ZoneOverlaySignals
	}{
		{
			name: "bgp cannot determine (FRR not running) never collapses to ready or not-ready",
			signals: func() ZoneOverlaySignals {
				s := good
				s.BGP = OverlaySignal{State: OverlayUnknown, Detail: "FRR not installed/running on node1"}
				return s
			}(),
			wantSubstr: "bgp cannot determine: FRR not installed/running on node1",
		},
		{
			name: "vtep cannot determine (no mtuprobe reachability data) never collapses to ready or not-ready",
			signals: func() ZoneOverlaySignals {
				s := good
				s.VTEP = OverlaySignal{State: OverlayUnknown, Detail: "no mtuprobe reachability data yet for this zone's nodes"}
				return s
			}(),
			wantSubstr: "vtep cannot determine: no mtuprobe reachability data yet",
		},
		{
			name:       "zone with no overlay entry at all (seam wired but never reported on this zone) is honestly unknown, not silently clean",
			signals:    ZoneOverlaySignals{}, // zero value: BGP/VTEP both OverlayUnknown
			wantSubstr: "cannot determine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, op := evpnZoneCreateOp("zone1", 1450)
			overlay := map[string]ZoneOverlaySignals{"zone1": tt.signals}
			findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})

			var found *Finding
			for i := range findings {
				if findings[i].Code == codeSDNOverlayReadiness {
					found = &findings[i]
				}
			}
			if found == nil {
				t.Fatalf("no %s finding in %+v", codeSDNOverlayReadiness, findings)
			}
			if found.Severity != SeverityWarning {
				t.Errorf("severity = %s, want warning — an unconfirmed signal must never block apply outright", found.Severity)
			}
			if !strings.Contains(found.Message, tt.wantSubstr) {
				t.Errorf("message = %q, want substring %q", found.Message, tt.wantSubstr)
			}
		})
	}
}

// TestOverlayReadinessValidate_MTUAssumedVsMeasuredDisagreement is this
// card's explicit AC2/deliverable: when a measurement exists, it REPLACES
// the assumed default (validate_sdn.go's underlayMTU/vxlanOverhead), and
// when the two would have reached different verdicts, that disagreement is
// named in the finding rather than silently discarded.
func TestOverlayReadinessValidate_MTUAssumedVsMeasuredDisagreement(t *testing.T) {
	t.Run("measured finds LESS headroom than the assumed default said was fine — the measurement wins and disagreement is surfaced", func(t *testing.T) {
		// assumed: underlay 1500 - overhead 50 = 1450 safe; zone mtu 1450 is
		// exactly at the assumed-safe boundary, so the assumed default alone
		// would NOT have warned (checkVxlanMTU's own "mtu already 1450 does
		// not warn" case). A live measurement of 1480 lowers safe to 1430,
		// which zone mtu 1450 now breaches.
		_, op := evpnZoneCreateOp("zone1", 1450)
		overlay := map[string]ZoneOverlaySignals{
			"zone1": {
				BGP:  OverlaySignal{State: OverlayGood},
				VTEP: OverlaySignal{State: OverlayGood},
				MTU:  OverlayMTUSignal{Node: "node1", Measured: 1480, HasValue: true},
			},
		}
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})

		var found *Finding
		for i := range findings {
			if findings[i].Code == codeSDNOverlayReadiness {
				found = &findings[i]
			}
		}
		if found == nil {
			t.Fatalf("no %s finding in %+v (measured headroom is tighter than assumed and should have fired)", codeSDNOverlayReadiness, findings)
		}
		if found.Severity != SeverityWarning {
			t.Errorf("severity = %s, want warning", found.Severity)
		}
		if !strings.Contains(found.Message, "measured 1480-byte underlay path MTU") {
			t.Errorf("message = %q, want it to name the measured value", found.Message)
		}
		if !strings.Contains(found.Message, "the assumed 1500-byte default said this was fine") {
			t.Errorf("message = %q, want it to explicitly surface the assumed-vs-measured disagreement", found.Message)
		}
	})

	t.Run("measured finds MORE headroom than assumed would have flagged — no finding, the measurement wins silently", func(t *testing.T) {
		// zone mtu 1500 would breach the assumed default (1450 safe,
		// checkVxlanMTU's own "underlay 1500 + zone mtu 1500 warns" case),
		// but a live measurement of a fatter underlay (e.g. jumbo-capable
		// path, measured 1600) proves there IS headroom after all.
		_, op := evpnZoneCreateOp("zone1", 1500)
		overlay := map[string]ZoneOverlaySignals{
			"zone1": {
				BGP:  OverlaySignal{State: OverlayGood},
				VTEP: OverlaySignal{State: OverlayGood},
				MTU:  OverlayMTUSignal{Node: "node1", Measured: 1600, HasValue: true},
			},
		}
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})
		for _, f := range findings {
			if f.Code == codeSDNOverlayReadiness {
				t.Errorf("unexpected overlay readiness finding: measured headroom is sufficient, the measurement should win: %+v", f)
			}
		}
	})

	t.Run("no measurement yet falls back to the assumed default exactly as checkVxlanMTU already does, never blocking on missing data", func(t *testing.T) {
		_, op := evpnZoneCreateOp("zone1", 1500)
		overlay := map[string]ZoneOverlaySignals{
			"zone1": {
				BGP:  OverlaySignal{State: OverlayGood},
				VTEP: OverlaySignal{State: OverlayGood},
				MTU:  OverlayMTUSignal{}, // HasValue: false
			},
		}
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})

		var found *Finding
		for i := range findings {
			if findings[i].Code == codeSDNOverlayReadiness {
				found = &findings[i]
			}
		}
		if found == nil {
			t.Fatalf("no %s finding in %+v (assumed-default fallback should have fired, same as checkVxlanMTU)", codeSDNOverlayReadiness, findings)
		}
		if !strings.Contains(found.Message, "no live measurement yet") {
			t.Errorf("message = %q, want it to say no live measurement was available", found.Message)
		}
	})
}

// TestOverlayReadinessValidate_NoRegression pins the deliverable "no
// regression to non-VXLAN/EVPN zone validation": a simple zone is never
// evaluated by this class regardless of what the overlay map carries for
// it, and a nil/absent Overlay (the seam not wired at all — every existing
// caller before this card) produces nothing new whatsoever, byte for byte.
func TestOverlayReadinessValidate_NoRegression(t *testing.T) {
	t.Run("simple zone type is never checked, even with bad signals wired", func(t *testing.T) {
		ref := testRef(inventory.KindSDNZone, "", "zone1")
		op := mkOp(OpSdnZoneCreate, ref, &SdnZoneCreateParams{Type: "simple", MTU: 1500})
		overlay := map[string]ZoneOverlaySignals{
			"zone1": {BGP: OverlaySignal{State: OverlayBad, Detail: "should never be evaluated"}},
		}
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})
		for _, f := range findings {
			if f.Code == codeSDNOverlayReadiness {
				t.Errorf("unexpected overlay readiness finding on a simple zone: %+v", f)
			}
		}
	})

	t.Run("seam not wired at all (Overlay nil, every pre-T-4106 caller) is a pure no-op", func(t *testing.T) {
		_, op := evpnZoneCreateOp("zone1", 1500) // would breach the assumed default if evaluated
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{})
		for _, f := range findings {
			if f.Code == codeSDNOverlayReadiness {
				t.Errorf("unexpected overlay readiness finding with the seam unwired: %+v", f)
			}
		}
	})

	t.Run("mtu unset (0) is never checked, mirroring checkVxlanMTU's own skip", func(t *testing.T) {
		_, op := evpnZoneCreateOp("zone1", 0)
		overlay := map[string]ZoneOverlaySignals{
			"zone1": {BGP: OverlaySignal{State: OverlayGood}, VTEP: OverlaySignal{State: OverlayGood}},
		}
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{Overlay: overlay})
		for _, f := range findings {
			if f.Code == codeSDNOverlayReadiness {
				t.Errorf("unexpected overlay readiness finding with mtu unset: %+v", f)
			}
		}
	})
}

// TestOverlayReadinessInput_SkipsWhenSeamUnwired pins
// Service.overlayReadinessInput's nil-seam degraded mode directly (as
// opposed to the SafetyOptions.Overlay-level tests above, which start from
// an already-built map): a Service with no OverlayPreflight configured
// returns a nil map, never an error, never a populated-but-empty one.
func TestOverlayReadinessInput_SkipsWhenSeamUnwired(t *testing.T) {
	svc := &Service{} // overlayPreflight left nil: the seam-unwired case
	_, op := evpnZoneCreateOp("zone1", 1500)
	got := svc.overlayReadinessInput(t.Context(), []Op{op})
	if got != nil {
		t.Errorf("overlayReadinessInput with no seam wired = %+v, want nil", got)
	}
}

// fakeInv adapts a fixed inventory.Snapshot to InventorySource, for tests
// that need a *Service with a real (non-nil) inventory seam.
type fakeInv struct{ snap inventory.Snapshot }

func (f fakeInv) Snapshot() inventory.Snapshot { return f.snap }

// fakeOverlayPreflighter is a scriptable OverlayReadinessPreflighter: it
// records every batch of zones it was called with (queries) and returns
// either result or err, whichever is set.
// Field order is densest-pointer-first: an error is two pointer words, a map
// one, a slice one plus two scalars.
type fakeOverlayPreflighter struct {
	err     error
	result  map[string]ZoneOverlaySignals
	queries []OverlayZoneQuery
}

func (f *fakeOverlayPreflighter) OverlayReadiness(_ context.Context, zones []OverlayZoneQuery) (map[string]ZoneOverlaySignals, error) {
	f.queries = zones
	return f.result, f.err
}

// TestOverlayReadinessInput_BatchesTouchedZonesAndUnionsNodes pins
// overlayReadinessInput's query construction: it queries only the
// changeset's own touched vxlan/evpn zones (a simple zone is excluded
// even though it too is touched), in one batched call, with each zone's
// node set being the deduplicated, sorted union of its member and exit
// nodes.
func TestOverlayReadinessInput_BatchesTouchedZonesAndUnionsNodes(t *testing.T) {
	evpnRef := testRef(inventory.KindSDNZone, "", "evpnzone")
	simpleRef := testRef(inventory.KindSDNZone, "", "simplezone")
	ops := []Op{
		mkOp(OpSdnZoneCreate, evpnRef, &SdnZoneCreateParams{
			Type: "evpn", Nodes: []string{"node2", "node1"}, ExitNodes: []string{"node1", "node3"},
		}),
		mkOp(OpSdnZoneCreate, simpleRef, &SdnZoneCreateParams{Type: "simple"}),
	}

	fake := &fakeOverlayPreflighter{result: map[string]ZoneOverlaySignals{
		"evpnzone": {BGP: OverlaySignal{State: OverlayGood}, VTEP: OverlaySignal{State: OverlayGood}},
	}}
	svc := &Service{inv: fakeInv{snap: buildSnapshot()}, overlayPreflight: fake}

	got := svc.overlayReadinessInput(t.Context(), ops)

	if len(fake.queries) != 1 {
		t.Fatalf("queries = %+v, want exactly one (the evpn zone; the simple zone must be excluded)", fake.queries)
	}
	q := fake.queries[0]
	if q.ZoneID != "evpnzone" {
		t.Errorf("queried zone = %q, want %q", q.ZoneID, "evpnzone")
	}
	wantNodes := []string{"node1", "node2", "node3"}
	if !slicesEqual(q.Nodes, wantNodes) {
		t.Errorf("queried nodes = %v, want deduplicated sorted union %v", q.Nodes, wantNodes)
	}
	if got["evpnzone"].BGP.State != OverlayGood {
		t.Errorf("got[evpnzone].BGP.State = %v, want OverlayGood (from the fake's result)", got["evpnzone"].BGP.State)
	}
	if _, ok := got["simplezone"]; ok {
		t.Errorf("got has an entry for the simple zone, want none: %+v", got)
	}
}

// TestOverlayReadinessInput_FetchErrorDegradesToUnknown pins the
// documented degrade path: a seam fetch error never fails validation
// outright — every queried zone still gets an entry (the zero value,
// honestly OverlayUnknown for BGP/VTEP), not a nil map and not a silently
// dropped zone.
func TestOverlayReadinessInput_FetchErrorDegradesToUnknown(t *testing.T) {
	_, op := evpnZoneCreateOp("zone1", 1500)
	fake := &fakeOverlayPreflighter{err: errors.New("fetching cluster BGP/EVPN status: boom")}
	svc := &Service{inv: fakeInv{snap: buildSnapshot()}, overlayPreflight: fake, log: slog.Default()}

	got := svc.overlayReadinessInput(t.Context(), []Op{op})

	sig, ok := got["zone1"]
	if !ok {
		t.Fatalf("got = %+v, want an entry for zone1 even on a fetch error", got)
	}
	if sig.BGP.State != OverlayUnknown || sig.VTEP.State != OverlayUnknown {
		t.Errorf("sig = %+v, want both BGP and VTEP OverlayUnknown on a fetch error", sig)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
