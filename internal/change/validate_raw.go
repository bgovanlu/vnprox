package change

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// rawReplaceExclusiveFindings flags any iface.raw.replace op sharing a
// changeset with another node-file op targeting the *same* node
// (docs/features/change-management.md §7: the raw editor's save is meant
// to be "a changeset whose single op is iface.raw.replace" — mixing it with
// an AST-level edit of the same node's file in one changeset is ambiguous:
// the raw op discards the whole tree, so ordering between the two would
// silently determine which one "wins", and a stale AST-edit reference
// (e.g. bridge.port.add naming a port the raw content just removed) could
// pass referential checks against a live snapshot that hasn't moved yet).
// A raw.replace alongside ops on a *different* node is unaffected — those
// are independent per-node file-stage steps in BuildPlan/apply_exec.go.
//
// This must run against ops as the caller received them (before
// Service.expandRawReplaceOps injects a raw op's own synthesized delta,
// which also targets that same node and would otherwise trip this same
// check against itself) — see schemaValidate's doc comment for why this
// isn't just another schemaValidateOp case.
func rawReplaceExclusiveFindings(ops []Op) []Finding {
	nodeOpCount := map[string]int{}
	for _, op := range ops {
		if nodeFileOpTypes[op.Type] {
			nodeOpCount[op.Target.Node]++
		}
	}
	var out []Finding
	for _, op := range ops {
		if op.Type == OpIfaceRawReplace && nodeOpCount[op.Target.Node] > 1 {
			out = append(out, errorf(codeRawReplaceNotExclusive, refOf(op),
				"iface.raw.replace must be the only node-file op targeting node %s in this changeset", op.Target.Node))
		}
	}
	return out
}

// expandRawReplace turns one iface.raw.replace op into the equivalent set
// of standard-vocabulary bond/bridge/vlan/iface ops it would take to reach
// the same end state, by diffing the node's declared entities before
// (before, the live file content) and after (the op's new Content) — both
// run through the exact same internal/inventory.FromInterfaces adapter, so
// the comparison is apples-to-apples (declared-only fields on both sides,
// no runtime-only netlink/PVE-API noise). This is what "validators run
// against the parsed result — reuse T-202 pipeline on the parsed entity
// delta" (this task's card) means concretely: the synthesized ops feed
// straight into referentialValidate/safetyValidate/advisoryValidate
// (validate.go), so e.g. a raw edit that deletes the management bridge
// produces the same synthesized bridge.delete op — and the same
// safety.protected_interface/safety.guest_bearing_bridge findings — as if
// the user had used the bridge delete dialog (AC2).
//
// It never mutates anything and performs no I/O: before/after are plain
// strings, callers (Service.expandRawReplaceOps) own reading the live file.
// A malformed after returns a single raw.parse_error Finding (with the
// parser's line number) and no ops; a malformed before (should not happen
// for a live file, but defensively handled) is treated as an empty prior
// state rather than failing the whole expansion.
func expandRawReplace(target inventory.Ref, before, after string) ([]Op, []Finding) {
	node := target.Node

	afterFile, err := host.ParseInterfaces([]byte(after))
	if err != nil {
		return nil, []Finding{rawParseErrorFinding(target, err)}
	}

	var beforeFile *host.File
	if bf, ferr := host.ParseInterfaces([]byte(before)); ferr == nil {
		beforeFile = bf
	}

	oldByRef := entitiesByRef(inventory.FromInterfaces(node, beforeFile))
	newByRef := entitiesByRef(inventory.FromInterfaces(node, afterFile))

	var ops []Op
	for ref := range oldByRef {
		if _, stillExists := newByRef[ref]; stillExists {
			continue
		}
		if op, ok := rawDeleteOpFor(ref); ok {
			ops = append(ops, op)
		}
	}
	for ref, newEnt := range newByRef {
		oldEnt, existed := oldByRef[ref]
		if !existed {
			if op, ok := rawCreateOpFor(ref, newEnt); ok {
				ops = append(ops, op)
			}
			continue
		}
		ops = append(ops, rawUpdateOpsFor(ref, oldEnt, newEnt)...)
	}

	// Deterministic order: map iteration above is not, so sort by
	// (target string, op type) before returning — the caller only uses
	// this for validation (order doesn't change the validation classes'
	// per-op findings), but a stable order keeps this function's output
	// (and any test asserting on it) reproducible.
	sort.Slice(ops, func(i, j int) bool {
		ti, tj := ops[i].Target.String(), ops[j].Target.String()
		if ti != tj {
			return ti < tj
		}
		return ops[i].Type < ops[j].Type
	})
	return ops, nil
}

