// health_trunkvlans.go implements docs/features/monitoring.md §5's
// "trunk_unused_vlans" health check (T-803, informational): a VLAN-aware
// bridge's trunked VID set (inventory.Bridge.Vids, from host-netlink's
// bridge VLAN table) contains a VID no guest NIC on that bridge/VNet
// actually uses (inventory.GuestNic.EffectiveVid, the already-resolved VLAN
// including VNet tag propagation). Detection only, and deliberately never
// auto-narrows the trunk — a VID a guest doesn't currently use may still be
// reserved for planned capacity, an external device on that switch port, or
// simply not yet migrated; this is a "you might be able to tidy this up"
// signal, not a correctness problem.
//
// Hysteresis-exempt (mgmt_single_path-style): a trunk's allowed VID set and
// which VIDs guests actually use are both structural configuration facts,
// not noisy live counters — there is nothing to debounce.

package findings

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const CheckTrunkUnusedVlans = "trunk_unused_vlans"

const trunkUnusedVlansDocsLink = "docs/features/monitoring.md#5-health-checks"

// maxUnusedVlansListed caps how many individual VIDs checkTrunkUnusedVlans
// names in a finding's detail text — a full default trunk (e.g. "2-4094")
// with only a handful of VIDs in guest use would otherwise produce an
// unreadable, multi-thousand-entry detail string for a single finding.
const maxUnusedVlansListed = 20

// checkTrunkUnusedVlans returns one finding per VLAN-aware bridge whose
// trunked VID set contains at least one VID no GuestNic attached to it
// currently uses.
func checkTrunkUnusedVlans(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok || !br.VlanAware || len(br.Vids) == 0 {
			continue
		}

		used := usedVids(snap, br.GetRef())
		var unused []int
		for _, vr := range br.Vids {
			for vid := vr.Low; vid <= vr.High; vid++ {
				if !used[vid] {
					unused = append(unused, vid)
				}
			}
		}
		if len(unused) == 0 {
			continue
		}
		sort.Ints(unused)

		detail := fmt.Sprintf("bridge %s on node %s trunks VID(s) %s that no guest NIC on it currently uses",
			br.Name, br.GetRef().Node, formatVidList(unused))
		f := newHealthFinding(CheckTrunkUnusedVlans, SeverityInfo, detail, []string{br.GetRef().Node}, []string{br.GetRef().String()})
		f.DocsLink = trunkUnusedVlansDocsLink
		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// usedVids collects every EffectiveVid a GuestNic attached to bridgeRef
// declares.
func usedVids(snap inventory.Snapshot, bridgeRef inventory.Ref) map[int]bool {
	used := map[int]bool{}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok || nic.BridgeOrVnet != bridgeRef {
			continue
		}
		if nic.EffectiveVid != 0 {
			used[nic.EffectiveVid] = true
		}
	}
	return used
}

// formatVidList renders vids (already sorted) as a comma-joined list,
// capped at maxUnusedVlansListed entries with a "... and N more" suffix.
func formatVidList(vids []int) string {
	shown := vids
	truncated := 0
	if len(vids) > maxUnusedVlansListed {
		shown = vids[:maxUnusedVlansListed]
		truncated = len(vids) - maxUnusedVlansListed
	}
	parts := make([]string, len(shown))
	for i, v := range shown {
		parts[i] = strconv.Itoa(v)
	}
	s := strings.Join(parts, ", ")
	if truncated > 0 {
		s += fmt.Sprintf(", ... and %d more", truncated)
	}
	return s
}
