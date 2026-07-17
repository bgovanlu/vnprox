// mtu.go: the cross-node half of docs/features/topology.md §6's MTU
// consistency family — the same-named bridge's MTU differing across cluster
// nodes, fixable via "MTU alignment" (a bridge.update aligning every outlier
// node to the majority MTU). The within-node path sub-check (a bridge's MTU
// vs. its own ports' MTU) stays in internal/drift: it is not a cross-node
// comparison and has no computable fix, so it has no second consumer to
// share with. This is the exact logic internal/drift previously carried
// inline in mtuCrossNodeFindings.

package xnode

import (
	"fmt"
	"sort"
	"strings"
)

// CrossNodeMTU reports every same-named bridge whose MTU disagrees across the
// cluster nodes that have it, with a fix aligning each outlier node to the
// majority MTU.
func CrossNodeMTU(src Source) []Divergence {
	var out []Divergence
	byName := BridgesByName(src)
	for _, name := range SortedBridgeNames(byName) {
		byNode := byName[name]
		if len(byNode) < 2 {
			continue
		}
		present := make([]string, 0, len(byNode))
		for node := range byNode {
			present = append(present, node)
		}
		sort.Strings(present)

		mtuByNode := map[string]int{}
		votes := map[int]int{}
		for _, n := range present {
			mtu, ok := EffectiveMTU(byNode[n].MTU, byNode[n].MTUDeclared)
			if !ok {
				continue
			}
			mtuByNode[n] = mtu
			votes[mtu]++
		}
		if len(votes) < 2 {
			continue
		}
		canonical := majorityInt(votes)

		var refs, dissentNodes []string
		var fixes []BridgeFix
		for _, n := range present {
			refs = append(refs, byNode[n].GetRef().String())
			mtu, ok := mtuByNode[n]
			if !ok || mtu == canonical {
				continue
			}
			dissentNodes = append(dissentNodes, fmt.Sprintf("%s=%d", n, mtu))
			m := canonical
			fixes = append(fixes, BridgeFix{Target: byNode[n].GetRef(), MTU: &m})
		}
		if len(fixes) == 0 {
			continue
		}
		detail := fmt.Sprintf("bridge %s MTU has drifted across the cluster: %s (canonical %d)",
			name, strings.Join(dissentNodes, ", "), canonical)
		out = append(out, Divergence{
			Family:   FamilyMTUConsistency,
			Subject:  name,
			Detail:   detail,
			FixTitle: fmt.Sprintf("align bridge %s MTU to %d", name, canonical),
			Nodes:    sortedUnique(present),
			Refs:     sortedUnique(refs),
			Fixes:    fixes,
		})
	}
	return out
}

func majorityInt(votes map[int]int) int {
	bestVal, bestVotes := 0, -1
	vals := make([]int, 0, len(votes))
	for v := range votes {
		vals = append(vals, v)
	}
	sort.Ints(vals)
	for _, v := range vals {
		if votes[v] > bestVotes {
			bestVal, bestVotes = v, votes[v]
		}
	}
	return bestVal
}
