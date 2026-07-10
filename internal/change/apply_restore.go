package change

import (
	"context"
	"fmt"
)

// restoringTitle names the restoring draft after the changeset it reverses.
func restoringTitle(orig Changeset) string {
	base := orig.Title
	if base == "" {
		base = orig.ID
	}
	return fmt.Sprintf("Rollback of %s", base)
}

// buildRestoringOpsFromSnapshot synthesizes the ops for a restoring draft
// that reverses a committed changeset (docs/features/change-management.md
// §4: manual rollback of a committed changeset "creates a new restoring
// changeset via the normal flow"), by diffing orig's own pre-apply snapshot
// against each affected node's *current* live file (restoreOpsForNode,
// T-206).
//
// This subsumes T-205's per-op inverse synthesis (create<->delete, port
// add<->remove) and additionally handles delete/update ops, which T-205
// deferred here with a typed *ErrInverseUnsupported: an op-level inverse
// cannot recover a deleted/updated entity's prior field values from the op
// alone, but the pre-snapshot already has them, so diffing full entity state
// against live naturally reconstructs the right ops regardless of which
// combination of create/update/delete ops produced the difference — the
// same mechanism a plain POST /snapshots/{id}/restore uses.
func (s *Service) buildRestoringOpsFromSnapshot(ctx context.Context, orig Changeset) ([]Op, error) {
	pre, err := s.loadPreSnapshot(ctx, orig.ID)
	if err != nil {
		return nil, fmt.Errorf("change: loading pre-snapshot to build a restoring draft for changeset %s: %w", orig.ID, err)
	}
	var ops []Op
	for _, f := range pre {
		live, err := s.nodes.ReadInterfaces(ctx, f.Node)
		if err != nil {
			return nil, fmt.Errorf("change: reading live %s on node %s to build a restoring draft: %w", interfacesPath, f.Node, err)
		}
		nodeOps, err := restoreOpsForNode(f.Node, f.Content, live)
		if err != nil {
			return nil, err
		}
		ops = append(ops, nodeOps...)
	}
	return ops, nil
}
