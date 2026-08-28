// SPDX-License-Identifier: Apache-2.0

// validate_overlay.go implements T-4106's overlay-readiness preflight: one
// composed verdict — BGP sessions up (internal/evpn), VTEP reachability,
// and measured MTU headroom (internal/mtuprobe) — for every vxlan/evpn zone
// a changeset's own zone.create/update ops touch, surfaced as a single
// validate-time Finding per zone rather than three a client would have to
// correlate.
//
// This is deliberately NOT internal/change/preflight.go's ImpactPreflighter
// (T-1604's failure-impact veto, hooked into the *scheduler* for
// unattended applies — a different composition entirely, gating
// windowStart rather than validate). OverlayReadinessPreflighter below
// matches that file's seam *shape* (a small ctx-taking interface the
// composition root backs with a real package, so internal/change never
// imports internal/evpn/internal/mtuprobe directly, or their host/peer/FRR
// dependency web) but is wired into Service.validationInputs — the SAME
// assembly point TcMirror/Switches/Allocations already use (see
// policy_service.go's validationInputs doc comment) — and consumed by the
// PURE overlayReadinessValidate below, never called from the scheduler.
//
// It also upgrades validate_sdn.go's underlayMTU assumption in place:
// overlayMTUReason below replaces that assumed default with a live
// mtuprobe measurement wherever the seam has one for the zone, and falls
// back to the documented assumed default exactly as before wherever it
// does not — never blocking validation on missing measurement data. When
// both a measurement and the assumed default are known and they would
// have reached different verdicts, that disagreement is named explicitly
// in the finding rather than silently discarded: a live measurement
// finding *less* headroom than the assumed default predicted is a real
// misconfiguration signal (the physical underlay is worse than assumed),
// not noise.

package change

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// OverlaySignalState is the three-state result of one overlay-readiness
// sub-signal: OverlayGood ("checked and fine"), OverlayBad ("checked and
// not fine"), and OverlayUnknown ("could not check" — FRR not running, no
// mtuprobe data yet, a VTEP reachability probe that never completed, or
// the whole fetch erroring). Collapsing OverlayUnknown into either of the
// other two would lie to the operator about what this daemon actually
// knows (this task's card), so it is kept as its own state throughout —
// the zero value is OverlayUnknown, so a zero-value OverlaySignal (a zone
// the seam never reported on at all) is honestly "could not check", never
// silently "fine".
type OverlaySignalState int

const (
	OverlayUnknown OverlaySignalState = iota
	OverlayGood
	OverlayBad
)

// OverlaySignal is one sub-signal's verdict plus a human-readable reason.
// Detail should always be populated for OverlayBad/OverlayUnknown (never a
// bare "not ready"/"cannot determine" with no reason) — AC3's "naming
// which signal failed, not a generic 'overlay not ready'" applies to the
// composed message zoneOverlayFinding builds from this, and an empty
// Detail there falls back to a generic label, which is a degraded case,
// not the intended one.
type OverlaySignal struct {
	Detail string
	State  OverlaySignalState
}

// OverlayMTUSignal is the MTU sub-signal's live input: a measured underlay
// MTU for (one representative node of) this zone's member/exit nodes, when
// internal/mtuprobe has one. Unlike BGP/VTEP above, MTU has no "unknown"
// state at the composition level: HasValue false always resolves via the
// documented assumed-default fallback (validate_sdn.go's underlayMTU),
// never blocking on missing data — this card's explicit deliverable, and
// exactly why this type carries no OverlaySignalState of its own.
type OverlayMTUSignal struct {
	// Node names which node Measured came from, purely for the finding
	// message — mtuprobe measures per-node (MeasuredUnderlayMTU(node)), so
	// when a zone has several member nodes the composition root supplies
	// the tightest (minimum) of them, the constraining one for the whole
	// VTEP mesh's headroom.
	Node     string
	Measured int
	HasValue bool
}

// ZoneOverlaySignals is one vxlan/evpn zone's already-fetched overlay
// signals, gathered once per validate call by Service.overlayReadinessInput
// via the OverlayReadinessPreflighter seam. The pure composer below only
// ever reads this struct, never touches evpn/mtuprobe itself — the same
// "Service reads live state, the pure validator only compares against what
// it's given" shape every other SafetyOptions field already follows. Its
// zero value (BGP/VTEP both OverlayUnknown, no measured MTU) is exactly
// what a zone the seam did not report on gets, which is the correct
// "could not check" answer, not a false "fine".
type ZoneOverlaySignals struct {
	BGP  OverlaySignal
	VTEP OverlaySignal
	MTU  OverlayMTUSignal
}

