// specdrift.go implements T-1102's sixth drift check family: live state vs.
// a pinned declarative spec (internal/spec, T-1101's "one versionable YAML
// document capturing cluster-wide network intent"). Unlike the five
// check-family files this package already has (bridge.go, mtu.go, sdn.go,
// pending.go, filerun.go), this check is not a pure func(inventory.Snapshot)
// []Finding: it needs the pinned spec's content, an external, mutable piece
// of app-owned state (docs/architecture.md §7) that GET/POST/DELETE
// /spec/pin (internal/api/specpin.go) reads and writes. It is therefore a
// Service method, combined into Findings' output alongside the five pure
// check functions, rather than a checkFuncs entry.
//
// The reconcile diff itself is NOT reimplemented here — it calls T-1101's
// frozen internal/spec.Import(Spec, inventory.Snapshot) ([]change.Op,
// []inventory.Ref, error) verbatim, the same "one shared implementation"
// discipline internal/xnode's cross-node comparisons already establish for
// the other five families (adapter.go's doc comment). One Finding is raised
// per distinct entity (change.Op.Target) Import says needs reconciling —
// AC2's "exactly one spec_drift finding naming the entity" — not one big
// finding for the whole spec, so a cluster with several diverging entities
// gets one finding each, exactly like every other check family.

package drift

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// PinProvider is the pinned-spec seam: the current pin's raw YAML content,
// if any. Deliberately context-free (unlike the store repo it typically
// wraps, whose Get takes a context) — Findings/RunLoop call this from a
// background cycle with no natural request context to thread through, the
// same shape cmd/vnproxd's mgmtStatusAdapter already establishes for
// findings.MgmtProvider (see that adapter's doc comment). cmd/vnproxd wires
// a small adapter over *store.PinnedSpecRepo that supplies context.Background()
// internally.
type PinProvider interface {
	// Pin returns the currently pinned spec's raw content and true, or
	// ("", false) if nothing is pinned.
	Pin() (content string, ok bool)
}

// specDriftFindings computes the CheckSpecDrift family: it asks pins for the
// current pin (nil pins or "nothing pinned" both mean zero findings — pinning
// is opt-in, docs/features/topology.md §6), parses it, diffs it against snap
// via spec.Import, and turns the resulting ops into one Finding per distinct
// target entity. A parse/diff error (should not happen in practice — POST
// /spec/pin validates the document before storing it) is logged and treated
// as zero findings rather than panicking the whole drift cycle.
func (s *Service) specDriftFindings(snap inventory.Snapshot) []Finding {
	parsed, ok := s.parsedSpec()
	if !ok {
		return nil
	}
	ops, _, err := spec.Import(parsed, snap)
	if err != nil {
		s.log.Error("drift: reconciling pinned spec against live state", "error", err)
		return nil
	}
	if len(ops) == 0 {
		return nil
	}
	return specDriftFindingsFromOps(ops)
}

// parsedSpec returns the current spec document — the third position both this
// file and reconcile.go (T-2703) diff against — parsed, or false when nothing
// is pinned, no provider is wired, or the document does not parse. A parse
// failure is logged and treated as "no spec" rather than panicking the drift
// cycle; POST /spec/pin validates a document before storing it, and T-2701's
// sync raises its own finding for an unparseable one.
func (s *Service) parsedSpec() (spec.Spec, bool) {
	if s.pins == nil {
		return spec.Spec{}, false
	}
	content, ok := s.pins.Pin()
	if !ok {
		return spec.Spec{}, false
	}
	parsed, err := spec.Parse([]byte(content))
	if err != nil {
		s.log.Error("drift: parsing pinned spec", "error", err)
		return spec.Spec{}, false
	}
	return parsed, true
}

// specDriftFindingsFromOps groups ops by their target Ref (spec.Import may
// emit more than one op for the same entity, e.g. a bridge.update alongside
// bridge.port.add/remove) into one Finding per entity, each carrying its own
// slice of ops as its computable fix.
func specDriftFindingsFromOps(ops []change.Op) []Finding {
	var order []inventory.Ref
	byRef := map[inventory.Ref][]change.Op{}
	for _, op := range ops {
		ref := op.Target
		if _, seen := byRef[ref]; !seen {
			order = append(order, ref)
		}
		byRef[ref] = append(byRef[ref], op)
	}

	out := make([]Finding, 0, len(order))
	for _, ref := range order {
		refOps := byRef[ref]
		detail := fmt.Sprintf("%s diverges from the pinned spec (%d op(s) needed to reconcile)", ref, len(refOps))
		f := newFinding(CheckSpecDrift, SeverityWarning, detail, []string{ref.Node}, []string{ref.String()})
		f = f.withFix("drift: reconcile "+ref.String()+" to pinned spec", refOps)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
