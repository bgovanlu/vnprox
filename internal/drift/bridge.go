// SPDX-License-Identifier: Apache-2.0

// bridge.go implements docs/features/topology.md §6's first check family:
// "bridge presence/VLAN-awareness/VID sets for same-named bridges". The
// comparison itself now lives in internal/xnode (BridgeDivergences), shared
// verbatim with internal/change's cross-node pre-apply validator class
// (T-801) so the two are one implementation rather than two names for one
// problem; this file is the drift-side adapter (live snapshot -> drift
// Finding, warning severity, drift-titled fixes).

package drift

import (
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// checkBridgeDivergence is the CheckBridgeDivergence family.
func checkBridgeDivergence(snap inventory.Snapshot) []Finding {
	return driftFindings(xnode.BridgeDivergences(snap))
}
