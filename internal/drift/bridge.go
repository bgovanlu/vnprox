// bridge.go implements docs/features/topology.md §6's first check family:
// "bridge presence/VLAN-awareness/VID sets for same-named bridges" —
// comparing every bridge name that appears on two or more cluster nodes
// (a same-named bridge existing in only one node's inventory is too
// idiosyncratic a shape to flag; the whole point of this check is
// divergence in what is otherwise meant to be "the same" bridge).

package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// checkBridgeDivergence is the CheckBridgeDivergence family: bridge
// presence, VLAN-awareness, and VID-set divergence across same-named
// bridges. VLAN-awareness/VID divergence is fixable via bridge-property
// harmonization (docs/features/topology.md §6's "each fix is an op patch
// through the normal drawer"); presence divergence is not (creating a
// missing bridge requires a physical-port assignment decision a drift
// checker cannot safely make on its own).
func checkBridgeDivergence(snap inventory.Snapshot) []Finding {
	var out []Finding
	byName := bridgesByName(snap)
	allNodes := clusterNodes(snap)

	for _, name := range sortedBridgeNames(byName) {
		byNode := byName[name]
		if len(byNode) < 2 {
			continue // only one node has this bridge: nothing to compare
		}
		present := make([]string, 0, len(byNode))
		for node := range byNode {
			present = append(present, node)
		}
		sort.Strings(present)

		if f, ok := presenceFinding(name, present, allNodes, byNode); ok {
			out = append(out, f)
		}
		if f, ok := vlanAwareFinding(name, present, byNode); ok {
			out = append(out, f)
		}
		if f, ok := vidSetFinding(name, present, byNode); ok {
			out = append(out, f)
		}
	}
	return out
}

// presenceFinding flags a same-named bridge present on some cluster nodes
// but missing from others.
func presenceFinding(name string, present, allNodes []string, byNode map[string]*inventory.Bridge) (Finding, bool) {
	presentSet := map[string]bool{}
	for _, n := range present {
		presentSet[n] = true
	}
	var missing []string
	for _, n := range allNodes {
		if !presentSet[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return Finding{}, false
	}
	refs := make([]string, 0, len(present))
	for _, n := range present {
		refs = append(refs, byNode[n].GetRef().String())
	}
	detail := fmt.Sprintf("bridge %s exists on %s but not on %s",
		name, strings.Join(present, ", "), strings.Join(missing, ", "))
	nodes := append(append([]string(nil), present...), missing...)
	f := newFinding(CheckBridgeDivergence, SeverityWarning, detail, nodes, refs)
	return f, true
}

// vlanAwareFinding flags disagreement in a same-named bridge's declared
// VLAN-awareness across nodes, with a computable fix aligning every
// dissenting node to the majority value.
func vlanAwareFinding(name string, present []string, byNode map[string]*inventory.Bridge) (Finding, bool) {
	votes := map[bool]int{}
	reported := map[string]bool{}
	for _, n := range present {
		br := byNode[n]
		if !br.VlanAwareSet {
			continue
		}
		reported[n] = br.VlanAware
		votes[br.VlanAware]++
	}
	if len(votes) < 2 {
		return Finding{}, false
	}
	canonical := majorityBool(votes)

	var refs, dissentNodes []string
	var ops []change.Op
	for _, n := range present {
		refs = append(refs, byNode[n].GetRef().String())
		val, ok := reported[n]
		if !ok || val == canonical {
			continue
		}
		dissentNodes = append(dissentNodes, n)
		v := canonical
		ops = append(ops, change.Op{
			Type:   change.OpBridgeUpdate,
			Target: byNode[n].GetRef(),
			Params: &change.BridgeUpdateParams{VlanAware: &v},
		})
	}
	detail := fmt.Sprintf("bridge %s VLAN-awareness disagrees across nodes: %s expect %v, but %s %s set to %v",
		name, strings.Join(present, ", "), canonical, strings.Join(dissentNodes, ", "),
		pluralIs(len(dissentNodes)), !canonical)
	f := newFinding(CheckBridgeDivergence, SeverityWarning, detail, present, refs)
	f = f.withFix(fmt.Sprintf("drift: align bridge %s VLAN-awareness to %v", name, canonical), ops)
	return f, true
}

// vidSetFinding flags disagreement in a same-named VLAN-aware bridge's
// declared VID set across nodes, with a computable fix aligning every
// dissenting node to the majority set.
func vidSetFinding(name string, present []string, byNode map[string]*inventory.Bridge) (Finding, bool) {
	canon := map[string]int{} // canonical vid-set string -> vote count
	byNodeKey := map[string]string{}
	for _, n := range present {
		br := byNode[n]
		if !br.VlanAware {
			continue
		}
		key := vidsKey(br.Vids)
		byNodeKey[n] = key
		canon[key]++
	}
	if len(canon) < 2 {
		return Finding{}, false
	}
	winnerKey, winnerVids := majorityVids(canon, byNode, present, byNodeKey)

	var refs, dissentNodes []string
	var ops []change.Op
	for _, n := range present {
		refs = append(refs, byNode[n].GetRef().String())
		key, ok := byNodeKey[n]
		if !ok || key == winnerKey {
			continue
		}
		dissentNodes = append(dissentNodes, n)
		vids := cloneVidRanges(winnerVids)
		cvids := toChangeVidRanges(vids)
		ops = append(ops, change.Op{
			Type:   change.OpBridgeUpdate,
			Target: byNode[n].GetRef(),
			Params: &change.BridgeUpdateParams{Vids: &cvids},
		})
	}
	detail := fmt.Sprintf("bridge %s VLAN ID sets differ across nodes: canonical %s, but %s %s",
		name, winnerKey, strings.Join(dissentNodes, ", "), pluralDiffer(len(dissentNodes)))
	f := newFinding(CheckBridgeDivergence, SeverityWarning, detail, present, refs)
	f = f.withFix(fmt.Sprintf("drift: align bridge %s VLAN IDs to %s", name, winnerKey), ops)
	return f, true
}

func pluralIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func pluralDiffer(n int) string {
	if n == 1 {
		return "differs"
	}
	return "differ"
}

func majorityBool(votes map[bool]int) bool {
	if votes[true] > votes[false] {
		return true
	}
	if votes[false] > votes[true] {
		return false
	}
	// Tie: prefer true deterministically (VLAN-aware is the safer default
	// to converge on — a non-aware bridge silently drops tagged traffic).
	return true
}

func majorityVids(canon map[string]int, byNode map[string]*inventory.Bridge, present []string, byNodeKey map[string]string) (string, []inventory.VidRange) {
	bestKey, bestVotes := "", -1
	keys := make([]string, 0, len(canon))
	for k := range canon {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if canon[k] > bestVotes {
			bestKey, bestVotes = k, canon[k]
		}
	}
	for _, n := range present {
		if byNodeKey[n] == bestKey {
			return bestKey, byNode[n].Vids
		}
	}
	return bestKey, nil
}

func vidsKey(vids []inventory.VidRange) string {
	parts := make([]string, len(vids))
	for i, v := range vids {
		parts[i] = v.String()
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ",")
}

func cloneVidRanges(vids []inventory.VidRange) []inventory.VidRange {
	return append([]inventory.VidRange(nil), vids...)
}

func toChangeVidRanges(vids []inventory.VidRange) []change.VidRange {
	out := make([]change.VidRange, len(vids))
	for i, v := range vids {
		out[i] = change.VidRange{Low: v.Low, High: v.High}
	}
	return out
}
