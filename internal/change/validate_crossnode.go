package change

import (
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// crossnodeValidate is validator class 4 (docs/features/change-management.md
// §2): the cross-node consistency class. Unlike every other class — which
// evaluates a changeset's ops against the node(s) an op targets — this class
// folds the changeset's projected effect across the *whole* cluster snapshot
// and compares same-named entities across nodes: the same shape of
// comparison internal/drift already runs against *live* state, run here
// against *what the state would become*. It runs after safety and before
// advisory (see ValidateWithSafety), and short-circuits the advisory class on
// any error exactly like the other classes.
//
// The three comparison families are shared verbatim with internal/drift
// through internal/xnode (BridgeDivergences / CrossNodeMTU /
// SDNRealizationGaps) — the whole point of factoring xnode out was to keep
// this class and drift's CheckBridgeDivergence/CheckMTUConsistency/
// CheckSDNRealization one implementation, never "the same problem under two
// names". This class supplies its own severity (all three block apply) and,
// for the fixable families, its own change.Op fix patch (built by the shared
// CrossNodeFixOps, which drift also calls).
//
// Scope: the class only reports a divergence whose subject bridge this
// changeset actually touches (a bridge.create/update/delete or
// bridge.port.add/remove op). A same-named bridge or SDN zone the changeset
// leaves entirely alone is not this changeset's responsibility — its
// pre-existing divergence is the async drift checker's job, and reporting it
// here would make an unrelated lockstep changeset fail on drift it neither
// introduced nor could correct (T-801 acceptance criterion 5). Conversely, a
// plain bridge.delete that breaks an *untouched* SDN zone's realization *is*
// caught, because that zone's realizing bridge is one the changeset touched
// (the very gap sdnValidate leaves — T-801 acceptance criterion 3).
func crossnodeValidate(ops []Op, snap inventory.Snapshot) []Finding {
	touched := touchedBridgeNames(ops)
	if len(touched) == 0 {
		// No bridge op: every trigger this class reasons about (bridge
		// presence/props, SDN realization affected by a bridge op) is absent,
		// so there is nothing for it to check. A pure sdn.* changeset stays
		// T-402's sdnValidate's job, not double-checked here.
		return nil
	}
	src := projectCrossnode(ops, snap)

	var out []Finding
	for _, d := range xnode.BridgeDivergences(src) {
		if !touched[d.Subject] {
			continue
		}
		out = append(out, crossnodeFinding(codeCrossnodeBridge, d))
	}
	for _, d := range xnode.CrossNodeMTU(src) {
		if !touched[d.Subject] {
			continue
		}
		out = append(out, crossnodeFinding(codeCrossnodeMTU, d))
	}
	for _, d := range xnode.SDNRealizationGaps(src) {
		if !touched[d.Subject] {
			continue
		}
		out = append(out, crossnodeFinding(codeCrossnodeSDN, d))
	}
	return out
}

// touchedBridgeNames is the set of bridge names this changeset's ops act on
// directly — the subjects this class is responsible for. A bridge.delete
// still counts (its name is the subject of any realization/presence gap the
// deletion opens), which is exactly how a bare bridge.delete reaches the SDN
// realization check.
func touchedBridgeNames(ops []Op) map[string]bool {
	out := map[string]bool{}
	for _, op := range ops {
		switch op.Type {
		case OpBridgeCreate, OpBridgeUpdate, OpBridgeDelete, OpBridgePortAdd, OpBridgePortRemove:
			out[op.Target.ID] = true
		}
	}
	return out
}

// crossnodeFinding builds a blocking Finding from a shared xnode.Divergence,
// attaching the shared-builder fix patch when the divergence is fixable.
// Message is set directly (not via errorf) so a detail string that happens to
// contain a percent sign is never treated as a format directive.
func crossnodeFinding(code string, d xnode.Divergence) Finding {
	ref := ""
	if len(d.Refs) > 0 {
		ref = d.Refs[0]
	}
	f := Finding{Severity: SeverityError, Code: code, Message: d.Detail, Ref: ref}
	if len(d.Fixes) > 0 {
		f.Fix = CrossNodeFixOps(d.Fixes)
	}
	return f
}

// CrossNodeFixOps turns a shared xnode BridgeFix list into the []Op patch a
// caller offers as a one-click fix — one bridge.update per dissenting node,
// each aligning exactly one field to the cluster majority. Exported because
// internal/drift (which cannot re-derive change.Op inside xnode without a
// cycle) builds its fixable drift findings' patches through this same
// function, so both the comparison (xnode) and the op construction (here) are
// single implementations shared by drift and this class.
func CrossNodeFixOps(fixes []xnode.BridgeFix) []Op {
	if len(fixes) == 0 {
		return nil
	}
	ops := make([]Op, 0, len(fixes))
	for _, fx := range fixes {
		params := &BridgeUpdateParams{}
		switch {
		case fx.VlanAware != nil:
			v := *fx.VlanAware
			params.VlanAware = &v
		case fx.MTU != nil:
			m := *fx.MTU
			params.MTU = &m
		case fx.Vids != nil:
			vv := toChangeVids(*fx.Vids)
			params.Vids = &vv
		}
		ops = append(ops, Op{Type: OpBridgeUpdate, Target: fx.Target, Params: params})
	}
	return ops
}

// projectedGraph is the per-node projected cluster snapshot this class
// compares across: a base inventory.Snapshot's bridges, cluster nodes, and
// SDN zones, cloned into a mutable index and then folded forward through the
// changeset's own ops (the same "fold every op's net effect" the other
// classes' projection does — see validate_projection.go — but tracking the
// bridge/zone *properties* the cross-node comparisons read, which the
// referential projection has no need for). It satisfies xnode.Source.
type projectedGraph struct {
	entities map[inventory.Ref]inventory.Entity
}

func (g *projectedGraph) All() []inventory.Entity {
	out := make([]inventory.Entity, 0, len(g.entities))
	for _, e := range g.entities {
		out = append(out, e)
	}
	return out
}

func (g *projectedGraph) Get(ref inventory.Ref) (inventory.Entity, bool) {
	e, ok := g.entities[ref]
	return e, ok
}

// projectCrossnode seeds a projectedGraph from snap (bridges, nodes, zones —
// the only kinds the cross-node comparisons read) and folds ops in order.
func projectCrossnode(ops []Op, snap inventory.Snapshot) *projectedGraph {
	g := &projectedGraph{entities: map[inventory.Ref]inventory.Entity{}}
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Node:
			g.entities[v.GetRef()] = &inventory.Node{Ref: v.Ref, Name: v.Name}
		case *inventory.Bridge:
			g.entities[v.GetRef()] = &inventory.Bridge{
				Ref: v.Ref, Name: v.Name,
				VlanAware: v.VlanAware, VlanAwareSet: v.VlanAwareSet,
				Vids: append([]inventory.VidRange(nil), v.Vids...),
				MTU:  v.MTU, MTUDeclared: v.MTUDeclared,
			}
		case *inventory.SdnZone:
			g.entities[v.GetRef()] = &inventory.SdnZone{
				Ref: v.Ref, ID: v.ID, Type: v.Type, Bridge: v.Bridge,
				Nodes: append([]string(nil), v.Nodes...),
			}
		}
	}
	for _, op := range ops {
		g.fold(op)
	}
	return g
}

