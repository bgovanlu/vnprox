// SPDX-License-Identifier: Apache-2.0

package findings

// health_neighborflap.go implements T-3905's neighbor_binding_flap finding:
// the findings-stream surface for internal/neighbor.HistoryRecorder.Flaps,
// itself computed over the persisted neighbor_bindings ring (docs/data-
// model.md, 0050_neighbor_bindings.sql).
//
// Relationship to arp_spoof_suspected (health_rogue.go), stated explicitly
// so the two checks don't drift apart independently: arp_spoof_suspected is
// the *live*, in-memory, security-severity signal — recomputed each
// findings cycle from a fresh RogueScan, reset on daemon restart, and
// deliberately hysteresis-exempt because a spoof signal must never be
// debounced away. neighbor_binding_flap is the *persisted*, warning-
// severity, node-attributed complement: it survives a restart (the ring it
// reads is durable SQLite, not a process-lifetime tracker), and it covers a
// direction arp_spoof_suspected does not — "one MAC claiming many IPs"
// (FlapKindMACClaim) — alongside the IP-churn direction the two checks
// share (FlapKindIPChurn, using the identical arpChurnWindow/
// arpChurnThreshold-matching thresholds — see
// internal/neighbor/history.go's IPFlapWindow/IPFlapThreshold doc comment
// for why the numbers are shared on purpose). A cluster running both checks
// should expect an IP-churn event to often surface as both
// arp_spoof_suspected (immediately, in-memory) and neighbor_binding_flap
// (once the persisted ring's next flap evaluation runs) — this is
// intentional overlap between "the security alarm" and "the operational
// timeline," not duplication to be collapsed into one check: silencing one
// must never silence the other; docs/features/monitoring.md §5 documents
// both.
//
// This package still does not import internal/store directly (the same
// decoupling every other Config seam in this package uses) — the flap
// query logic (window/threshold, candidate scan, count) lives in
// internal/neighbor, which does import internal/store; this file only
// adapts an already-computed []neighbor.FlapEvent-shaped report into
// Finding values.

import "fmt"

// CheckNeighborBindingFlap is this file's sole check name.
const CheckNeighborBindingFlap = "neighbor_binding_flap"

const neighborFlapDocsLink = "docs/features/monitoring.md#5-health-checks"

// NeighborFlapKind mirrors internal/neighbor.FlapKind without importing
// internal/neighbor (this package imports no host/store-facing package
// directly — every such dependency crosses a Provider seam, the same
// decoupling every other Config field here uses).
type NeighborFlapKind string

const (
	NeighborFlapIPChurn  NeighborFlapKind = "ip_churn"
	NeighborFlapMACClaim NeighborFlapKind = "mac_claim"
)

// NeighborFlapReading is one flapping binding, as reported by
// cmd/vnproxd's neighborFlapAdapter over internal/neighbor.HistoryRecorder.
// Field-for-field mirror of neighbor.FlapEvent.
type NeighborFlapReading struct {
	Node string
	Kind NeighborFlapKind
	IP   string // set for NeighborFlapIPChurn
	MAC  string // set for NeighborFlapMACClaim
	// IPs precedes Count: a slice carries a pointer and an int does not, and
	// govet's fieldalignment measures bytes up to the final pointer.
	IPs   []string // set for NeighborFlapMACClaim
	Count int
}

// NeighborFlapProvider is the findings engine's seam onto T-3905's
// persisted binding-flap detector. A nil provider skips this check
// entirely, the same degradation every other optional Config field in this
// package uses.
type NeighborFlapProvider interface {
	NeighborFlaps() ([]NeighborFlapReading, error)
}

// neighborFlapFindings adapts every currently-flapping binding into a
// Finding. Unlike arp_spoof_suspected this check needs no cross-cycle
// tracker of its own: NeighborFlapProvider already returns a report that
// has crossed threshold (internal/neighbor.HistoryRecorder.Flaps applies
// IPFlapThreshold/MACClaimThreshold itself, against the persisted ring),
// so this function is a pure, stateless adapt-and-render step — the same
// shape storeCapacityFindings' rep-to-Finding half takes, minus the
// hysteresis debouncer (a flap reading is already a windowed aggregate over
// several ticks' worth of history, not a single noisy instantaneous
// reading, so it needs no further smoothing — the same
// already-smoothed-signal reasoning docs/api.md's hysteresis-exempt list
// documents for orphan_vnet/trunk_unused_vlans/fw_rule_unused).
func neighborFlapFindings(p NeighborFlapProvider) []Finding {
	if p == nil {
		return nil
	}
	readings, err := p.NeighborFlaps()
	if err != nil || len(readings) == 0 {
		return nil
	}

	out := make([]Finding, 0, len(readings))
	for _, r := range readings {
		switch r.Kind {
		case NeighborFlapIPChurn:
			detail := fmt.Sprintf(
				"%s resolved to %d different MACs within the last %s on node %s — a flapping binding, not a single clean rebind. This can mean a rebooting/DHCP-renewing host, a misconfigured duplicate address, or ARP spoofing; see the neighbor binding timeline for the exact MAC sequence.",
				r.IP, r.Count, ipChurnWindowLabel, r.Node,
			)
			f := newHealthFinding(CheckNeighborBindingFlap, SeverityWarning, detail,
				[]string{r.Node}, []string{r.IP})
			f.DocsLink = neighborFlapDocsLink
			out = append(out, f)
		case NeighborFlapMACClaim:
			detail := fmt.Sprintf(
				"MAC %s was recorded as the owner of %d different IPs within the last %s on node %s (%s) — one interface claiming many addresses in a short window. This can mean a NAT/router interface, a misconfigured bridge, or a MAC flooding/spoofing attempt; see the neighbor binding timeline for the claimed addresses.",
				r.MAC, r.Count, macClaimWindowLabel, r.Node, joinIPs(r.IPs),
			)
			refs := append([]string{r.MAC}, r.IPs...)
			f := newHealthFinding(CheckNeighborBindingFlap, SeverityWarning, detail,
				[]string{r.Node}, refs)
			f.DocsLink = neighborFlapDocsLink
			out = append(out, f)
		}
	}
	sortFindings(out)
	return out
}

// ipChurnWindowLabel/macClaimWindowLabel are the human-readable window
// labels for Detail text — kept as plain strings here (rather than
// importing internal/neighbor's time.Duration constants) since this
// package deliberately takes no dependency on internal/neighbor; if
// internal/neighbor's IPFlapWindow/MACClaimWindow ever change, update these
// alongside them (both are exercised by health_neighborflap_test.go's
// Detail-text assertions, which will fail loudly on drift).
const (
	ipChurnWindowLabel  = "2m"
	macClaimWindowLabel = "2m"
)

func joinIPs(ips []string) string {
	if len(ips) == 0 {
		return ""
	}
	out := ips[0]
	for _, ip := range ips[1:] {
		out += ", " + ip
	}
	return out
}
