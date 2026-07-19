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
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// WgTunnelCarrier identifies an existing WireGuard tunnel's carrier interface
// and owning node. TouchesMgmtPath consumes a tunnelID->WgTunnelCarrier map so
// it can flag wg ops that reference a management-path tunnel WITHOUT the
// carrier appearing in the op params: a standalone wg.peer.add/remove, a
// carrier-less wg.tunnel.update (MTU/port only), or a wg.tunnel.delete on an
// already-existing tunnel. For those ops the carrier lives only in the store,
// so without this lookup they would fall through untouched and bypass T-703's
// no-override ceremony (the mgmt-path interlock).
type WgTunnelCarrier struct {
	Node    string
	Carrier string
}

// WgCarrierSource resolves the tunnelID->carrier map TouchesMgmtPath needs.
// The API layer builds it once per request from the WireGuard read service
// (cmd/vnproxd's wireGuardReadService), and Service.sealOpSecrets' sibling
// scheduling gate uses it via Config.WgCarriers so an unattended scheduled
// wg op on a mgmt-path tunnel is caught the same way.
type WgCarrierSource interface {
	TunnelCarriers(ctx context.Context) (map[string]WgTunnelCarrier, error)
}

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
// tunnelCarriers maps an existing WireGuard tunnel's id to its stored carrier
// (WgCarrierSource) so wg ops that don't name the carrier in their params
// (wg.peer.*, carrier-less wg.tunnel.update, wg.tunnel.delete) are still
// flagged when that carrier is on a node's management path. A nil/empty map
// degrades to params-only coverage (a create still names its own carrier).
//
// Pure: callers (internal/api) compute paths + tunnelCarriers once per request
// via Service.MgmtStatus / the WireGuard read service and reuse them across a
// whole changeset list.
func TouchesMgmtPath(paths map[string][]topology.MgmtPath, tunnelCarriers map[string]WgTunnelCarrier, ops []Op) bool {
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
		// storedCarrierTouched reports whether the tunnel identified by
		// tunnelID has a stored carrier on this op's node's management path —
		// the resolution wg.peer.*/delete/carrier-less-update ops rely on
		// since they carry no carrier of their own.
		storedCarrierTouched := func(tunnelID string) bool {
			c, ok := tunnelCarriers[tunnelID]
			return ok && c.Node == node && touched(c.Carrier)
		}

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
		case *WgTunnelCreateParams:
			// T-1401: a wg.* op on a tunnel whose carrier interface is itself
			// part of a node's resolved management/corosync path is
			// touchesMgmtPath — inheriting T-703's ceremony with no override.
			// A create names its own carrier (the tunnel doesn't exist yet).
			if touched(params.Carrier) {
				return true
			}
		case *WgTunnelUpdateParams:
			// A carrier-setting update names the new carrier; a carrier-less
			// update (MTU/port only) or one moving the carrier OFF the path
			// still edits a tunnel that may currently ride the mgmt path, so
			// its stored carrier is resolved too.
			if params.Carrier != nil && touched(*params.Carrier) {
				return true
			}
			if storedCarrierTouched(op.Target.ID) {
				return true
			}
		case *WgTunnelDeleteParams:
			// Delete carries no params: resolve the tunnel's stored carrier.
			// Tearing the interface + its routes down on a mgmt-path carrier
			// is exactly the ceremony-worthy case.
			if storedCarrierTouched(op.Target.ID) {
				return true
			}
		case *WgPeerAddParams, *WgPeerRemoveParams:
			// Peer ops target "<tunnelID>/<pubkey>" and carry no carrier; a
			// peer add/remove re-renders and re-syncs the tunnel's interface,
			// so it touches the mgmt path whenever the tunnel's stored carrier
			// is on it.
			if storedCarrierTouched(wgPeerTunnelID(op.Target.ID)) {
				return true
			}
		default:
			// Every remaining iface-namespace op (iface.update,
			// bond/bridge/vlan update+delete) touches exactly its target;
			// cluster-scoped ops (sdn.*, fw.*, ipam.*, guest.nic.update)
			// never touch a node's interfaces file at all, and their
			// targets' kinds can never collide with an iface-namespace name
			// set entry because the name set is keyed by (node, name) and
			// cluster-scoped refs have an empty node.
			if isIfaceNamespaceKind(op.Target.Kind) && touched(op.Target.ID) {
				return true
			}
		}
	}
	return false
}

// wgPeerTunnelID extracts the tunnel id from a wg-peer target id, which
// encodes "<tunnelID>/<peer public key>" (params_wg.go). A target with no "/"
// (a tunnel-scoped or malformed id) yields the whole string, which simply
// won't match any tunnelCarriers entry.
func wgPeerTunnelID(targetID string) string {
	if i := strings.IndexByte(targetID, '/'); i >= 0 {
		return targetID[:i]
	}
	return targetID
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
