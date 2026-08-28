// SPDX-License-Identifier: Apache-2.0

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
	case IfaceRename:
		return mutateIfaceRename(f, o, nl)
	case IfaceRawReplace:
		return mutateIfaceRawReplace(f, o)
	case NatMasqueradeCreate:
		return mutateNatMasqueradeCreate(f, o, nl)
	case NatMasqueradeDelete:
		return mutateNatMasqueradeDelete(f, o)
	case NatPortForwardCreate:
		return mutateNatPortForwardCreate(f, o, nl)
	case NatPortForwardUpdate:
		return mutateNatPortForwardUpdate(f, o, nl)
	case NatPortForwardDelete:
		return mutateNatPortForwardDelete(f, o)
	case RouteStaticCreate:
		return mutateRouteStaticCreate(f, o, nl)
	case RouteStaticUpdate:
		return mutateRouteStaticUpdate(f, o, nl)
	case RouteStaticDelete:
		return mutateRouteStaticDelete(f, o)
	case VFProvision:
		return mutateVFProvision(f, o, nl)
	default:
		return fmt.Errorf("ifaces: mutate: unsupported op %T", op)
	}
}

// mutateIfaceRawReplace re-parses o.Content into a fresh AST and replaces
// f's Entries wholesale — the one op in this package that does not edit f
// in place. Render()ing f afterward reproduces o.Content byte-for-byte
// (host.File's lossless-render guarantee for an unmutated parse), so this
// is what makes DiffChangeset's "before vs. after Render()" comparison
// yield a correct full-file diff, and what makes computeStagedFile's
// generic parse->MutateAll->Render pipeline (internal/change/apply_exec.go)
// stage exactly o.Content with no special-casing needed there.
func mutateIfaceRawReplace(f *host.File, o IfaceRawReplace) error {
	nf, err := host.ParseInterfaces([]byte(o.Content))
	if err != nil {
		return fmt.Errorf("ifaces: iface.raw.replace: parsing replacement content: %w", err)
	}
	f.Entries = nf.Entries
	return nil
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
