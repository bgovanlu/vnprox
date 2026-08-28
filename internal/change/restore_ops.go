// SPDX-License-Identifier: Apache-2.0

package change

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ErrRestoreUnsupported is returned by restoreOpsForNode when a node's
// target (snapshot) and live interfaces(5) files differ in a way the typed
// op vocabulary cannot express — today, only re-creating an OVS bond whose
// snapshot carries no ovs_bridge attachment. Every other difference
// (bridge/bond/vlan create, delete, or field update; physical NIC MTU
// updates) is synthesized as one or more ops.
//
// Until T-3105 this covered *every* OVS bond, because inventory.Bond did
// not model the attachment at all. It now does, so an OVS bond snapshotted
// by a current build restores; the refusal survives for snapshots taken
// before that field existed, which is a real case and not a hypothetical —
// snapshots outlive the code that wrote them.
type ErrRestoreUnsupported struct {
	Node   string
	Kind   inventory.Kind
	ID     string
	Reason string
}

func (e *ErrRestoreUnsupported) Error() string {
	return fmt.Sprintf("change: cannot synthesize a restore op for %s %s:%s: %s", e.Kind, e.Node, e.ID, e.Reason)
}

// restoreOpsForNode computes the ops that would turn liveContent into
// targetContent on node, by structurally diffing the two interfaces(5)
// files' entities (bridges, bonds, VLAN sub-interfaces, physical NICs) and
// emitting the create/update/delete op each difference needs — the same
// typed op vocabulary every other draft is made of, so a restore is
// reviewed/validated/applied exactly like any other change
// (docs/features/change-management.md §4: "restores go through the same
// review flow"; docs/user-guide.md §3: "restores go through the same
// review flow").
//
// This is what completes the T-205 apply engine's committed-changeset-
// rollback synthesis for delete/update ops (T-205's *ErrInverseUnsupported):
// rather than inverting each op in isolation — which cannot recover a
// deleted/updated entity's prior field values from the op alone — this
// diffs the entity state a snapshot already recorded (byte-exact) against
// the current live file, which naturally reconstructs the right ops no
// matter what combination of ops produced the difference.
//
// Known, documented simplifications (accepted since the user always reviews
// the resulting draft's diff/plan before applying, per §3/§4 above, so any
// gap here is visible before it matters):
//   - Autostart is not modeled by internal/inventory's entities, so a
//     recreated bridge/bond/VLAN always sets autostart=true.
//   - Bond/VLAN per-stanza comments and a bond's MII-monitor interval are not
//     modeled either, so a recreated bond/VLAN never restores those.
//   - Clearing a field (e.g. a comment or gateway back to "") on an existing
//     entity produces an update op that requests the clear, but the shared
//     change->ifaces adapter (internal/change/apply_exec.go's
//     changeOpsToIfaces) has a pre-existing, already-documented gap where a
//     pointer-to-zero-value does not reach the file mutator as a "remove"
//     (see the T-205 report) — this is inherited, not introduced, and (like
//     the two points above) is visible in the diff review before apply.
func restoreOpsForNode(node, targetContent, liveContent string) ([]Op, error) {
	if targetContent == liveContent {
		return nil, nil
	}
	target, err := parseNodeEntities(node, targetContent)
	if err != nil {
		return nil, fmt.Errorf("change: parsing restore target content for node %s: %w", node, err)
	}
	live, err := parseNodeEntities(node, liveContent)
	if err != nil {
		return nil, fmt.Errorf("change: parsing live content for node %s: %w", node, err)
	}

	var order []string
	seen := map[string]bool{}
	for _, id := range target.order {
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	for _, id := range live.order {
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}

	var ops []Op
	for _, id := range order {
		t, tok := target.byID[id]
		l, lok := live.byID[id]
		switch {
		case tok && !lok:
			op, err := createOpFor(t)
			if err != nil {
				return nil, err
			}
			if op != nil {
				ops = append(ops, *op)
			}
		case !tok && lok:
			if op := deleteOpFor(l); op != nil {
				ops = append(ops, *op)
			}
		case tok && lok:
			if t.GetRef().Kind != l.GetRef().Kind {
				// The name was reused for a different kind of interface
				// between the two states: delete the live one, then
				// recreate the target one, rather than attempting an
				// update across incompatible types.
				if dop := deleteOpFor(l); dop != nil {
					ops = append(ops, *dop)
				}
				cop, err := createOpFor(t)
				if err != nil {
					return nil, err
				}
				if cop != nil {
					ops = append(ops, *cop)
				}
				continue
			}
			nodeOps, err := updateOpsFor(l, t)
			if err != nil {
				return nil, err
			}
			ops = append(ops, nodeOps...)
		}
	}
	return ops, nil
}

// nodeEntities indexes one node's parsed interfaces(5) entities by name
// (interface names are unique within one file), preserving file order.
type nodeEntities struct {
	byID  map[string]inventory.Entity
	order []string
}

func parseNodeEntities(node, content string) (nodeEntities, error) {
	f, err := host.ParseInterfaces([]byte(content))
	if err != nil {
		return nodeEntities{}, err
	}
	ents := inventory.FromInterfaces(node, f)
	ne := nodeEntities{byID: make(map[string]inventory.Entity, len(ents))}
	for _, e := range ents {
		id := e.GetRef().ID
		ne.byID[id] = e
		ne.order = append(ne.order, id)
	}
	return ne, nil
}

// createOpFor synthesizes the op that (re)creates entity e, which exists in
// the target state but not the live one. Physical NICs have no create op
// (they are hardware, docs/features/change-management.md §5: "Renaming NICs
// is out of scope") — nil, nil means "nothing to do".
func createOpFor(e inventory.Entity) (*Op, error) {
	ref := e.GetRef()
	switch v := e.(type) {
	case *inventory.Bridge:
		return &Op{Type: OpBridgeCreate, Target: ref, Params: &BridgeCreateParams{
			Ports:     append([]string(nil), v.DeclaredPortNames...),
			Vids:      toParamVids(v.Vids),
			Addresses: append([]string(nil), v.Addresses...),
			Gateway:   v.Gateway,
			Comments:  v.Comments,
			MTU:       v.MTUDeclared,
			VlanAware: v.VlanAware,
			STP:       v.STP,
		}}, nil
	case *inventory.Bond:
		// An OVS bond is a member of exactly one bridge and cannot be
		// rendered without that name (BondCreateParams.Bridge → ovs_bridge).
		// inventory.Bond carries it since T-3105; a snapshot that predates
		// that, or one taken from a stanza with no ovs_bridge line, still
		// cannot be restored — so the refusal narrows to the case that
		// actually lacks the attachment rather than to every OVS bond.
		if ref.Kind == inventory.KindOVSBond && v.OVSBridge == "" {
			return nil, &ErrRestoreUnsupported{Node: ref.Node, Kind: ref.Kind, ID: ref.ID,
				Reason: "OVS bond re-creation needs its ovs_bridge attachment, and this snapshot carries none " +
					"(snapshots taken before T-3105 did not record it)"}
		}
		return &Op{Type: OpBondCreate, Target: ref, Params: &BondCreateParams{
			Mode: v.Mode, LACPRate: v.LACPRate, XmitHashPolicy: v.XmitHashPolicy,
			Slaves: append([]string(nil), v.DeclaredSlaves...), MTU: v.MTUDeclared,
			Bridge: v.OVSBridge,
		}}, nil
	case *inventory.VlanIface:
		return &Op{Type: OpVlanCreate, Target: ref, Params: &VlanCreateParams{
			Parent: v.ParentName, Addresses: append([]string(nil), v.Addresses...), Vid: v.Vid, MTU: v.MTUDeclared,
		}}, nil
	default:
		// PhysNic (hardware) and anything else this model doesn't cover.
		return nil, nil
	}
}

// deleteOpFor synthesizes the op that removes entity e, which exists in the
// live state but not the target one. Physical NICs have no delete op —
// nil means "nothing to do" (the declaration is simply more detailed in the
// live file than the target; leaving it in place is harmless).
func deleteOpFor(e inventory.Entity) *Op {
	ref := e.GetRef()
	switch ref.Kind {
	case inventory.KindBridge, inventory.KindOVSBridge:
		return &Op{Type: OpBridgeDelete, Target: ref, Params: &BridgeDeleteParams{}}
	case inventory.KindBond, inventory.KindOVSBond:
		return &Op{Type: OpBondDelete, Target: ref, Params: &BondDeleteParams{}}
	case inventory.KindVlan:
		return &Op{Type: OpVlanDelete, Target: ref, Params: &VlanDeleteParams{}}
	default:
		return nil
	}
}

// updateOpsFor synthesizes the op(s) that turn live's declared fields into
// target's, for one entity present (by name and kind) in both states.
func updateOpsFor(live, target inventory.Entity) ([]Op, error) {
	ref := target.GetRef()
	switch t := target.(type) {
	case *inventory.Bridge:
		l, ok := live.(*inventory.Bridge)
		if !ok {
			return nil, fmt.Errorf("change: restore: live/target type mismatch for bridge %s", ref)
		}
		return bridgeUpdateOps(ref, l, t), nil
	case *inventory.Bond:
		l, ok := live.(*inventory.Bond)
		if !ok {
			return nil, fmt.Errorf("change: restore: live/target type mismatch for bond %s", ref)
		}
		return bondUpdateOps(ref, l, t), nil
	case *inventory.VlanIface:
		l, ok := live.(*inventory.VlanIface)
		if !ok {
			return nil, fmt.Errorf("change: restore: live/target type mismatch for vlan %s", ref)
		}
		return vlanUpdateOps(ref, l, t), nil
	case *inventory.PhysNic:
		l, ok := live.(*inventory.PhysNic)
		if !ok {
			return nil, fmt.Errorf("change: restore: live/target type mismatch for physnic %s", ref)
		}
		return physNicUpdateOps(ref, l, t), nil
	default:
		return nil, nil
	}
}

func toParamVids(vids []inventory.VidRange) []VidRange {
	if len(vids) == 0 {
		return nil
	}
	out := make([]VidRange, len(vids))
	for i, v := range vids {
		out[i] = VidRange{Low: v.Low, High: v.High}
	}
	return out
}

func vidsEqual(a, b []inventory.VidRange) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[inventory.VidRange]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func stringsEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func toSet(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

// bridgeUpdateOps diffs live against target and emits the bridge.port.add/
// remove ops port-membership changes need (bridge.update deliberately
// carries no Ports field — see BridgeUpdateParams' doc comment) plus a
// single bridge.update for every other changed field.
func bridgeUpdateOps(ref inventory.Ref, live, target *inventory.Bridge) []Op {
	var ops []Op

	liveSet, targetSet := toSet(live.DeclaredPortNames), toSet(target.DeclaredPortNames)
	for _, p := range live.DeclaredPortNames {
		if !targetSet[p] {
			ops = append(ops, Op{Type: OpBridgePortRemove, Target: ref, Params: &BridgePortRemoveParams{Port: p}})
		}
	}
	for _, p := range target.DeclaredPortNames {
		if !liveSet[p] {
			ops = append(ops, Op{Type: OpBridgePortAdd, Target: ref, Params: &BridgePortAddParams{Port: p}})
		}
	}

	u := &BridgeUpdateParams{}
	changed := false
	if live.VlanAware != target.VlanAware {
		b := target.VlanAware
		u.VlanAware = &b
		changed = true
	}
	if !vidsEqual(live.Vids, target.Vids) {
		v := toParamVids(target.Vids)
		u.Vids = &v
		changed = true
	}
	if !stringsEqualUnordered(live.Addresses, target.Addresses) {
		a := append([]string(nil), target.Addresses...)
		u.Addresses = &a
		changed = true
	}
	if live.Gateway != target.Gateway {
		g := target.Gateway
		u.Gateway = &g
		changed = true
	}
	if live.MTUDeclared != target.MTUDeclared {
		m := target.MTUDeclared
		u.MTU = &m
		changed = true
	}
	if live.STP != target.STP {
		s := target.STP
		u.STP = &s
		changed = true
	}
	if live.Comments != target.Comments {
		c := target.Comments
		u.Comments = &c
		changed = true
	}
	if changed {
		ops = append(ops, Op{Type: OpBridgeUpdate, Target: ref, Params: u})
	}
	return ops
}

func bondUpdateOps(ref inventory.Ref, live, target *inventory.Bond) []Op {
	u := &BondUpdateParams{}
	changed := false
	if live.Mode != target.Mode {
		m := target.Mode
		u.Mode = &m
		changed = true
	}
	if !stringsEqualUnordered(live.DeclaredSlaves, target.DeclaredSlaves) {
		s := append([]string(nil), target.DeclaredSlaves...)
		u.Slaves = &s
		changed = true
	}
	if live.LACPRate != target.LACPRate {
		r := target.LACPRate
		u.LACPRate = &r
		changed = true
	}
	if live.XmitHashPolicy != target.XmitHashPolicy {
		p := target.XmitHashPolicy
		u.XmitHashPolicy = &p
		changed = true
	}
	if live.MTUDeclared != target.MTUDeclared {
		m := target.MTUDeclared
		u.MTU = &m
		changed = true
	}
	if !changed {
		return nil
	}
	return []Op{{Type: OpBondUpdate, Target: ref, Params: u}}
}

func vlanUpdateOps(ref inventory.Ref, live, target *inventory.VlanIface) []Op {
	u := &VlanUpdateParams{}
	changed := false
	if !stringsEqualUnordered(live.Addresses, target.Addresses) {
		a := append([]string(nil), target.Addresses...)
		u.Addresses = &a
		changed = true
	}
	if live.MTUDeclared != target.MTUDeclared {
		m := target.MTUDeclared
		u.MTU = &m
		changed = true
	}
	if !changed {
		return nil
	}
	return []Op{{Type: OpVlanUpdate, Target: ref, Params: u}}
}

func physNicUpdateOps(ref inventory.Ref, live, target *inventory.PhysNic) []Op {
	if live.MTUDeclared == target.MTUDeclared {
		return nil
	}
	m := target.MTUDeclared
	return []Op{{Type: OpIfaceUpdate, Target: ref, Params: &IfaceUpdateParams{MTU: &m}}}
}