// OverlayZoneQuery is one zone this changeset's own ops touch, plus the
// node set (its effective member+exit nodes, union) the composition root
// should evaluate BGP/VTEP/MTU signals against — resolved by
// Service.overlayReadinessInput from the changeset's net-effect zone
// topology (effectiveZones, validate_sdn.go), since only that layer knows
// it; the seam implementation itself never reads ops/snap.
type OverlayZoneQuery struct {
	ZoneID string
	Nodes  []string
}

// OverlayReadinessPreflighter is the seam onto internal/evpn (BGP session
// state) and internal/mtuprobe (measured MTU, and — see the composition
// root adapter's own doc comment for exactly what it can and cannot assert
// here — VTEP reachability's honest Good/Unknown split), matching
// preflight.go's ImpactPreflighter shape: a small ctx-taking interface,
// evaluated over every touched zone in one batched call (mirroring
// ImpactPreflighter's `refs []inventory.Ref` batch shape, not one call per
// item), so internal/change never imports either package — or their
// host/peer/FRR dependency web — directly.
//
// An implementation error means the fetch itself could not be attempted
// for any of zones; Service.overlayReadinessInput degrades that to
// OverlayUnknown for every queried zone's every sub-signal rather than
// failing validation outright — an unassessable overlay is worth a
// warning, never a hard validate-time failure over a transient collector
// hiccup (unlike ImpactPreflighter's fail-closed stance, which guards an
// *unattended* apply with no operator watching; this is an attended,
// interactive validate call, and "cannot determine" is itself a findings-
// visible, honest answer here).
type OverlayReadinessPreflighter interface {
	OverlayReadiness(ctx context.Context, zones []OverlayZoneQuery) (map[string]ZoneOverlaySignals, error)
}

