package ifaces

import (
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// OpType identifies one entry in docs/data-model.md §3's "iface", "bond",
// "bridge", and "vlan" op groups — the subset of the v1 op vocabulary that
// mutates a node's /etc/network/interfaces file (as opposed to sdn/fw/ipam
// ops, which touch other files, or guest ops, which go through the PVE API
// directly). The string values are the wire "op" field and must match
// docs/data-model.md §3 exactly, since other tasks (T-201's changeset
// store, T-207's UI) key off them.
type OpType string

const (
	OpIfaceUpdate      OpType = "iface.update"
	OpBondCreate       OpType = "bond.create"
	OpBondUpdate       OpType = "bond.update"
	OpBondDelete       OpType = "bond.delete"
	OpBridgeCreate     OpType = "bridge.create"
	OpBridgeUpdate     OpType = "bridge.update"
	OpBridgeDelete     OpType = "bridge.delete"
	OpBridgePortAdd    OpType = "bridge.port.add"
	OpBridgePortRemove OpType = "bridge.port.remove"
	OpVlanCreate       OpType = "vlan.create"
	OpVlanUpdate       OpType = "vlan.update"
	OpVlanDelete       OpType = "vlan.delete"
)

// Op is the common interface every concrete op type in this package
// implements. Target names the entity the op applies to using
// internal/inventory's Ref triplet — the same identity type
// docs/data-model.md documents for the wire "target" field and the type
// internal/inventory, internal/topology, and internal/api already share, so
// this package does not invent a second Ref shape.
//
// NOTE for the T-201 integrator: internal/change's Op tagged union (built
// concurrently in a separate worktree against the same docs/data-model.md
// §3 vocabulary) is expected to have its own envelope
// {op, target, params}. This package's DecodeOps/DecodeOp functions decode
// that exact wire shape into the concrete types below, so a
// internal/change.Op -> ifaces.Op adapter only needs to re-marshal
// (json.Marshal(change.Op); ifaces.DecodeOp(bytes)) or, better, construct
// the concrete ifaces type directly from already-decoded fields — see the
// per-type doc comments for field-by-field mapping. Do not change the
// OpType string constants above without updating docs/data-model.md; other
// tasks depend on them matching exactly.
type Op interface {
	// Kind returns the op's wire type string.
	Kind() OpType
	// Ref returns the entity the op targets.
	Ref() inventory.Ref
}

// --- iface.update ----------------------------------------------------------

// IfaceUpdate patches fields on any already-existing iface(5) stanza
// (physnic, bond, bridge, or vlan) without changing its kind. Pointer/slice
// fields left nil/zero are left untouched by Mutate — this is a partial
// patch, matching the "iface.update (mtu, comments, addresses, gateway,
// autostart)" field list in docs/data-model.md §3. RemoveAddresses/
// RemoveGateway let a caller explicitly clear a previously-set value (a nil
// Addresses/Gateway alone means "leave as-is", not "clear").
type IfaceUpdate struct {
	MTU           *int
	Comments      *string
	Gateway       *string
	Autostart     *bool
	Target        inventory.Ref
	Addresses     []string
	RemoveAddress bool
	RemoveGateway bool
}

func (o IfaceUpdate) Kind() OpType       { return OpIfaceUpdate }
func (o IfaceUpdate) Ref() inventory.Ref { return o.Target }

// --- bond.* ------------------------------------------------------------

// BondCreate stages a new bond iface stanza. Target.Kind selects Linux
// bonding (inventory.KindBond: bond-slaves/bond-mode/...) vs. OVS bonding
// (inventory.KindOVSBond: ovs_bonds/ovs_bridge/ovs_type=OVSBond/...); Bridge
// must name the OVS bridge the bond attaches to when OVS.
type BondCreate struct {
	Target         inventory.Ref
	Mode           string
	LacpRate       string
	XmitHashPolicy string
	Comments       string
	Bridge         string
	Slaves         []string
	MIIMon         int
	MTU            int
	Autostart      bool
}

func (o BondCreate) Kind() OpType       { return OpBondCreate }
func (o BondCreate) Ref() inventory.Ref { return o.Target }

// BondUpdate patches an existing bond's options in place. Zero-value fields
// are left untouched (Mode == "" means "don't change bond-mode", etc.);
// use the explicit Remove* flags to clear an option instead of changing it.
type BondUpdate struct {
	Comments             *string
	Target               inventory.Ref
	Mode                 string
	LacpRate             string
	XmitHashPolicy       string
	Slaves               []string
	MIIMon               int
	MTU                  int
	RemoveLacpRate       bool
	RemoveXmitHashPolicy bool
}

func (o BondUpdate) Kind() OpType       { return OpBondUpdate }
func (o BondUpdate) Ref() inventory.Ref { return o.Target }

// BondDelete removes a bond's iface stanza and its auto/allow-* reference.
type BondDelete struct{ Target inventory.Ref }

func (o BondDelete) Kind() OpType       { return OpBondDelete }
func (o BondDelete) Ref() inventory.Ref { return o.Target }

// --- bridge.* ----------------------------------------------------------

// BridgeCreate stages a new bridge iface stanza. Target.Kind selects Linux
// bridging (inventory.KindBridge: bridge-ports/bridge-vlan-aware/...) vs.
// OVS bridging (inventory.KindOVSBridge: ovs_type=OVSBridge/ovs_ports/...).
type BridgeCreate struct {
	Target    inventory.Ref
	Gateway   string
	Comments  string
	Ports     []string
	Vids      []inventory.VidRange
	Addresses []string
	MTU       int
	VlanAware bool
	STP       bool
	Autostart bool
}

func (o BridgeCreate) Kind() OpType       { return OpBridgeCreate }
func (o BridgeCreate) Ref() inventory.Ref { return o.Target }

// BridgeUpdate patches an existing bridge's options in place. Zero-value
// fields are left untouched; VlanAware/STP are pointers so "explicitly set
// to false" is distinguishable from "don't touch".
type BridgeUpdate struct {
	VlanAware     *bool
	STP           *bool
	Gateway       *string
	Comments      *string
	Target        inventory.Ref
	Ports         []string
	Vids          []inventory.VidRange
	Addresses     []string
	MTU           int
	RemoveVids    bool
	RemoveGateway bool
}

func (o BridgeUpdate) Kind() OpType       { return OpBridgeUpdate }
func (o BridgeUpdate) Ref() inventory.Ref { return o.Target }

// BridgeDelete removes a bridge's iface stanza and its auto/allow-*
// reference. Reattaching guests attached to it is a separate guest.nic.update
// op the planner/UI is responsible for including in the same changeset
// (docs/features/change-management.md §5); this op only touches the file.
type BridgeDelete struct{ Target inventory.Ref }

func (o BridgeDelete) Kind() OpType       { return OpBridgeDelete }
func (o BridgeDelete) Ref() inventory.Ref { return o.Target }

// BridgePortAdd appends Port to Target's port list (bridge-ports for Linux,
// ovs_ports for OVS, detected from the existing stanza's options).
type BridgePortAdd struct {
	Target inventory.Ref
	Port   string
}

func (o BridgePortAdd) Kind() OpType       { return OpBridgePortAdd }
func (o BridgePortAdd) Ref() inventory.Ref { return o.Target }

// BridgePortRemove removes Port from Target's port list.
type BridgePortRemove struct {
	Target inventory.Ref
	Port   string
}

func (o BridgePortRemove) Kind() OpType       { return OpBridgePortRemove }
func (o BridgePortRemove) Ref() inventory.Ref { return o.Target }

// --- vlan.* ------------------------------------------------------------

// VlanCreate stages a new VLAN sub-interface. Name is derived as
// "<Parent>.<VID>" per the Debian vlan-raw-device naming convention (as
// seen throughout T-102's testdata corpus, e.g. "bond0.10", "vmbr0.20") —
// Target.ID is expected to already be in that form; VlanName can be used to
// compute it before constructing the op.
type VlanCreate struct {
	Target    inventory.Ref
	Parent    string
	Comments  string
	Addresses []string
	VID       int
	MTU       int
	Autostart bool
}

func (o VlanCreate) Kind() OpType       { return OpVlanCreate }
func (o VlanCreate) Ref() inventory.Ref { return o.Target }

// VlanName computes the conventional "<parent>.<vid>" interface name.
func VlanName(parent string, vid int) string { return fmt.Sprintf("%s.%d", parent, vid) }

// VlanUpdate patches an existing VLAN sub-interface's options in place.
type VlanUpdate struct {
	Comments  *string
	Target    inventory.Ref
	Addresses []string
	MTU       int
}

func (o VlanUpdate) Kind() OpType       { return OpVlanUpdate }
func (o VlanUpdate) Ref() inventory.Ref { return o.Target }

// VlanDelete removes a VLAN sub-interface's iface stanza and its auto/
// allow-* reference.
type VlanDelete struct{ Target inventory.Ref }

func (o VlanDelete) Kind() OpType       { return OpVlanDelete }
func (o VlanDelete) Ref() inventory.Ref { return o.Target }

// --- wire decode ---------------------------------------------------------

// envelope is the docs/api.md wire shape: {"op": "<type>", "target": Ref,
// "params": {...}}.
type envelope struct {
	Op     OpType          `json:"op"`
	Target string          `json:"target"`
	Params json.RawMessage `json:"params"`
}

// wireParams mirrors the union of every op's params, used only to decode
// the "params" object; unrecognized/absent fields are simply left zero.
// Field names are the camelCase wire names implied by
// docs/data-model.md §3's field lists.
type wireParams struct {
	VlanAware            *bool                `json:"vlanAware"`
	Comments             *string              `json:"comments"`
	Gateway              *string              `json:"gateway"`
	Autostart            *bool                `json:"autostart"`
	STP                  *bool                `json:"stp"`
	MTU                  *int                 `json:"mtu"`
	Bridge               string               `json:"bridge"`
	Port                 string               `json:"port"`
	Parent               string               `json:"parent"`
	XmitHashPolicy       string               `json:"xmitHashPolicy"`
	LacpRate             string               `json:"lacpRate"`
	Mode                 string               `json:"mode"`
	Slaves               []string             `json:"slaves"`
	Addresses            []string             `json:"addresses"`
	Ports                []string             `json:"ports"`
	Vids                 []inventory.VidRange `json:"vids"`
	MIIMon               int                  `json:"miimon"`
	VID                  int                  `json:"vid"`
	RemoveAddress        bool                 `json:"removeAddress"`
	RemoveGateway        bool                 `json:"removeGateway"`
	RemoveVids           bool                 `json:"removeVids"`
	RemoveLacpRate       bool                 `json:"removeLacpRate"`
	RemoveXmitHashPolicy bool                 `json:"removeXmitHashPolicy"`
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func boolOr(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// DecodeOp decodes one {"op","target","params"} envelope (docs/api.md wire
// shape) into a concrete Op value. It is the counterpart to the ops_json a
// real changeset store (T-201) persists, so this package's diff generation
// can be driven directly from stored bytes without a compile-time
// dependency on internal/change's Op type.
func DecodeOp(raw json.RawMessage) (Op, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("ifaces: decoding op envelope: %w", err)
	}
	target, err := inventory.ParseRef(env.Target)
	if err != nil {
		return nil, fmt.Errorf("ifaces: decoding op %q target: %w", env.Op, err)
	}
	var p wireParams
	if len(env.Params) > 0 {
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return nil, fmt.Errorf("ifaces: decoding op %q params: %w", env.Op, err)
		}
	}

	switch env.Op {
	case OpIfaceUpdate:
		return IfaceUpdate{
			Target: target, MTU: p.MTU, Comments: p.Comments, Addresses: p.Addresses,
			Gateway: p.Gateway, Autostart: p.Autostart,
			RemoveAddress: p.RemoveAddress, RemoveGateway: p.RemoveGateway,
		}, nil
	case OpBondCreate:
		return BondCreate{
			Target: target, Mode: p.Mode, Slaves: p.Slaves, MIIMon: p.MIIMon,
			LacpRate: p.LacpRate, XmitHashPolicy: p.XmitHashPolicy, MTU: intOr(p.MTU),
			Comments: strOr(p.Comments), Autostart: boolOr(p.Autostart), Bridge: p.Bridge,
		}, nil
	case OpBondUpdate:
		return BondUpdate{
			Target: target, Mode: p.Mode, Slaves: p.Slaves, MIIMon: p.MIIMon,
			LacpRate: p.LacpRate, XmitHashPolicy: p.XmitHashPolicy, MTU: intOr(p.MTU),
			Comments: p.Comments, RemoveLacpRate: p.RemoveLacpRate,
			RemoveXmitHashPolicy: p.RemoveXmitHashPolicy,
		}, nil
	case OpBondDelete:
		return BondDelete{Target: target}, nil
	case OpBridgeCreate:
		return BridgeCreate{
			Target: target, Ports: p.Ports, VlanAware: boolOr(p.VlanAware), Vids: p.Vids,
			STP: boolOr(p.STP), MTU: intOr(p.MTU), Addresses: p.Addresses,
			Gateway: strOr(p.Gateway), Comments: strOr(p.Comments), Autostart: boolOr(p.Autostart),
		}, nil
	case OpBridgeUpdate:
		return BridgeUpdate{
			Target: target, Ports: p.Ports, VlanAware: p.VlanAware, Vids: p.Vids,
			STP: p.STP, MTU: intOr(p.MTU), Addresses: p.Addresses, Gateway: p.Gateway,
			Comments: p.Comments, RemoveVids: p.RemoveVids, RemoveGateway: p.RemoveGateway,
		}, nil
	case OpBridgeDelete:
		return BridgeDelete{Target: target}, nil
	case OpBridgePortAdd:
		return BridgePortAdd{Target: target, Port: p.Port}, nil
	case OpBridgePortRemove:
		return BridgePortRemove{Target: target, Port: p.Port}, nil
	case OpVlanCreate:
		return VlanCreate{
			Target: target, Parent: p.Parent, VID: p.VID, Addresses: p.Addresses,
			MTU: intOr(p.MTU), Comments: strOr(p.Comments), Autostart: boolOr(p.Autostart),
		}, nil
	case OpVlanUpdate:
		return VlanUpdate{Target: target, Addresses: p.Addresses, MTU: intOr(p.MTU), Comments: p.Comments}, nil
	case OpVlanDelete:
		return VlanDelete{Target: target}, nil
	default:
		return nil, fmt.Errorf("ifaces: unsupported op type %q", env.Op)
	}
}

// DecodeOps decodes a JSON array of envelopes (the ops_json shape a
// changeset draft stores) into concrete Op values, preserving order.
func DecodeOps(raw json.RawMessage) ([]Op, error) {
	var envs []json.RawMessage
	if err := json.Unmarshal(raw, &envs); err != nil {
		return nil, fmt.Errorf("ifaces: decoding ops array: %w", err)
	}
	out := make([]Op, 0, len(envs))
	for i, e := range envs {
		op, err := DecodeOp(e)
		if err != nil {
			return nil, fmt.Errorf("ifaces: op[%d]: %w", i, err)
		}
		out = append(out, op)
	}
	return out, nil
}
