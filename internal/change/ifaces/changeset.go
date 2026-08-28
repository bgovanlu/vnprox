// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// interfacesPath is the file path node-file-affecting ops always mutate.
// Other changeset diff contributors (SDN configs, firewall files) belong
// to other tasks' file groups; this package only ever emits this one path.
const interfacesPath = "/etc/network/interfaces"

// FileDiff is one file's rendered diff for one node — the "per-file
// unified diffs" half of docs/api.md's `GET /changesets/{id}/diff` shape.
type FileDiff struct {
	Node    string `json:"node"`
	Path    string `json:"path"`
	Unified string `json:"unified"`
	Changed bool   `json:"changed"`
}

// ChangesetDiff is the full rendered diff for a changeset's
// node-file-affecting ops: every touched node's file diff, plus a
// structured summary per op in the caller's original order. It is exactly
// what `GET /changesets/{id}/diff` (docs/api.md) needs to return for the
// ops this package understands; the route is wired by internal/change's
// Service.Diff (T-205), which composes this with any sdn/fw/guest-op
// contributions other packages may add later.
type ChangesetDiff struct {
	Files []FileDiff  `json:"files"`
	Ops   []OpSummary `json:"ops"`
}

// DiffChangeset renders the file diffs and op summaries for ops (assumed to
// be every node-file-affecting op in one changeset, in the order the user
// added them). For each node any op targets, it reads that node's current
// /etc/network/interfaces via reader, parses it, applies that node's ops in
// order with Mutate, and renders the unified diff between the original and
// mutated content. Nodes are processed in first-appearance order across
// ops, which is also the order FileDiff entries come back in — this
// function has no other source of ordering (no map iteration) so its
// output is deterministic for a given ops slice.
//
// DiffChangeset is deliberately a pure function over a host.Reader and a
// concrete op slice — no changeset store or HTTP types — so the diff logic
// can be exercised directly in tests and reused by internal/change's
// Service.Diff, which wires it to the `GET /changesets/{id}/diff` route the
// task card requires.
func DiffChangeset(ctx context.Context, reader host.Reader, ops []Op, changesetID string) (*ChangesetDiff, error) {
	nodeOrder := make([]string, 0, 4)
	byNode := make(map[string][]Op, 4)
	for _, op := range ops {
		node := op.Ref().Node
		if _, ok := byNode[node]; !ok {
			nodeOrder = append(nodeOrder, node)
		}
		byNode[node] = append(byNode[node], op)
	}

	out := &ChangesetDiff{
		Files: make([]FileDiff, 0, len(nodeOrder)),
		Ops:   make([]OpSummary, 0, len(ops)),
	}
	for _, op := range ops {
		out.Ops = append(out.Ops, Summarize(op))
	}

	for _, node := range nodeOrder {
		before, err := reader.InterfacesFile(ctx, node, false)
		if err != nil {
			return nil, fmt.Errorf("ifaces: diffing changeset %s: reading %s on node %s: %w", changesetID, interfacesPath, node, err)
		}
		f, err := host.ParseInterfaces([]byte(before))
		if err != nil {
			return nil, fmt.Errorf("ifaces: diffing changeset %s: parsing %s on node %s: %w", changesetID, interfacesPath, node, err)
		}
		if err := MutateAll(f, byNode[node], changesetID); err != nil {
			return nil, fmt.Errorf("ifaces: diffing changeset %s: %w", changesetID, err)
		}
		after := f.Render()
		unified := UnifiedDiff(interfacesPath, interfacesPath, before, after)
		out.Files = append(out.Files, FileDiff{
			Node: node, Path: interfacesPath,
			Unified: unified, Changed: unified != "",
		})
	}
	return out, nil
}
