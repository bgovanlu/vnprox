// SPDX-License-Identifier: Apache-2.0

package topology

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// KindPhysGroup is the topology.Node.Kind value for a synthesized
// physical-layer per-node summary pill (T-1907). Like KindSwitch, it has no
// single backing inventory entity — it summarizes N PhysNic entities on one
// cluster node — so it only ever appears as a rendered topology.Node, never
// as a valid inventory Ref / GET /inventory/{ref} target.
const KindPhysGroup = "phys-group"

// physGroupID is the rendered id of the per-node physical-layer summary
// pill for node: the "phys-group:<node>" convention, mirroring
// collapseGuests' "guest-group:<node>:<targetRef>" ids (types.go's Node doc
// comment) — deliberately not a valid inventory Ref, so the frontend must
// special-case it as "expand this pill" rather than "open the inspector",
// exactly as it already must for a guest-group id.
func physGroupID(node string) string {
	return "phys-group:" + node
}

// collapsePhysical replaces, for every cluster node whose PhysNic count
// exceeds DefaultPhysicalCollapseThreshold, the individual physnic nodes
// with one "N NICs" per-node summary pill (docs/features/topology.md §4:
// "physical layer collapses to per-node summary" — the gap T-607's docs
// audit flagged and T-1907 closes), mirroring collapseGuests' threshold ->
// synthetic summary -> expand-on-demand mechanism rather than inventing a
// second one.
//
// Grouping is deliberately coarser than collapseGuests': a guest's
// attachment point (bridge/VNet) is itself the natural summary target, so
// collapseGuests groups per (node, target) and gives each pill exactly one
// outgoing edge to expand from. A node's physical NICs have no single such
// target — they fan out to whichever bonds/bridges happen to be configured,
// or nowhere at all — and docs/features/topology.md §4 itself says "a
// per-node summary", singular, not "one pill per attachment point". So this
// groups strictly by node, and carries its member refs directly on the
// synthetic Node (Members) rather than relying on a shared target's own
// /inventory/{ref} `related` list the way web/src/topology/expand.ts's
// guest-group trick does — see types.go's Members doc comment for why that
// trick has no physical-layer analog.
//
// snapEdges is the full (pre-collapse) inventory edge list. It is consulted
// only to synthesize connectivity edges from the pill to whatever
// bonds/bridges its collapsed NICs fed into (enslaved-by/port-of), badged
// with a count and painted with the worst status among the NICs feeding
// that target, so the pill still renders wired into the L2 layer instead of
// a disconnected island — the same reason T-902's client-side capsule LOD
// band redirects/merges its members' edges rather than dropping them.
// LLDP adjacency (a physnic's own lldp-adjacent edge to its discovered
// neighbor) is not re-synthesized: LldpNeighbor entities, and the
// KindSwitch nodes switches.go merges them into, are LayerPhysical too but
// are left individually rendered, untouched by this pass — mirroring
// T-902's own documented scope cut ("lldp-neighbor entities... intentionally
// excluded from the capsule's scope"). A collapsed NIC's own attach edge to
// its raw neighbor observation is the one piece of per-NIC detail this pass
// intentionally drops; the neighbor/switch itself never disappears, and
// still renders (missing that one edge) as long as some other NIC anywhere
// keeps it wired in.
func collapsePhysical(nodes []Node, snapEdges []inventory.Edge) ([]Node, []Edge) {
	byNodeGroup := map[string][]Node{}
	for _, n := range nodes {
		if n.Kind != string(inventory.KindPhysNic) {
			continue
		}
		byNodeGroup[n.NodeGroup] = append(byNodeGroup[n.NodeGroup], n)
	}

	toCollapse := map[string][]Node{}
	for node, ns := range byNodeGroup {
		if len(ns) > DefaultPhysicalCollapseThreshold {
			toCollapse[node] = ns
		}
	}
	if len(toCollapse) == 0 {
		return nodes, nil
	}

	collapsedIDs := map[string]bool{}
	statusByID := map[string]Status{}
	for _, ns := range toCollapse {
		for _, n := range ns {
			collapsedIDs[n.ID] = true
			statusByID[n.ID] = n.Status
		}
	}

	filtered := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if !collapsedIDs[n.ID] {
			filtered = append(filtered, n)
		}
	}

	// Only emit a synthesized group edge to a target that actually survived
	// filtering (a ?layers=/?node= filter, or the target simply not
	// existing) — the same "never a dangling edge" discipline buildEdges
	// applies everywhere else.
	survivingIDs := make(map[string]bool, len(filtered))
	for _, n := range filtered {
		survivingIDs[n.ID] = true
	}

	type targetKey struct {
		node string
		kind inventory.EdgeKind
		to   inventory.Ref
	}
	targetCount := map[targetKey]int{}
	targetStatus := map[targetKey]Status{}
	var targetOrder []targetKey
	for _, e := range snapEdges {
		if e.Kind != inventory.EdgeEnslavedBy && e.Kind != inventory.EdgePortOf {
			continue
		}
		if e.From.Kind != inventory.KindPhysNic {
			continue
		}
		fromID := e.From.String()
		if !collapsedIDs[fromID] {
			continue
		}
		key := targetKey{node: e.From.Node, kind: e.Kind, to: e.To}
		if _, seen := targetCount[key]; !seen {
			targetOrder = append(targetOrder, key)
		}
		targetCount[key]++
		if cur, ok := targetStatus[key]; ok {
			targetStatus[key] = worstStatus(cur, statusByID[fromID])
		} else {
			targetStatus[key] = statusByID[fromID]
		}
	}

	var groupEdges []Edge
	var groupNodeOrder []string
	for node := range toCollapse {
		groupNodeOrder = append(groupNodeOrder, node)
	}
	sort.Strings(groupNodeOrder)

	for _, node := range groupNodeOrder {
		ns := toCollapse[node]
		groupID := physGroupID(node)

		members := make([]string, len(ns))
		status := StatusOK
		for i, n := range ns {
			members[i] = n.ID
			status = worstStatus(status, n.Status)
		}
		sort.Strings(members)

		filtered = append(filtered, Node{
			ID:             groupID,
			Kind:           KindPhysGroup,
			Label:          fmt.Sprintf("%d NICs", len(ns)),
			Layer:          LayerPhysical,
			NodeGroup:      node,
			Status:         status,
			Badges:         []string{"count=" + strconv.Itoa(len(ns))},
			CollapsedCount: len(ns),
			Members:        members,
		})

		for _, key := range targetOrder {
			if key.node != node {
				continue
			}
			toID := key.to.String()
			if !survivingIDs[toID] {
				continue
			}
			groupEdges = append(groupEdges, Edge{
				From:   groupID,
				To:     toID,
				Kind:   string(key.kind),
				Badges: []string{"count=" + strconv.Itoa(targetCount[key])},
				Status: targetStatus[key],
			})
		}
	}

	sort.Slice(groupEdges, func(i, j int) bool {
		if groupEdges[i].From != groupEdges[j].From {
			return groupEdges[i].From < groupEdges[j].From
		}
		if groupEdges[i].To != groupEdges[j].To {
			return groupEdges[i].To < groupEdges[j].To
		}
		return groupEdges[i].Kind < groupEdges[j].Kind
	})

	return filtered, groupEdges
}
