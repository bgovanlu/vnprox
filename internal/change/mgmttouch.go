// mgmttouch.go implements T-703's server-computed `touchesMgmtPath` flag
// (docs/api.md's changesets section): whether a changeset's ops intersect
// any node's resolved management path — T-702's carrier refs plus every
// entity in their physical paths (internal/topology.ResolveMgmtPaths). The
// flag is *display/ceremony* metadata, not an interlock: T-203's safety
// class (validate_safety.go) remains the enforcement backstop, and this
// computation deliberately over-approximates (an op merely *naming* a
// path member counts) because its only consumers are the review screen's
// mandatory-acknowledgement block and the apply handler's confirm-window
// floor — a false positive costs one extra confirmation, a false negative
// would skip the ceremony on a genuinely dangerous edit.

package change

import (
	"context"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// MgmtConfirmTimeoutFloor is the minimum (and default) commit-confirm
// window for a changeset whose ops touch a management path (T-703: "confirm
// window defaulted to 180s and not reducible below the default for such
// changesets"). Deliberately above DefaultConfirmTimeout: a management-path
// edit that severs connectivity needs more slack for the operator to
// notice, re-reach the UI (possibly via another node), and confirm, than an
// ordinary bridge edit does. Enforced at the API layer
// (internal/api.handleApplyChangeset), which rejects a lower explicit
// confirmTimeoutSec with 400 confirm_window_too_short.
const MgmtConfirmTimeoutFloor = 180 * time.Second

// TouchesMgmtPath reports whether any op in ops touches any node's resolved
// management path in paths (MgmtStatus.Nodes — both the carrier refs
// themselves and every entity in their resolved physical paths). "Touches"
// means the op targets such an entity, or names one by its
// interfaces(5)-namespace name (a bond's slaves, a bridge's ports, a VLAN
// sub-interface's parent), or wholesale-replaces the interfaces file of a
// node that has a management path at all (iface.raw.replace — the file
// carries the path by definition, and diffing the raw content is the
// validators' job, not this flag's).
//
// Pure: callers (internal/api) compute paths once per request via
// Service.MgmtStatus and reuse it across a whole changeset list.
func TouchesMgmtPath(paths map[string][]topology.MgmtPath, ops []Op) bool {
	if len(paths) == 0 || len(ops) == 0 {
		return false
	}

	// names: (node, iface name) of every carrier and path member.
	names := map[ifaceKey]bool{}
	for node, list := range paths {
		for _, p := range list {
			names[ifaceKey{node, p.Ref.ID}] = true
			for _, ref := range p.Path {
				names[ifaceKey{node, ref.ID}] = true
			}
		}
	}

	for _, op := range ops {
		node := op.Target.Node
		touched := func(name string) bool { return name != "" && names[ifaceKey{node, name}] }

		switch params := op.Params.(type) {
		case *IfaceRawReplaceParams:
			// Target is a node ref: the op rewrites that node's whole file.
			if len(paths[op.Target.ID]) > 0 {
				return true
			}
		case *BondCreateParams:
			if touched(op.Target.ID) {
				return true
			}
			for _, s := range params.Slaves {
				if touched(s) {
					return true
				}
			}
		case *BondUpdateParams:
			if touched(op.Target.ID) {
				return true
			}
			if params.Slaves != nil {
				for _, s := range *params.Slaves {
					if touched(s) {
						return true
					}
				}
			}
		case *BridgeCreateParams:
			if touched(op.Target.ID) {
				return true
			}
			for _, p := range params.Ports {
				if touched(p) {
					return true
				}
			}
		case *BridgePortAddParams:
			if touched(op.Target.ID) || touched(params.Port) {
				return true
			}
		case *BridgePortRemoveParams:
			if touched(op.Target.ID) || touched(params.Port) {
				return true
			}
		case *VlanCreateParams:
			if touched(op.Target.ID) || touched(params.Parent) {
				return true
			}
		case *QosShapeCreateParams:
			// T-1505: a qos.shape.* op whose Bridge param names a node's
			// resolved management/corosync path is touchesMgmtPath — a tc/
			// HTB shape on the management/uplink bridge can starve or
			// deprioritize management/corosync traffic exactly as
			// disruptively as an iface.update, so it inherits T-703's
			// ceremony with no override, mirroring BridgePortAddParams'
			// dual (target OR named-param) check above. A create names its
			// own bridge in its params.
			if touched(params.Bridge) {
				return true
			}
		case *QosShapeUpdateParams:
			// An update that (re)sets Bridge names it directly; an update
			// that leaves Bridge unset (rate/ceil/priority only) still
			// mutates an existing shape whose bridge this package cannot
			// resolve from the op alone (the target id carries no bridge
			// name — see QosShapeDeleteParams below for the identical,
			// currently-unresolved gap on delete). Flagged, not silently
			// assumed safe: a future pass can close it with a
			// tunnelID->carrier-style lookup (mirrors T-1401's
			// WgCarrierSource) once a QoS read seam exists for it.
			if params.Bridge != nil && touched(*params.Bridge) {
				return true
			}
		case *QosShapeDeleteParams:
			// No params at all: this op names only the shape's own id, and
			// this package has no store-backed id->bridge lookup to
			// resolve it from here (unlike, say, T-1401's WgCarrierSource
			// pattern for a carrier-less wg op). A delete on a mgmt-path
			// shape therefore does not currently set touchesMgmtPath — a
			// documented, flagged gap (mirrors the identical limitation a
			// delete-by-id op with no named iface/bridge param always has
			// in this function without such a lookup), left for a
			// follow-up that threads a QoS read seam through the same way
			// WgCarrierSource does for WireGuard.
		default:
			// Every remaining iface-namespace op (iface.update,
			// bond/bridge/vlan update+delete) touches exactly its target;
			// cluster-scoped ops (sdn.*, fw.*, ipam.*, guest.nic.update)
			// never touch a node's interfaces file at all, and their
			// targets' kinds can never collide with an iface-namespace name
			// set entry because the name set is keyed by (node, name) and
			// cluster-scoped refs have an empty node. qos.shape.* targets
			// (KindQosShape, handled by the explicit cases above) are
			// node-scoped but likewise never collide with that name set.
			if isIfaceNamespaceKind(op.Target.Kind) && touched(op.Target.ID) {
				return true
			}
		}
	}
	return false
}

// isIfaceNamespaceKind reports whether kind lives in a node's flat
// interfaces(5) namespace (the kinds a management path can contain).
func isIfaceNamespaceKind(kind inventory.Kind) bool {
	switch kind {
	case inventory.KindPhysNic, inventory.KindBond, inventory.KindOVSBond,
		inventory.KindBridge, inventory.KindOVSBridge, inventory.KindVlan:
		return true
	default:
		return false
	}
}

// RecordMgmtAck appends the audit entry for a user's typed management-path
// acknowledgement (T-703's apply-side ceremony: the review screen's
// mandatory acknowledgement block for a touchesMgmtPath changeset —
// docs/api.md's changesets section). node is the node name the user typed;
// the API layer calls this right before Apply so the audit trail reads
// "acknowledged, then applied".
func (s *Service) RecordMgmtAck(ctx context.Context, id, author, node string) {
	s.appendAudit(ctx, author, "changeset.mgmt_ack", "success", id, map[string]any{"node": node})
}