// overlayReadinessValidate is this card's composed check, run as a sibling
// of sdnValidate (ValidateWithSafety folds its findings into the same SDN
// pre-apply class — see that function's own comment). overlay is nil when
// the seam is not wired at all (SafetyOptions.Overlay's zero value):
// exactly SafetyOptions.TcMirror's zero-ceilings convention, a nil map
// means "unconfigured — skip", never "every zone fails". Once wired, every
// touched vxlan/evpn zone is looked up in overlay; an absent entry (the
// composition root's fetch didn't cover it, or errored) is the zero
// ZoneOverlaySignals, which is honestly "could not check" per that type's
// own doc comment, not silently skipped.
func overlayReadinessValidate(ops []Op, snap inventory.Snapshot, overlay map[string]ZoneOverlaySignals) []Finding {
	if overlay == nil {
		return nil
	}
	zones := effectiveZones(ops, snap)

	touched := map[string]bool{}
	for _, op := range ops {
		if op.Type == OpSdnZoneCreate || op.Type == OpSdnZoneUpdate {
			touched[op.Target.ID] = true
		}
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []Finding
	for _, id := range ids {
		z := zones[id]
		if z.typ != "vxlan" && z.typ != "evpn" {
			continue
		}
		if f, ok := zoneOverlayFinding(id, z.typ, z.MTU, overlay[id]); ok {
			out = append(out, f)
		}
	}
	return out
}

// zoneOverlayFinding composes zoneID's BGP/VTEP/MTU sub-signals into
// exactly one Finding, or ok=false when every sub-signal is clean
// (BGP/VTEP both OverlayGood, MTU headroom fine) — silence there is
// "checked, all clean", matching every other validator class in this
// package (no finding == nothing wrong). Severity is the worst of:
// SeverityError when BGP or VTEP is OverlayBad — a confirmed-broken
// overlay blocks the same way any other pre-apply SDN error does (this
// card's "BGP down blocks with a named reason"); otherwise SeverityWarning
// when anything is OverlayUnknown or the MTU headroom check fails — never
// blocking on a signal this daemon could not confirm one way or the
// other.
func zoneOverlayFinding(zoneID, zoneType string, mtu int, sig ZoneOverlaySignals) (Finding, bool) {
	var reasons []string
	severity := SeverityWarning

	switch sig.BGP.State {
	case OverlayBad:
		severity = SeverityError
		reasons = append(reasons, "bgp down: "+detailOr(sig.BGP.Detail, "session(s) not established"))
	case OverlayUnknown:
		reasons = append(reasons, "bgp cannot determine: "+detailOr(sig.BGP.Detail, "no BGP session state available"))
	}

	switch sig.VTEP.State {
	case OverlayBad:
		severity = SeverityError
		reasons = append(reasons, "vtep unreachable: "+detailOr(sig.VTEP.Detail, "unreachable"))
	case OverlayUnknown:
		reasons = append(reasons, "vtep cannot determine: "+detailOr(sig.VTEP.Detail, "no reachability data available"))
	}

	if mtuReason, breach := overlayMTUReason(mtu, sig.MTU); breach {
		reasons = append(reasons, mtuReason)
	}

	if len(reasons) == 0 {
		return Finding{}, false
	}

	ref := inventory.Ref{Kind: inventory.KindSDNZone, ID: zoneID}.String()
	msg := fmt.Sprintf("overlay readiness for %s zone %q: %s", zoneType, zoneID, strings.Join(reasons, "; "))
	return Finding{Severity: severity, Code: codeSDNOverlayReadiness, Message: msg, Ref: ref}, true
}

// detailOr returns detail, falling back to fallback when detail is empty —
// zoneOverlayFinding's defense against an OverlaySignal that reached
// OverlayBad/OverlayUnknown without a Detail string (a caller bug in the
// composition root adapter, not something the operator should ever see as
// a bare, unexplained "not ready").
func detailOr(detail, fallback string) string {
	if detail != "" {
		return detail
	}
	return fallback
}

// overlayMTUReason computes the MTU sub-signal's contribution to the
// composed finding. mtu==0 (unset) is never checked, mirroring
// checkVxlanMTU's own skip (validate_advisory.go) — PVE applies its own
// sane default. When live carries a measurement, it REPLACES the assumed
// underlayMTU constant for this decision (this card's upgrade path); when
// it does not, the existing assumed default is the fallback exactly as
// checkVxlanMTU already computes it — never blocking on missing
// measurement data (AC2). When both a measurement and the assumed default
// are known and they would have reached different verdicts, that
// disagreement is named explicitly in the returned reason.
func overlayMTUReason(mtu int, live OverlayMTUSignal) (reason string, breach bool) {
	if mtu == 0 {
		return "", false
	}
	assumedSafe := underlayMTU - vxlanOverhead
	assumedBreach := mtu > assumedSafe

	if !live.HasValue {
		if !assumedBreach {
			return "", false
		}
		return fmt.Sprintf("mtu: zone mtu %d leaves no headroom for VXLAN's %d-byte encapsulation overhead over the assumed %d-byte underlay path MTU (no live measurement yet — set it to %d)",
			mtu, vxlanOverhead, underlayMTU, assumedSafe), true
	}

	measuredSafe := live.Measured - vxlanOverhead
	measuredBreach := mtu > measuredSafe
	if !measuredBreach {
		return "", false
	}
	detail := fmt.Sprintf("mtu: zone mtu %d leaves no headroom for VXLAN's %d-byte encapsulation overhead over node %s's measured %d-byte underlay path MTU (set it to %d)",
		mtu, vxlanOverhead, live.Node, live.Measured, measuredSafe)
	if !assumedBreach {
		detail += fmt.Sprintf(" — the assumed %d-byte default said this was fine; the live measurement disagrees and wins", underlayMTU)
	}
	return detail, true
}

// overlayReadinessInput gathers T-4106's OverlayReadinessPreflighter
// output for every vxlan/evpn zone this changeset's own zone.create/update
// ops touch, once per validate call — the same "Service reads live state,
// the pure validator only compares against what it's given" shape
// tcMirrorUsage/switchSafetyInput/dhcpAllocations (service.go,
// validate_switch.go) already establish. A nil seam (feature not wired)
// returns nil, which overlayReadinessValidate treats as "skip entirely"
// (SafetyOptions.TcMirror's zero-ceilings convention, applied here). A
// seam error degrades every queried zone to its zero-value
// ZoneOverlaySignals (OverlayUnknown for BGP/VTEP, no measured MTU)
// rather than failing validation outright.
func (s *Service) overlayReadinessInput(ctx context.Context, ops []Op) map[string]ZoneOverlaySignals {
	if s.overlayPreflight == nil {
		return nil
	}
	snap := s.inventorySnapshot()
	zones := effectiveZones(ops, snap)

	touched := map[string]bool{}
	for _, op := range ops {
		if op.Type == OpSdnZoneCreate || op.Type == OpSdnZoneUpdate {
			touched[op.Target.ID] = true
		}
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var queries []OverlayZoneQuery
	for _, id := range ids {
		z := zones[id]
		if z.typ != "vxlan" && z.typ != "evpn" {
			continue
		}
		queries = append(queries, OverlayZoneQuery{ZoneID: id, Nodes: unionSortedUnique(z.nodes, z.exitNodes)})
	}
	if len(queries) == 0 {
		return nil
	}

	result, fetchErr := s.overlayPreflight.OverlayReadiness(ctx, queries)
	if fetchErr != nil {
		s.log.Warn("change: overlay readiness pre-flight fetch failed; reporting overlay signals as unknown", "error", fetchErr)
		result = map[string]ZoneOverlaySignals{}
	}

	out := make(map[string]ZoneOverlaySignals, len(queries))
	for _, q := range queries {
		out[q.ZoneID] = result[q.ZoneID] // zero value (all Unknown) when absent
	}
	return out
}

// unionSortedUnique returns the deduplicated, sorted union of a and b —
// OverlayZoneQuery.Nodes' construction from a zone's effective member and
// exit node lists, which may overlap.
func unionSortedUnique(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, n := range list {
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
