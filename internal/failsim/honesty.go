// SPDX-License-Identifier: Apache-2.0

package failsim

// Dimension codes are the machine-stable keys that appear in
// Impact.NotEvaluated when the simulator cannot assess an impact category.
// They mirror internal/sim's Caveat codes: a caller (or T-1607's posture
// score) keys off these exact strings, so they must not change.
const (
	// DimQuorum: corosync quorum impact could not be assessed — no corosync
	// config was supplied (a single, not-yet-clustered node), or at least one
	// voting node's ring address does not resolve to any interface in the
	// snapshot so its post-failure reachability is unknowable. Reported rather
	// than a confident quorumRisk:false.
	DimQuorum = "quorum"
	// DimCeph: Ceph public/cluster-network isolation could not be assessed —
	// no Ceph read model was supplied, or Ceph declares no networks (not
	// installed). Reported rather than a confident cephRisk:false.
	DimCeph = "ceph"
	// DimTunnels: WireGuard tunnel impact could not be assessed — no tunnel
	// model was supplied. Reported rather than silently treating tunnel-borne
	// connectivity as unaffected.
	DimTunnels = "tunnels"
	// DimGuestConnectivity: one or more guests have a NIC whose attachment
	// (bridge/vnet) does not resolve in the snapshot, so whether the removal
	// disconnects them cannot be determined. Reported (with the count)
	// rather than silently excluding them from disconnectedGuests as if they
	// were unaffected.
	DimGuestConnectivity = "guest-connectivity"
)

// honestyDimension is one row of the grep-able feature→evaluated|not-evaluated
// inventory (AC7), mirroring internal/sim's caveats.go Feature constants. Code
// is the DimXxx key; Evaluated states the precondition under which the
// dimension is actually computed (and therefore NOT added to NotEvaluated).
type honestyDimension struct {
	Code      string
	Evaluated string
}

// honestyInventory is the single source of truth for this package's honesty
// audit: every impact dimension, and the exact condition under which it is
// evaluated versus degraded to a loud "not evaluated". The report's honesty
// table (T-1604 AC7) is generated from this, so code and report cannot drift.
// The connectivity dimensions (guests, VLANs) are always evaluated over the
// snapshot's own graph — they have no external precondition — and so are
// listed here as "always" for completeness; only unresolvable individual
// entities degrade, via DimGuestConnectivity.
var honestyInventory = []honestyDimension{
	{Code: "disconnected-guests", Evaluated: "always (recomputed connected components over the post-failure snapshot)"},
	{Code: "stranded-vlans", Evaluated: "always (SDN vnets whose realizing bridge loses every live uplink)"},
	{Code: "mgmt-path-loss", Evaluated: "always (shared topology.ResolveMgmtPaths over the post-failure snapshot)"},
	{Code: DimQuorum, Evaluated: "corosync config supplied AND every voting node's ring address resolves to an interface"},
	{Code: DimCeph, Evaluated: "Ceph status supplied AND at least one of public/cluster network is declared"},
	{Code: DimTunnels, Evaluated: "at least one WireGuard tunnel supplied"},
	{Code: DimGuestConnectivity, Evaluated: "every guest NIC's bridge/vnet attachment resolves in the snapshot"},
}

// HonestyInventory returns the dimension→evaluated-condition audit inventory
// (a copy, so callers cannot mutate the package's source of truth). Exported
// for the completion report's honesty table and any external audit tooling,
// the same role internal/sim's report-embedded inventory plays.
func HonestyInventory() []struct{ Code, Evaluated string } {
	out := make([]struct{ Code, Evaluated string }, len(honestyInventory))
	for i, d := range honestyInventory {
		out[i] = struct{ Code, Evaluated string }{Code: d.Code, Evaluated: d.Evaluated}
	}
	return out
}
