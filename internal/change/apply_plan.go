package change

import "fmt"

// StepKind classifies one apply step per docs/architecture.md §4 /
// docs/data-model.md §3's ordering: cluster-scope PVE API calls, then
// per-node interface-file staging, then per-node reload, then sdn.apply
// last.
type StepKind string

const (
	// StepStageFile writes one node's new /etc/network/interfaces to its
	// staged interfaces.new (host writer), realizing that node's node-file
	// ops. It activates nothing on its own.
	StepStageFile StepKind = "stage_file"
	// StepReload applies one node's staged file and reloads the network
	// (ifreload). This is the connectivity-affecting step the commit-confirm
	// window guards.
	StepReload StepKind = "reload"
	// StepSDNApply applies pending cluster SDN config (PUT /cluster/sdn),
	// always last (docs/data-model.md §3).
	StepSDNApply StepKind = "sdn_apply"
)

// nodeFileOpTypes is the subset of the v1 op vocabulary that mutates a node's
// /etc/network/interfaces file — exactly the ops internal/change/ifaces (T-204)
// knows how to turn into file edits.
var nodeFileOpTypes = map[OpType]bool{
	OpIfaceUpdate:      true,
	OpBondCreate:       true,
	OpBondUpdate:       true,
	OpBondDelete:       true,
	OpBridgeCreate:     true,
	OpBridgeUpdate:     true,
	OpBridgeDelete:     true,
	OpBridgePortAdd:    true,
	OpBridgePortRemove: true,
	OpVlanCreate:       true,
	OpVlanUpdate:       true,
	OpVlanDelete:       true,
}

// Step is one entry in a rendered apply Plan — the shape the review screen's
// Plan tab (docs/features/change-management.md §3) renders and the executor
// runs, persisted verbatim into changesets.plan_json before apply.
type Step struct {
	Kind    StepKind `json:"kind"`
	Node    string   `json:"node,omitempty"`
	Summary string   `json:"summary"`
	// OpIdx lists the indices (into the changeset's Ops slice) this step
	// realizes, so the executor can recover the concrete ops for a step and
	// the UI can cross-link a step to its op cards. Empty for sdn.apply.
	OpIdx []int `json:"opIdx,omitempty"`
}

// Plan is a changeset's full ordered apply plan (changesets.plan_json).
type Plan struct {
	Steps []Step `json:"steps"`
}

// BuildPlan turns a changeset's ops into an ordered typed Plan per
// docs/architecture.md §4: node-file ops are grouped by node into a
// stage→reload pair per node (in first-appearance node order — the same
// deterministic ordering internal/change/ifaces.DiffChangeset uses so the
// plan and the diff agree), and a trailing sdn.apply step when the changeset
// carries one.
//
// The stage and reload steps for a node are emitted as an adjacent pair
// (stage nodeA, reload nodeA, stage nodeB, reload nodeB, ...) rather than all
// stages then all reloads: reload consumes the just-staged file, and pairing
// keeps each node transactional (PVE's own per-node ifupdown2 model,
// docs/architecture.md §4) and makes rollback a clean per-node file restore.
// This is a documented interpretation of the doc's category ordering for the
// multi-node case.
//
// It returns *ErrUnsupportedOp for any op the T-205 executor cannot run yet
// (guest/SDN-write/fw/ipam families), so an un-executable changeset is
// refused before any mutation rather than partially applied.
func BuildPlan(ops []Op) (Plan, error) {
	var nodeOrder []string
	byNode := map[string][]int{}
	sdnApply := false

	for i, op := range ops {
		switch {
		case nodeFileOpTypes[op.Type]:
			node := op.Target.Node
			if _, seen := byNode[node]; !seen {
				nodeOrder = append(nodeOrder, node)
			}
			byNode[node] = append(byNode[node], i)
		case op.Type == OpSdnApply:
			sdnApply = true
		default:
			return Plan{}, &ErrUnsupportedOp{OpType: op.Type}
		}
	}

	var steps []Step
	for _, node := range nodeOrder {
		idxs := byNode[node]
		steps = append(steps,
			Step{
				Kind:    StepStageFile,
				Node:    node,
				OpIdx:   idxs,
				Summary: fmt.Sprintf("Stage /etc/network/interfaces on %s (%d op(s))", node, len(idxs)),
			},
			Step{
				Kind:    StepReload,
				Node:    node,
				Summary: fmt.Sprintf("Reload network on %s (ifreload)", node),
			},
		)
	}
	if sdnApply {
		steps = append(steps, Step{
			Kind:    StepSDNApply,
			Summary: "Apply pending cluster SDN configuration",
		})
	}

	return Plan{Steps: steps}, nil
}

// affectedNodes returns, in first-appearance order, every node the plan's
// per-node steps touch — the set the pre-apply snapshot must capture and the
// post-terminal RefreshNow must refresh.
func (p Plan) affectedNodes() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Node != "" && !seen[s.Node] {
			seen[s.Node] = true
			out = append(out, s.Node)
		}
	}
	return out
}
