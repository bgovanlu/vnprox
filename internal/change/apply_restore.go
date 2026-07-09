package change

import "fmt"

// ErrInverseUnsupported is returned when a committed changeset contains an op
// whose structural inverse T-205 does not synthesize (delete/update ops,
// whose faithful reconstruction needs the stored pre-snapshot *content* — the
// snapshot→draft restore path T-206 owns). Rollback of a committed changeset
// made only of exactly-invertible ops (create/port ops) still works; this
// error keeps T-205 from emitting a wrong inverse rather than approximating
// one.
type ErrInverseUnsupported struct {
	OpType OpType
}

func (e *ErrInverseUnsupported) Error() string {
	return fmt.Sprintf("change: cannot synthesize inverse of op %q for a restoring draft (needs T-206 snapshot restore)", e.OpType)
}

// buildRestoringOps synthesizes the ops for a restoring draft that reverses a
// committed changeset (docs/features/change-management.md §4: manual rollback
// of a committed changeset "creates a new restoring changeset via the normal
// flow"). Ops are inverted in reverse order so dependencies unwind correctly
// (delete B before delete A when the forward order created A then B).
//
// T-205 synthesizes inverses for the exactly-invertible ops — create↔delete
// and bridge.port.add↔remove — which need no pre-state lookup and whose diff
// is a precise inverse. Delete/update ops return *ErrInverseUnsupported (see
// its doc): their faithful inversion is the T-206 snapshot-restore path.
func buildRestoringOps(ops []Op) ([]Op, error) {
	out := make([]Op, 0, len(ops))
	for i := len(ops) - 1; i >= 0; i-- {
		inv, skip, err := inverseOp(ops[i])
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		out = append(out, inv)
	}
	return out, nil
}

// inverseOp returns the inverse of a single op. skip is true for ops that
// have no file/diff effect to reverse (sdn.apply).
func inverseOp(op Op) (inv Op, skip bool, err error) {
	switch op.Type {
	case OpBridgeCreate:
		return Op{Type: OpBridgeDelete, Target: op.Target, Params: &BridgeDeleteParams{}}, false, nil
	case OpBondCreate:
		return Op{Type: OpBondDelete, Target: op.Target, Params: &BondDeleteParams{}}, false, nil
	case OpVlanCreate:
		return Op{Type: OpVlanDelete, Target: op.Target, Params: &VlanDeleteParams{}}, false, nil
	case OpBridgePortAdd:
		p, ok := op.Params.(*BridgePortAddParams)
		if !ok {
			return Op{}, false, fmt.Errorf("change: op %q has unexpected params type %T", op.Type, op.Params)
		}
		return Op{Type: OpBridgePortRemove, Target: op.Target, Params: &BridgePortRemoveParams{Port: p.Port}}, false, nil
	case OpBridgePortRemove:
		p, ok := op.Params.(*BridgePortRemoveParams)
		if !ok {
			return Op{}, false, fmt.Errorf("change: op %q has unexpected params type %T", op.Type, op.Params)
		}
		return Op{Type: OpBridgePortAdd, Target: op.Target, Params: &BridgePortAddParams{Port: p.Port}}, false, nil
	case OpSdnApply:
		return Op{}, true, nil
	default:
		return Op{}, false, &ErrInverseUnsupported{OpType: op.Type}
	}
}

// restoringTitle names the restoring draft after the changeset it reverses.
func restoringTitle(orig Changeset) string {
	base := orig.Title
	if base == "" {
		base = orig.ID
	}
	return fmt.Sprintf("Rollback of %s", base)
}
