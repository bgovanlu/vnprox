// SPDX-License-Identifier: Apache-2.0

package migration

// Verdict is Assessment's pinned classification — docs/api.md's Migration
// planner section, the exact three-value vocabulary T-1604 (Phase 16
// failure-impact simulator) and T-1103 (maintenance scheduler) consume.
type Verdict string

const (
	// VerdictOK: ample headroom, low risk of the migration/evacuation
	// meaningfully contending with other traffic on the link.
	VerdictOK Verdict = "ok"
	// VerdictTight: headroom exists but is thin relative to the estimated
	// dirty-page rate, or the link shows mild latency/loss degradation —
	// worth a second look before a Friday-night evacuation, not a hard
	// stop.
	VerdictTight Verdict = "tight"
	// VerdictInsufficient: no usable headroom remains, the estimated
	// dirty-page rate would exceed it (migration may not converge), or the
	// link is severely degraded. Advisory only — see doc.go: this package
	// never blocks anything, it only names the risk.
	VerdictInsufficient Verdict = "insufficient"
)

// Assessment is Plan's pinned response shape (docs/api.md's Migration
// planner section) — see doc.go's "Verdict stability" note. Every field is
// always populated; Caveats is an empty (never nil) slice when nothing
// needs flagging, so JSON serialization never omits it.
//
//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing.
type Assessment struct {
	// HeadroomMbps is the estimated spare bandwidth on the resolved
	// migration-network proxy (capacity.go/mesh.go) after subtracting
	// current T-1504-classified migration traffic volume — never negative
	// (floored at 0).
	HeadroomMbps float64 `json:"headroomMbps"`
	// EstimatedTransferSec is guest RAM size divided by HeadroomMbps — the
	// documented sentinel -1 when HeadroomMbps is 0 (no finite estimate is
	// possible).
	EstimatedTransferSec float64 `json:"estimatedTransferSec"`
	// Verdict is Plan's advisory classification — see Verdict's own doc
	// comment. Never causes or blocks any migration action by itself; see
	// doc.go's "Advisory only" section.
	Verdict Verdict `json:"verdict"`
	// BestEffort is unconditionally true on every Assessment this package
	// returns (see doc.go's "Dirty-rate estimate" section) — this arc has
	// no live guest instrumentation to make it anything else.
	BestEffort bool `json:"bestEffort"`
	// Caveats are human-readable notes about which inputs were estimated,
	// substituted, or unavailable — always present (possibly empty), never
	// nil.
	Caveats []string `json:"caveats"`
}
