// health_bondslave.go implements docs/features/monitoring.md §5's "bond
// slave down" check: a bond member interface whose MII/link status has
// dropped, straight from data already in inventory.Bond.SlaveDetail (no new
// collection needed — host-netlink already reports this via
// /proc/net/bonding, see internal/host.BondDetail.Slaves). Detection only:
// which physical action (recable, replace, re-enable a switch port) fixes a
// down slave is outside any changeset op vnprox can safely compute, so this
// is the check the task card explicitly names as having no computable fix
// (AC4's "bond slave down -> no").

package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckBondSlaveDown = "bond_slave_down"

const bondSlaveDownDocsLink = "docs/features/monitoring.md#5-health-checks"

// bondSlaveRiseCycles/bondSlaveFallCycles: a slave's MII status must be
// observed down/up this many consecutive Engine cycles before the finding
// fires/clears (light hysteresis against a single transient netlink read
// glitch — AC3's spirit applied to a binary, not just a numeric-threshold,
// check).
const (
	bondSlaveRiseCycles = 2
	bondSlaveFallCycles = 2
)

// checkBondSlaveDown evaluates every Bond entity's SlaveDetail against db
// (Engine's per-check debouncer) and returns one finding per slave that has
// been consistently reported down.
func checkBondSlaveDown(snap inventory.Snapshot, db *debouncer) []Finding {
	var out []Finding
	live := map[string]bool{}

	for _, e := range snap.All() {
		bond, ok := e.(*inventory.Bond)
		if !ok {
			continue
		}
		for _, sl := range bond.SlaveDetail {
			slaveRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: bond.GetRef().Node, ID: sl.Name}
			key := slaveRef.String()
			live[key] = true

			down := sl.MIIStatus != "" && !strings.EqualFold(sl.MIIStatus, "up")
			active := db.Evaluate(key, down, bondSlaveRiseCycles, bondSlaveFallCycles)
			if !active {
				continue
			}
			detail := fmt.Sprintf("bond %s on node %s has slave %s down (MII status %q) — traffic is riding on the remaining slave(s), if any",
				bond.Name, bond.GetRef().Node, sl.Name, sl.MIIStatus)
			f := newHealthFinding(CheckBondSlaveDown, SeverityWarning, detail,
				[]string{bond.GetRef().Node}, []string{bond.GetRef().String(), slaveRef.String()})
			f.DocsLink = bondSlaveDownDocsLink
			out = append(out, f)
		}
	}
	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
