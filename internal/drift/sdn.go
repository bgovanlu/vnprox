// SPDX-License-Identifier: Apache-2.0

// sdn.go implements docs/features/topology.md §6's third check family:
// "SDN zone node-membership vs. actual realization" — a zone lists a node
// as a member (SdnZone.Nodes), but that node has no bridge named
// SdnZone.Bridge in inventory, meaning real PVE's per-node
// GET /nodes/{node}/sdn/zones (T-3701) would report that node as
// status=error. Detection only: creating the missing bridge (which
// physical port(s) should it enslave?) is a decision a drift checker cannot
// safely make on its own, so this check offers no computable fix. The
// comparison lives in internal/xnode (SDNRealizationGaps), shared with
// internal/change's T-801 validator class; this file only adapts its
// result into a drift Finding (error severity).
//
// checkSDNZoneStatus (added by T-3701) is a second, independent check in
// this same file: PVE's own *live-reported* per-node zone status
// (SdnZone.NodeStatus, sourced from ListNodeSDNZoneStatus via
// internal/collect/pve.go's pollSDN) reporting anything other than "ok".
// This is deliberately not folded into checkSDNRealization above — the two
// checks can disagree, and both are worth keeping: labz on pvecube is a
// "simple" zone with no Bridge field set at all (nothing for
// checkSDNRealization's bridge-existence comparison to even check), yet PVE
// itself reports its status "error" for a reason vnprox cannot infer
// (planning/tasks/T-3701-sdn-zone-status.md, planning/reports/evidence/
// pve-9.2.4-sdn-zone-status.txt) — proof that a statically-computed
// membership/bridge check and PVE's own live-reported status are genuinely
// different signals, not one derivable from the other.

package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// checkSDNRealization is the CheckSDNRealization family.
func checkSDNRealization(snap inventory.Snapshot) []Finding {
	return driftFindings(xnode.SDNRealizationGaps(snap))
}

// checkSDNZoneStatus is the CheckSDNZoneStatus family (T-3701): one finding
// per zone that has at least one node reporting a live status other than
// "ok" (SdnZone.NodeStatus — see internal/pve.SDNZoneStatus's and
// ReconcileSDNZoneStatus's doc comments for where these values come from,
// including the vnprox-synthesized "unknown" for a declared member node PVE
// had nothing to report for at all). Detection only (Fixable: false, unlike
// the CheckSDNRealization sibling above): PVE's own zone status carries no
// explanation for a non-"ok" value, so inventing a remedy from a one-word
// status would repeat this task's own mistake
// (planning/tasks/T-3701-sdn-zone-status.md's "Deliverables" §4).
//
// Severity mirrors this codebase's existing red/amber zone-status
// vocabulary (internal/topology/project.go's sdnZoneStatus,
// web/src/sdn/status.ts's sdnNodeEntityStatus): "error" (and the
// vnprox-synthesized "unknown", which is exactly as unverified as an error)
// is SeverityError; "pending" (an expected, usually-transient staged-but-
// unapplied state, already visible elsewhere via T-401's own
// staged-vs-running diff) is SeverityWarning, not error — it should not by
// itself trip internal/change/autorollback.go's error-only auto-rollback
// gate the way a genuine realization failure should.
func checkSDNZoneStatus(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		z, ok := e.(*inventory.SdnZone)
		if !ok {
			continue
		}
		allNodes := make([]string, 0, len(z.NodeStatus))
		for node := range z.NodeStatus {
			allNodes = append(allNodes, node)
		}
		sort.Strings(allNodes)

		var badNodeNames, badNodeDetail []string
		severity := ""
		for _, node := range allNodes {
			status := z.NodeStatus[node]
			if status == "" || strings.EqualFold(status, "ok") {
				continue
			}
			badNodeNames = append(badNodeNames, node)
			badNodeDetail = append(badNodeDetail, node+":"+status)
			if strings.EqualFold(status, "pending") {
				if severity == "" {
					severity = SeverityWarning
				}
				continue
			}
			// "error", the synthesized "unknown", or anything else PVE
			// might someday report: treat as the severe case rather than
			// silently downgrading an unrecognized status to a warning.
			severity = SeverityError
		}
		if len(badNodeNames) == 0 {
			continue
		}
		detail := fmt.Sprintf("sdn zone %s reports non-ok realization status: %s", z.ID, strings.Join(badNodeDetail, ", "))
		out = append(out, newFinding(CheckSDNZoneStatus, severity, detail, badNodeNames, []string{z.GetRef().String()}))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