func (g *projectedGraph) fold(op Op) {
	switch p := op.Params.(type) {
	case *BridgeCreateParams:
		g.entities[op.Target] = &inventory.Bridge{
			Ref: op.Target, Name: op.Target.ID,
			VlanAware: p.VlanAware, VlanAwareSet: true,
			Vids: toInvVids(p.Vids),
			MTU:  p.MTU, MTUDeclared: p.MTU,
		}
	case *BridgeUpdateParams:
		br := g.bridge(op.Target)
		if br == nil {
			// The referential class errors (and short-circuits before this
			// class runs) on an update to a nonexistent bridge; guard anyway.
			return
		}
		if p.VlanAware != nil {
			br.VlanAware = *p.VlanAware
			br.VlanAwareSet = true
		}
		if p.Vids != nil {
			br.Vids = toInvVids(*p.Vids)
		}
		if p.MTU != nil {
			br.MTU = *p.MTU
			br.MTUDeclared = *p.MTU
		}
	case *BridgeDeleteParams:
		delete(g.entities, op.Target)
	case *SdnZoneCreateParams:
		g.entities[op.Target] = &inventory.SdnZone{
			Ref: op.Target, ID: op.Target.ID, Type: p.Type, Bridge: p.Bridge,
			Nodes: append([]string(nil), p.Nodes...),
		}
	case *SdnZoneUpdateParams:
		z := g.zone(op.Target)
		if z == nil {
			return
		}
		if p.Bridge != nil {
			z.Bridge = *p.Bridge
		}
		if p.Nodes != nil {
			z.Nodes = append([]string(nil), *p.Nodes...)
		}
	case *SdnZoneDeleteParams:
		delete(g.entities, op.Target)
	}
}

func (g *projectedGraph) bridge(ref inventory.Ref) *inventory.Bridge {
	if e, ok := g.entities[ref]; ok {
		if b, ok := e.(*inventory.Bridge); ok {
			return b
		}
	}
	return nil
}

func (g *projectedGraph) zone(ref inventory.Ref) *inventory.SdnZone {
	if e, ok := g.entities[ref]; ok {
		if z, ok := e.(*inventory.SdnZone); ok {
			return z
		}
	}
	return nil
}

// toInvVids converts this package's wire VidRange to internal/inventory's
// in-memory VidRange (the inverse of the existing toChangeVids in
// validate_raw.go), for folding a bridge op's declared VID set into the
// projected inventory graph the cross-node comparison reads.
func toInvVids(vids []VidRange) []inventory.VidRange {
	if len(vids) == 0 {
		return nil
	}
	out := make([]inventory.VidRange, len(vids))
	for i, v := range vids {
		out[i] = inventory.VidRange{Low: v.Low, High: v.High}
	}
	return out
}
