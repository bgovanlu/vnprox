// sdn.go implements docs/features/topology.md §6's third check family:
// "SDN zone node-membership vs. actual realization" — a zone lists a node
// as a member (SdnZone.Nodes), but that node has no bridge named
// SdnZone.Bridge in inventory, meaning GET /cluster/sdn/zones/{zone}/status
// would report that node as status=error. Detection only: creating the
// missing bridge (which physical port(s) should it enslave?) is a decision
// a drift checker cannot safely make on its own, so this check offers no
// computable fix.

package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// checkSDNRealization is the CheckSDNRealization family.
func checkSDNRealization(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		zone, ok := e.(*inventory.SdnZone)
		if !ok || zone.Bridge == "" {
			continue
		}
		var unrealized []string
		for _, node := range sortedCopy(zone.Nodes) {
			if !bridgeRealizedOn(snap, node, zone.Bridge) {
				unrealized = append(unrealized, node)
			}
		}
		if len(unrealized) == 0 {
			continue
		}
		detail := fmt.Sprintf("SDN zone %s lists %s as member node(s) but bridge %s is not realized there (GET /cluster/sdn/zones/%s/status would report status=error)",
			zone.ID, strings.Join(unrealized, ", "), zone.Bridge, zone.ID)
		f := newFinding(CheckSDNRealization, SeverityError, detail, unrealized, []string{zone.GetRef().String()})
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// bridgeRealizedOn reports whether node has a Linux or OVS bridge named
// bridgeName in snap.
func bridgeRealizedOn(snap inventory.Snapshot, node, bridgeName string) bool {
	for _, kind := range []inventory.Kind{inventory.KindBridge, inventory.KindOVSBridge} {
		if _, ok := snap.Get(inventory.Ref{Kind: kind, Node: node, ID: bridgeName}); ok {
			return true
		}
	}
	return false
}

func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}
