package topology

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// collapseGuests replaces, for every (node, attachment-target) group whose
// guest NIC count exceeds DefaultCollapseThreshold, the individual
// guest/guest-nic nodes with a single summarized "N guests" pill node
// (docs/features/topology.md §1: "Collapsible per bridge (\"23 guests\"
// pill expands on click)"). groups is keyed by "<node>|<targetRefString>"
// as populated by buildNodes. A Guest node is dropped only when every one
// of its NICs collapsed (so a guest with NICs on two different targets,
// only one of which collapsed, still renders). Returns the adjusted node
// list and the synthetic group->target edges (the real per-guest edges for
// collapsed NICs are dropped implicitly by Project's nodeSet-based edge
// filter, since their endpoint no longer exists).
func collapseGuests(nodes []Node, groups map[string][]inventory.Ref, byRef map[inventory.Ref]inventory.Entity) ([]Node, []Edge) {
	toCollapse := map[string][]inventory.Ref{}
	for key, refs := range groups {
		if len(refs) > DefaultCollapseThreshold {
			toCollapse[key] = refs
		}
	}
	if len(toCollapse) == 0 {
		return nodes, nil
	}

	collapsedNicIDs := map[string]bool{}
	for _, refs := range toCollapse {
		for _, ref := range refs {
			collapsedNicIDs[ref.String()] = true
		}
	}

	// Per guest, how many of its NICs are present at all vs. collapsed, so a
	// Guest node is only dropped once every NIC it has is absorbed into some
	// group's pill.
	totalNics := map[string]int{}
	collapsedNics := map[string]int{}
	for _, n := range nodes {
		if n.Kind != string(inventory.KindGuestNic) {
			continue
		}
		ref, err := inventory.ParseRef(n.ID)
		if err != nil {
			continue
		}
		nic, ok := byRef[ref].(*inventory.GuestNic)
		if !ok {
			continue
		}
		guestID := nic.Guest.String()
		totalNics[guestID]++
		if collapsedNicIDs[n.ID] {
			collapsedNics[guestID]++
		}
	}
	fullyCollapsedGuests := map[string]bool{}
	for g, total := range totalNics {
		if total > 0 && collapsedNics[g] == total {
			fullyCollapsedGuests[g] = true
		}
	}

	filtered := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if collapsedNicIDs[n.ID] {
			continue
		}
		if n.Kind == string(inventory.KindGuest) && fullyCollapsedGuests[n.ID] {
			continue
		}
		filtered = append(filtered, n)
	}

	var groupEdges []Edge
	for key, refs := range toCollapse {
		node, targetRefStr, ok := strings.Cut(key, "|")
		if !ok {
			continue
		}
		targetRef, err := inventory.ParseRef(targetRefStr)
		if err != nil {
			continue
		}

		var downAny bool
		vidSet := map[int]bool{}
		for _, ref := range refs {
			nic, ok := byRef[ref].(*inventory.GuestNic)
			if !ok {
				continue
			}
			if nic.LinkDown {
				downAny = true
			}
			vidSet[nic.EffectiveVid] = true
		}
		status := StatusOK
		if downAny {
			status = StatusDegraded
		}
		badges := []string{fmt.Sprintf("count=%d", len(refs))}
		if len(vidSet) == 1 {
			for vid := range vidSet {
				if vid != 0 {
					badges = append(badges, "vid="+strconv.Itoa(vid))
				}
			}
		}

		groupID := "guest-group:" + node + ":" + targetRefStr
		filtered = append(filtered, Node{
			ID:             groupID,
			Kind:           "guest-group",
			Label:          fmt.Sprintf("%d guests", len(refs)),
			Layer:          LayerGuest,
			NodeGroup:      node,
			Status:         status,
			Badges:         badges,
			CollapsedCount: len(refs),
		})
		groupEdges = append(groupEdges, Edge{
			From:   groupID,
			To:     targetRef.String(),
			Kind:   string(inventory.EdgeAttachedTo),
			Badges: badges,
			Status: status,
		})
	}

	return filtered, groupEdges
}
