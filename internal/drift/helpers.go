package drift

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// clusterNodes returns every cluster member's node name known to snap
// (every inventory.Node entity — docs/data-model.md §1), sorted. Used by
// checks that need to reason about "which nodes should have this bridge"
// (presence divergence, SDN realization) beyond just the nodes that happen
// to already report the entity in question.
func clusterNodes(snap inventory.Snapshot) []string {
	var out []string
	for _, e := range snap.All() {
		if n, ok := e.(*inventory.Node); ok {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}

// bridgesByName groups every Bridge/OVSBridge entity in snap by its Name,
// mapping to a further map keyed by node — the shape every cross-node
// bridge check (bridge.go, mtu.go) starts from.
func bridgesByName(snap inventory.Snapshot) map[string]map[string]*inventory.Bridge {
	out := map[string]map[string]*inventory.Bridge{}
	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok {
			continue
		}
		byNode := out[br.Name]
		if byNode == nil {
			byNode = map[string]*inventory.Bridge{}
			out[br.Name] = byNode
		}
		byNode[br.GetRef().Node] = br
	}
	return out
}

// sortedBridgeNames returns the names of m in stable order, for
// deterministic finding iteration.
func sortedBridgeNames(m map[string]map[string]*inventory.Bridge) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// portMTU resolves a bridge port Ref (PhysNic, Bond, or VlanIface) to its
// runtime MTU, falling back to declared MTU when runtime is unreported
// (zero). ok is false if ref does not resolve or has no MTU information.
func portMTU(snap inventory.Snapshot, ref inventory.Ref) (mtu int, ok bool) {
	e, found := snap.Get(ref)
	if !found {
		return 0, false
	}
	switch v := e.(type) {
	case *inventory.PhysNic:
		return effectiveMTU(v.MTU, v.MTUDeclared)
	case *inventory.Bond:
		return effectiveMTU(v.MTU, v.MTUDeclared)
	case *inventory.VlanIface:
		return effectiveMTU(v.MTU, v.MTUDeclared)
	default:
		return 0, false
	}
}

func effectiveMTU(runtime, declared int) (int, bool) {
	if runtime != 0 {
		return runtime, true
	}
	if declared != 0 {
		return declared, true
	}
	return 0, false
}
