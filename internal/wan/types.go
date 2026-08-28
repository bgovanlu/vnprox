// SPDX-License-Identifier: Apache-2.0

package wan

// Fabric is the internal/latmesh.Fabric value every WAN pair/sample carries
// — see store.WanFabric's doc comment for why this is a third fabric label
// rather than a fork of internal/latmesh's Pair/LinkHeat/Sample shapes.
const Fabric = "wan"

// Target is one operator-configured reference target: probe Host (an IP or
// hostname) from Node's Uplink. The uplink name is caller-chosen — in
// practice the web client populates it from T-1403's GET /edge/routes
// DefaultRoute.Iface list for the node being configured, but this package
// does not itself validate it against that list (an uplink name is just a
// label to this package; see this package's doc comment for the scope this
// card intentionally left out).
type Target struct {
	Node   string `json:"node"`
	Uplink string `json:"uplink"`
	Host   string `json:"host"`
}

// TargetStatus is one configured target's current-plus-rolling reading —
// GET /wan/status's per-target detail underneath each UplinkStatus.
type TargetStatus struct {
	Host           string  `json:"host"`
	At             int64   `json:"at"`
	RttMs          float64 `json:"rttMs"`
	LossPct        float64 `json:"lossPct"`
	RollingRttMs   float64 `json:"rollingRttMs"`
	RollingLossPct float64 `json:"rollingLossPct"`
	// Reachable is true iff the most recent probe tick got any reply at all
	// (LossPct < 100) — a target with LossPct == 100 is Reachable: false but
	// still reported (never silently dropped), the same "a persistently-
	// unreachable link still shows up" honesty stance internal/latmesh.
	// Service.Tick's own doc comment describes.
	Reachable bool `json:"reachable"`
}

// UplinkStatusLevel is GET /wan/status's per-uplink verdict vocabulary.
type UplinkStatusLevel string

const (
	// UplinkHealthy: every configured target's rolling loss is under
	// threshold.
	UplinkHealthy UplinkStatusLevel = "healthy"
	// UplinkDegraded: at least one configured target is over threshold, but
	// not every target is fully unreachable.
	UplinkDegraded UplinkStatusLevel = "degraded"
	// UplinkUnreachable: every configured target is at 100% loss.
	UplinkUnreachable UplinkStatusLevel = "unreachable"
	// UplinkUnknown is reserved and currently unused by Service.Status: a
	// freshly-configured target with no probe reading yet simply has no
	// entry in Status.Uplinks at all (see buildUplinkStatus' doc comment)
	// rather than an explicit "unknown" placeholder row. Kept in this
	// vocabulary so a future caller that wants to distinguish "never
	// configured" from "configured but not yet probed" has a value to use
	// without a wire-format change.
	UplinkUnknown UplinkStatusLevel = "unknown"
)

// UplinkStatus is GET /wan/status's per-uplink entry: T-1405 AC2's "each
// uplink's status independently" — multiple uplinks on one node never share
// or blend readings.
type UplinkStatus struct {
	Node    string            `json:"node"`
	Uplink  string            `json:"uplink"`
	Status  UplinkStatusLevel `json:"status"`
	Targets []TargetStatus    `json:"targets"`
	// AvailabilityPct/RttMs/LossPct are the uplink-level rollup: the mean
	// across its own configured targets' rolling figures (AvailabilityPct
	// = 100 - mean rolling loss%). Zero-value when Status is
	// UplinkUnknown (no reading yet) — never a misleadingly-cheerful 100%.
	AvailabilityPct float64 `json:"availabilityPct"`
	RttMs           float64 `json:"rttMs"`
	LossPct         float64 `json:"lossPct"`
}

// Verdict is GET /wan/status's dashboard-tile summary: "it's the ISP, not
// the cluster" or the all-clear, computed purely from this node's own
// uplink statuses (see this package's doc comment on scope) — the
// "otherwise clean" half of the objective (whether *other*, non-WAN cluster
// findings are also quiet) is layered on by the API handler, which alone
// has a FindingsService seam to ask (internal/wan deliberately never
// imports internal/findings, the same one-way dependency direction every
// other findings *producer* in this codebase keeps).
type Verdict string

const (
	VerdictHealthy   Verdict = "healthy"
	VerdictNoTargets Verdict = "no_targets"
	VerdictDegraded  Verdict = "wan_degraded"
	VerdictLikelyISP Verdict = "likely_isp"
)

// Status is GET /wan/status's full response shape.
type Status struct {
	Uplinks     []UplinkStatus `json:"uplinks"`
	GeneratedAt int64          `json:"generatedAt"`
}
