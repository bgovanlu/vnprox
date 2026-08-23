// sdn.go: docs/features/topology.md §6's third check family — an SDN zone
// lists a node as a member (SdnZone.Nodes), but that node has no bridge named
// SdnZone.Bridge, so real PVE's per-node GET /nodes/{node}/sdn/zones
// (T-3701) would report that node as status=error. Detection-only: creating
// the missing bridge needs a physical-port decision no comparison can make.
// This is the exact logic internal/drift previously carried inline in
// checkSDNRealization. See internal/drift/sdn.go's own doc comment for why
// this static membership/bridge comparison is a distinct signal from PVE's
// own live-reported per-node status (internal/drift's separate
// checkSDNZoneStatus family).

package xnode

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// SDNRealizationGaps reports, for every SDN zone with a realizing bridge, the
// member nodes on which that bridge is not realized. One Divergence per
// affected zone; its Subject is the zone's bridge name (so internal/change
// can scope to the bridges a changeset touched).
func SDNRealizationGaps(src Source) []Divergence {
	var out []Divergence
	for _, e := range src.All() {
		zone, ok := e.(*inventory.SdnZone)
		if !ok || zone.Bridge == "" {
			continue
		}
		var unrealized []string
		for _, node := range sortedCopy(zone.Nodes) {
			if !BridgeRealizedOn(src, node, zone.Bridge) {
				unrealized = append(unrealized, node)
			}
		}
		if len(unrealized) == 0 {
			continue
		}
		// The endpoint named here is operator-facing — it is what someone
		// reads out of a finding and then goes and runs. It used to name
		// GET /cluster/sdn/zones/{zone}/status, which PVE 9.2.4 answers with
		// a 501 (T-3701). Status is per-NODE, so the check to hand them is
		// one call per unrealized node.
		detail := fmt.Sprintf("SDN zone %s lists %s as member node(s) but bridge %s is not realized there (GET /nodes/{node}/sdn/zones reports zone %s with status=error on those nodes)",
			zone.ID, strings.Join(unrealized, ", "), zone.Bridge, zone.ID)
		out = append(out, Divergence{
			Family:  FamilySDNRealization,
			Subject: zone.Bridge,
			Detail:  detail,
			Nodes:   sortedUnique(unrealized),
			Refs:    sortedUnique([]string{zone.GetRef().String()}),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detail < out[j].Detail })
	return out
}

// BridgeRealizedOn reports whether node has a Linux or OVS bridge named
// bridgeName in src.
func BridgeRealizedOn(src Source, node, bridgeName string) bool {
	for _, kind := range []inventory.Kind{inventory.KindBridge, inventory.KindOVSBridge} {
		if _, ok := src.Get(inventory.Ref{Kind: kind, Node: node, ID: bridgeName}); ok {
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