// rawParseErrorFinding reports after's syntax error as a blocking Finding,
// including the parser's 1-based line number when available.
func rawParseErrorFinding(target inventory.Ref, err error) Finding {
	msg := err.Error()
	if perr, ok := asHostParseError(err); ok {
		msg = fmt.Sprintf("line %d: %s", perr.Line, perr.Msg)
	}
	return errorf(codeRawReplaceParseError, target.String(), "%s", msg)
}

// entitiesByRef indexes ents by Ref for the create/update/delete diff below.
func entitiesByRef(ents []inventory.Entity) map[inventory.Ref]inventory.Entity {
	out := make(map[inventory.Ref]inventory.Entity, len(ents))
	for _, e := range ents {
		out[e.GetRef()] = e
	}
	return out
}

// rawDeleteOpFor returns the delete op for an entity that disappeared from
// the new content, or ok=false if ref's kind has no delete op in the v1
// vocabulary (physnics: removing a stanza doesn't delete hardware).
func rawDeleteOpFor(ref inventory.Ref) (Op, bool) {
	switch ref.Kind {
	case inventory.KindBond, inventory.KindOVSBond:
		return Op{Type: OpBondDelete, Target: ref, Params: &BondDeleteParams{}}, true
	case inventory.KindBridge, inventory.KindOVSBridge:
		return Op{Type: OpBridgeDelete, Target: ref, Params: &BridgeDeleteParams{}}, true
	case inventory.KindVlan:
		return Op{Type: OpVlanDelete, Target: ref, Params: &VlanDeleteParams{}}, true
	default:
		return Op{}, false
	}
}

// rawCreateOpFor returns the create op for an entity newly present in the
// new content, or ok=false for kinds with no create op (physnics).
func rawCreateOpFor(ref inventory.Ref, ent inventory.Entity) (Op, bool) {
	switch v := ent.(type) {
	case *inventory.Bond:
		return Op{Type: OpBondCreate, Target: ref, Params: &BondCreateParams{
			Mode: v.Mode, LACPRate: v.LACPRate, XmitHashPolicy: v.XmitHashPolicy,
			Slaves: firstNonEmpty(v.Slaves, v.DeclaredSlaves), MTU: v.MTUDeclared,
		}}, true
	case *inventory.Bridge:
		return Op{Type: OpBridgeCreate, Target: ref, Params: &BridgeCreateParams{
			Gateway: v.Gateway, Comments: v.Comments,
			Ports: firstNonEmpty(v.PortNames, v.DeclaredPortNames), Vids: toChangeVids(v.Vids),
			Addresses: v.Addresses, MTU: v.MTUDeclared, VlanAware: v.VlanAware, STP: v.STP,
		}}, true
	case *inventory.VlanIface:
		return Op{Type: OpVlanCreate, Target: ref, Params: &VlanCreateParams{
			Parent: v.ParentName, Addresses: v.Addresses, Vid: v.Vid, MTU: v.MTUDeclared,
		}}, true
	default:
		return Op{}, false // physnic: no create op in the v1 vocabulary
	}
}

// rawUpdateOpsFor compares an entity present in both old and new content,
// returning update ops for whatever changed (nil if nothing did). Bridge
// port membership changes are split into bridge.port.add/remove ops
// (matching the documented convention that bridge.update never carries
// port membership — params_bridge.go's doc comment); everything else
// changed collapses into one *.update op.
func rawUpdateOpsFor(ref inventory.Ref, oldEnt, newEnt inventory.Entity) []Op {
	switch nv := newEnt.(type) {
	case *inventory.Bond:
		ov, ok := oldEnt.(*inventory.Bond)
		if !ok {
			return nil
		}
		return rawBondUpdateOps(ref, ov, nv)
	case *inventory.Bridge:
		ov, ok := oldEnt.(*inventory.Bridge)
		if !ok {
			return nil
		}
		return rawBridgeUpdateOps(ref, ov, nv)
	case *inventory.VlanIface:
		ov, ok := oldEnt.(*inventory.VlanIface)
		if !ok {
			return nil
		}
		return rawVlanUpdateOps(ref, ov, nv)
	case *inventory.PhysNic:
		ov, ok := oldEnt.(*inventory.PhysNic)
		if !ok {
			return nil
		}
		return rawPhysNicUpdateOps(ref, ov, nv)
	default:
		return nil
	}
}

