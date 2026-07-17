// health_orphanvnet.go implements docs/features/monitoring.md §5's
// "orphan_vnet" health check (T-803): an SdnVnet whose Zone no longer
// resolves to any known SdnZone — the zone was deleted out from under it
// (possible via a raw `POST /changesets` body or a race between two
// concurrent edits; the guided wizards never produce this shape). Detection
// only: the fix is "create the missing zone" or "delete the orphaned
// vnet", both product decisions this check cannot make safely.
//
// Hysteresis-exempt (mgmt_single_path-style, per docs/features/
// monitoring.md §5's contract that this is a decision, not a default): "does
// this vnet's zone exist" is a structural property of the current SDN
// config, not a noisy live counter — there is nothing to debounce. It fires
// the instant the referenced zone disappears and clears the instant the
// vnet is deleted or its zone is recreated.

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckOrphanVnet = "orphan_vnet"

const orphanVnetDocsLink = "docs/features/monitoring.md#5-health-checks"

// checkOrphanVnet returns one finding per SdnVnet whose Zone field does not
// name any SdnZone currently present in snap.
func checkOrphanVnet(snap inventory.Snapshot) []Finding {
	zoneIDs := map[string]bool{}
	for _, e := range snap.All() {
		if z, ok := e.(*inventory.SdnZone); ok {
			zoneIDs[z.ID] = true
		}
	}

	var out []Finding
	for _, e := range snap.All() {
		vnet, ok := e.(*inventory.SdnVnet)
		if !ok || vnet.Zone == "" || zoneIDs[vnet.Zone] {
			continue
		}
		detail := fmt.Sprintf(
			"SDN VNet %s references zone %s, which no longer exists — this VNet is orphaned and will not realize on any node",
			vnet.ID, vnet.Zone)
		f := newHealthFinding(CheckOrphanVnet, SeverityWarning, detail, nil, []string{vnet.GetRef().String()})
		f.DocsLink = orphanVnetDocsLink
		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
