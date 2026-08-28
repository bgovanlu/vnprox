// SPDX-License-Identifier: Apache-2.0

package blueprint

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// resolveParams merges supplied against bp's declared params (applying
// defaults, rejecting unknown names, rejecting a missing required param
// with no default, and type-checking every value — T-603 AC4), then
// layers in the builtin "__nodes__" binding from nodes. The returned
// bindings map is what substituteValue/substituteFields consume.
func resolveParams(bp *Blueprint, supplied map[string]any, nodes []string) (map[string]any, error) {
	declared := make(map[string]ParamDef, len(bp.Params))
	for _, p := range bp.Params {
		declared[p.Name] = p
	}
	for name := range supplied {
		if _, ok := declared[name]; !ok {
			return nil, fmt.Errorf("%w: unknown param %q", ErrInvalidParams, name)
		}
	}

	bindings := make(map[string]any, len(bp.Params)+1)
	for _, def := range bp.Params {
		v, given := supplied[def.Name]
		if !given {
			if def.Default != nil {
				v = def.Default
			} else if def.Required {
				return nil, fmt.Errorf("%w: %s is required", ErrInvalidParams, def.Name)
			} else {
				continue // optional, unsupplied, no default: leave unbound
			}
		}
		if err := validateParamValue(def, v); err != nil {
			return nil, err
		}
		bindings[def.Name] = v
	}

	nodesAny := make([]any, len(nodes))
	for i, n := range nodes {
		nodesAny[i] = n
	}
	bindings[builtinNodes] = nodesAny
	return bindings, nil
}

// clusterNodes returns every inventory.Node's name known to snap, sorted —
// the default target node list when an InstantiateRequest omits Nodes
// (mirrors internal/drift/helpers.go's identical helper, duplicated here
// since that package's is unexported and this one has no other reason to
// import internal/drift).
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