func rawBondUpdateOps(ref inventory.Ref, ov, nv *inventory.Bond) []Op {
	p := &BondUpdateParams{}
	changed := false
	if nv.Mode != ov.Mode {
		m := nv.Mode
		p.Mode, changed = &m, true
	}
	newSlaves := firstNonEmpty(nv.Slaves, nv.DeclaredSlaves)
	oldSlaves := firstNonEmpty(ov.Slaves, ov.DeclaredSlaves)
	if !stringSlicesEqual(newSlaves, oldSlaves) {
		s := newSlaves
		p.Slaves, changed = &s, true
	}
	if nv.XmitHashPolicy != ov.XmitHashPolicy {
		h := nv.XmitHashPolicy
		p.XmitHashPolicy, changed = &h, true
	}
	if nv.MTUDeclared != ov.MTUDeclared {
		m := nv.MTUDeclared
		p.MTU, changed = &m, true
	}
	if !changed {
		return nil
	}
	return []Op{{Type: OpBondUpdate, Target: ref, Params: p}}
}

func rawBridgeUpdateOps(ref inventory.Ref, ov, nv *inventory.Bridge) []Op {
	var ops []Op
	newPorts := firstNonEmpty(nv.PortNames, nv.DeclaredPortNames)
	oldPorts := firstNonEmpty(ov.PortNames, ov.DeclaredPortNames)
	added, removed := diffStringSlices(oldPorts, newPorts)
	for _, port := range added {
		ops = append(ops, Op{Type: OpBridgePortAdd, Target: ref, Params: &BridgePortAddParams{Port: port}})
	}
	for _, port := range removed {
		ops = append(ops, Op{Type: OpBridgePortRemove, Target: ref, Params: &BridgePortRemoveParams{Port: port}})
	}

	p := &BridgeUpdateParams{}
	changed := false
	if nv.VlanAware != ov.VlanAware {
		b := nv.VlanAware
		p.VlanAware, changed = &b, true
	}
	if nv.STP != ov.STP {
		b := nv.STP
		p.STP, changed = &b, true
	}
	if !vidRangesEqual(nv.Vids, ov.Vids) {
		v := toChangeVids(nv.Vids)
		p.Vids, changed = &v, true
	}
	if !stringSlicesEqual(nv.Addresses, ov.Addresses) {
		a := nv.Addresses
		p.Addresses, changed = &a, true
	}
	if nv.Gateway != ov.Gateway {
		g := nv.Gateway
		p.Gateway, changed = &g, true
	}
	if nv.MTUDeclared != ov.MTUDeclared {
		m := nv.MTUDeclared
		p.MTU, changed = &m, true
	}
	if nv.Comments != ov.Comments {
		c := nv.Comments
		p.Comments, changed = &c, true
	}
	if changed {
		ops = append(ops, Op{Type: OpBridgeUpdate, Target: ref, Params: p})
	}
	return ops
}

func rawVlanUpdateOps(ref inventory.Ref, ov, nv *inventory.VlanIface) []Op {
	p := &VlanUpdateParams{}
	changed := false
	if !stringSlicesEqual(nv.Addresses, ov.Addresses) {
		a := nv.Addresses
		p.Addresses, changed = &a, true
	}
	if nv.MTUDeclared != ov.MTUDeclared {
		m := nv.MTUDeclared
		p.MTU, changed = &m, true
	}
	if !changed {
		return nil
	}
	return []Op{{Type: OpVlanUpdate, Target: ref, Params: p}}
}

func rawPhysNicUpdateOps(ref inventory.Ref, ov, nv *inventory.PhysNic) []Op {
	if nv.MTUDeclared == ov.MTUDeclared || nv.MTUDeclared == 0 {
		return nil
	}
	m := nv.MTUDeclared
	return []Op{{Type: OpIfaceUpdate, Target: ref, Params: &IfaceUpdateParams{MTU: &m}}}
}

// stringSlicesEqual reports whether a and b hold the same elements in the
// same order.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// diffStringSlices returns the elements of b not in a (added) and the
// elements of a not in b (removed), each in their source slice's order.
func diffStringSlices(a, b []string) (added, removed []string) {
	inA := make(map[string]bool, len(a))
	for _, s := range a {
		inA[s] = true
	}
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	for _, s := range b {
		if !inA[s] {
			added = append(added, s)
		}
	}
	for _, s := range a {
		if !inB[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// toChangeVids converts inventory.VidRange (the in-memory type
// internal/inventory.Bridge.Vids carries) to this package's own wire
// VidRange type (params_common.go) — the two are structurally identical
// but intentionally distinct Go types (see that file's doc comment).
func toChangeVids(vids []inventory.VidRange) []VidRange {
	if vids == nil {
		return nil
	}
	out := make([]VidRange, len(vids))
	for i, v := range vids {
		out[i] = VidRange{Low: v.Low, High: v.High}
	}
	return out
}

// vidRangesEqual reports whether a and b hold the same VidRange values in
// the same order (both sides come from parseVidRangeList against a
// consistent option-rendering convention, so order is meaningful/stable
// here, not something that needs set-comparison).
func vidRangesEqual(a, b []inventory.VidRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
