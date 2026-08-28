// SPDX-License-Identifier: Apache-2.0

// pending.go implements docs/features/topology.md §6's fourth check
// family: "pending-but-unapplied interfaces.new files" — an interface PVE
// reports as `pending` (new/changed/deleted) on GET /nodes/{node}/network
// because a staged edit was never applied via reload (internal/inventory's
// Pending field, sourced from pve-network only — see merge.go). Detection
// only: applying a staged edit is a real infrastructure action ("reload")
// outside the v1 op vocabulary, and blindly reloading an operator's
// half-finished staged edit without review is not something a drift
// checker should ever do unattended.

package drift

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// checkPendingInterfaces is the CheckPendingInterfaces family.
func checkPendingInterfaces(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		pending, ref, ok := pendingOf(e)
		if !ok || pending == "" {
			continue
		}
		detail := fmt.Sprintf("interface %s on node %s has an unapplied staged change (pending=%s) — GET /nodes/%s/network reports it as pending, but reload was never run",
			ref.ID, ref.Node, pending, ref.Node)
		f := newFinding(CheckPendingInterfaces, SeverityWarning, detail, []string{ref.Node}, []string{ref.String()})
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func pendingOf(e inventory.Entity) (pending string, ref inventory.Ref, ok bool) {
	switch v := e.(type) {
	case *inventory.PhysNic:
		return v.Pending, v.GetRef(), true
	case *inventory.Bond:
		return v.Pending, v.GetRef(), true
	case *inventory.Bridge:
		return v.Pending, v.GetRef(), true
	case *inventory.VlanIface:
		return v.Pending, v.GetRef(), true
	default:
		return "", inventory.Ref{}, false
	}
}
