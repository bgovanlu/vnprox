// SPDX-License-Identifier: Apache-2.0

package blueprint

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Instantiate is docs/features/blueprints.md §1's "pick blueprint -> fill
// parameters -> vnprox expands to a changeset draft" pipeline, minus the
// actual draft creation (the caller — Service.Instantiate, or a test —
// hands the returned ops to change.Service.Create, which is what actually
// stages/validates the draft; this function never touches the store or
// the change engine's validators itself). Idempotent: instantiating twice
// against an unchanged snapshot in between produces the same ops the
// first time, and an empty slice the second (T-603 AC1/AC2).
func Instantiate(bp *Blueprint, req InstantiateRequest, snap inventory.Snapshot) ([]change.Op, error) {
	if err := Validate(bp); err != nil {
		return nil, err
	}
	nodes := req.Nodes
	if len(nodes) == 0 {
		nodes = clusterNodes(snap)
	}
	bindings, err := resolveParams(bp, req.Params, nodes)
	if err != nil {
		return nil, err
	}
	entities, err := expand(bp, bindings, nodes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}

	var ops []change.Op
	for _, e := range entities {
		entOps, err := diffEntity(e, snap)
		if err != nil {
			return nil, fmt.Errorf("blueprint: diffing %s: %w", e.ref, err)
		}
		ops = append(ops, entOps...)
	}
	return ops, nil
}
