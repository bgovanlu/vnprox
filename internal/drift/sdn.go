// sdn.go implements docs/features/topology.md §6's third check family:
// "SDN zone node-membership vs. actual realization" — a zone lists a node
// as a member (SdnZone.Nodes), but that node has no bridge named
// SdnZone.Bridge in inventory, meaning GET /cluster/sdn/zones/{zone}/status
// would report that node as status=error. Detection only: creating the
// missing bridge (which physical port(s) should it enslave?) is a decision
// a drift checker cannot safely make on its own, so this check offers no
// computable fix. The comparison lives in internal/xnode
// (SDNRealizationGaps), shared with internal/change's T-801 validator class;
// this file only adapts its result into a drift Finding (error severity).

package drift

import (
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// checkSDNRealization is the CheckSDNRealization family.
func checkSDNRealization(snap inventory.Snapshot) []Finding {
	return driftFindings(xnode.SDNRealizationGaps(snap))
}
