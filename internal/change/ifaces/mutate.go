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
	// Every line a mutator generates uses the file's dominant line
	// terminator, so a CRLF file stays consistently CRLF (see dominantEOL).
	nl := dominantEOL(f)
	switch o := op.(type) {
	case IfaceUpdate:
		return mutateIfaceUpdate(f, o, nl)
	case BondCreate:
		return mutateBondCreate(f, o, changesetID, nl)
	case BondUpdate:
		return mutateBondUpdate(f, o, nl)
	case BondDelete:
		return mutateBondDelete(f, o, nl)
	case BridgeCreate:
		return mutateBridgeCreate(f, o, changesetID, nl)
	case BridgeUpdate:
		return mutateBridgeUpdate(f, o, nl)
	case BridgeDelete:
		return mutateBridgeDelete(f, o, nl)
	case BridgePortAdd:
		return mutateBridgePortAdd(f, o, nl)
	case BridgePortRemove:
		return mutateBridgePortRemove(f, o, nl)
	case VlanCreate:
		return mutateVlanCreate(f, o, changesetID, nl)
	case VlanUpdate:
		return mutateVlanUpdate(f, o, nl)
	case VlanDelete:
		return mutateVlanDelete(f, o, nl)
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
