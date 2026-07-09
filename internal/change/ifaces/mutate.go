package ifaces

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// Mutate applies one op to f in place, producing a minimal edit per the
// package doc comment's contract (untouched stanzas byte-identical,
// comments preserved, managed-by-vnprox marker on newly created stanzas,
// deterministic append ordering). changesetID is stamped into the
// managed-by comment of any stanza the op newly creates; it is ignored by
// ops that only edit or remove existing stanzas.
func Mutate(f *host.File, op Op, changesetID string) error {
	switch o := op.(type) {
	case IfaceUpdate:
		return mutateIfaceUpdate(f, o)
	case BondCreate:
		return mutateBondCreate(f, o, changesetID)
	case BondUpdate:
		return mutateBondUpdate(f, o)
	case BondDelete:
		return mutateBondDelete(f, o)
	case BridgeCreate:
		return mutateBridgeCreate(f, o, changesetID)
	case BridgeUpdate:
		return mutateBridgeUpdate(f, o)
	case BridgeDelete:
		return mutateBridgeDelete(f, o)
	case BridgePortAdd:
		return mutateBridgePortAdd(f, o)
	case BridgePortRemove:
		return mutateBridgePortRemove(f, o)
	case VlanCreate:
		return mutateVlanCreate(f, o, changesetID)
	case VlanUpdate:
		return mutateVlanUpdate(f, o)
	case VlanDelete:
		return mutateVlanDelete(f, o)
	default:
		return fmt.Errorf("ifaces: mutate: unsupported op %T", op)
	}
}

// MutateAll applies ops to f in order, stopping at the first error. It is a
// convenience for callers (DiffChangeset, tests) applying every
// node-file-affecting op targeting one node's file in one pass; the
// resulting stanza order for any ops that create new entities is exactly
// the order those ops appear in ops (see the package doc comment).
func MutateAll(f *host.File, ops []Op, changesetID string) error {
	for i, op := range ops {
		if err := Mutate(f, op, changesetID); err != nil {
			return fmt.Errorf("ifaces: applying op[%d] (%s %s): %w", i, op.Kind(), op.Ref(), err)
		}
	}
	return nil
}
