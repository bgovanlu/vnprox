// SPDX-License-Identifier: Apache-2.0

// bridge.go: the same-named-bridge presence / VLAN-awareness / VID-set
// comparison — docs/features/topology.md §6's first check family. Only bridge
// names that appear on two or more cluster nodes are compared (a same-named
// bridge on a single node is too idiosyncratic a shape to call "divergence";
// the point of this check is disagreement in what is otherwise meant to be
// "the same" bridge). This is the exact logic internal/drift previously
// carried inline in its own bridge.go; drift and internal/change now both
// call BridgeDivergences.

package xnode

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// BridgeDivergences reports every same-named bridge's presence,
// VLAN-awareness, and VID-set divergence across nodes. VLAN-awareness and
// VID-set divergences carry a computable fix (harmonizing every dissenting
// node to the majority value); presence divergence does not (creating a
// missing bridge needs a physical-port decision no comparison can make).
func BridgeDivergences(src Source) []Divergence {
	var out []Divergence
	byName := BridgesByName(src)
	allNodes := ClusterNodes(src)

	for _, name := range SortedBridgeNames(byName) {
		byNode := byName[name]
		if len(byNode) < 2 {
			continue // only one node has this bridge: nothing to compare
		}
		present := make([]string, 0, len(byNode))
		for node := range byNode {
			present = append(present, node)
		}
		sort.Strings(present)

		if d, ok := presenceDivergence(name, present, allNodes, byNode); ok {
			out = append(out, d)
		}
		if d, ok := vlanAwareDivergence(name, present, byNode); ok {
			out = append(out, d)
		}
		if d, ok := vidSetDivergence(name, present, byNode); ok {
			out = append(out, d)
		}
	}
	return out
}

// presenceDivergence flags a same-named bridge present on some cluster nodes
// but missing from others. Detection-only.
func presenceDivergence(name string, present, allNodes []string, byNode map[string]*inventory.Bridge) (Divergence, bool) {
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
		return Divergence{}, false
	}
	refs := make([]string, 0, len(present))
	for _, n := range present {
		refs = append(refs, byNode[n].GetRef().String())
	}
	detail := fmt.Sprintf("bridge %s exists on %s but not on %s",
		name, strings.Join(present, ", "), strings.Join(missing, ", "))
	nodes := append(append([]string(nil), present...), missing...)
	return Divergence{
		Family:  FamilyBridgeDivergence,
		Subject: name,
		Detail:  detail,
		Nodes:   sortedUnique(nodes),
		Refs:    sortedUnique(refs),
	}, true
}

// vlanAwareDivergence flags disagreement in a same-named bridge's declared
// VLAN-awareness across nodes, with a fix aligning every dissenting node to
// the majority value.
func vlanAwareDivergence(name string, present []string, byNode map[string]*inventory.Bridge) (Divergence, bool) {
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
		return Divergence{}, false
	}
	canonical := majorityBool(votes)

	var refs, dissentNodes []string
	var fixes []BridgeFix
	for _, n := range present {
		refs = append(refs, byNode[n].GetRef().String())
		val, ok := reported[n]
		if !ok || val == canonical {
			continue
		}
		dissentNodes = append(dissentNodes, n)
		v := canonical
		fixes = append(fixes, BridgeFix{Target: byNode[n].GetRef(), VlanAware: &v})
	}
	detail := fmt.Sprintf("bridge %s VLAN-awareness disagrees across nodes: %s expect %v, but %s %s set to %v",
		name, strings.Join(present, ", "), canonical, strings.Join(dissentNodes, ", "),
		pluralIs(len(dissentNodes)), !canonical)
	return Divergence{
		Family:   FamilyBridgeDivergence,
		Subject:  name,
		Detail:   detail,
		FixTitle: fmt.Sprintf("align bridge %s VLAN-awareness to %v", name, canonical),
		Nodes:    sortedUnique(present),
		Refs:     sortedUnique(refs),
		Fixes:    fixes,
	}, true
}

// vidSetDivergence flags disagreement in a same-named VLAN-aware bridge's
// declared VID set across nodes, with a fix aligning every dissenting node to
// the majority set.
func vidSetDivergence(name string, present []string, byNode map[string]*inventory.Bridge) (Divergence, bool) {
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
		return Divergence{}, false
	}
	winnerKey, winnerVids := majorityVids(canon, byNode, present, byNodeKey)

	var refs, dissentNodes []string
	var fixes []BridgeFix
	for _, n := range present {
		refs = append(refs, byNode[n].GetRef().String())
		key, ok := byNodeKey[n]
		if !ok || key == winnerKey {
			continue
		}
		dissentNodes = append(dissentNodes, n)
		vids := cloneVidRanges(winnerVids)
		fixes = append(fixes, BridgeFix{Target: byNode[n].GetRef(), Vids: &vids})
	}
	detail := fmt.Sprintf("bridge %s VLAN ID sets differ across nodes: canonical %s, but %s %s",
		name, winnerKey, strings.Join(dissentNodes, ", "), pluralDiffer(len(dissentNodes)))
	return Divergence{
		Family:   FamilyBridgeDivergence,
		Subject:  name,
		Detail:   detail,
		FixTitle: fmt.Sprintf("align bridge %s VLAN IDs to %s", name, winnerKey),
		Nodes:    sortedUnique(present),
		Refs:     sortedUnique(refs),
		Fixes:    fixes,
	}, true
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
	// Tie: prefer true deterministically (VLAN-aware is the safer default to
	// converge on — a non-aware bridge silently drops tagged traffic).
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
