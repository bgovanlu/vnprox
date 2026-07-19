package ifaces

import (
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
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

	// OpIfaceRename renames a logical iface stanza (bridge/bond/vlan) in
	// place, rewriting the stanza header, its auto/allow-* references, and
	// every in-file reference to the old name (bridge-ports/ovs_ports/
	// bond-slaves/ovs_bonds/vlan-raw-device). Physical NIC (udev) renames
	// are out of scope — those are a hardware-specific, reboot-realized
	// procedure the change engine does not perform (see the editor's inline
	// help). Guest bridge= bindings live in PVE guest config, not this file,
	// so the change engine blocks renaming an interface with guests still
	// attached (validate_safety.go) rather than silently orphaning them.
	OpIfaceRename OpType = "iface.rename"

	// OpIfaceRawReplace is T-208's power-user escape hatch (docs/features/
	// change-management.md §7): the raw Monaco editor's save produces a
	// changeset whose single op replaces a node's entire
	// /etc/network/interfaces content wholesale, rather than an AST-level
	// patch. See IfaceRawReplace's doc comment for the mutation semantics.
	OpIfaceRawReplace OpType = "iface.raw.replace"

	// OpNatMasqueradeCreate/Delete and OpNatPortForwardCreate/Update/Delete
	// (T-1403's "nat" op group, docs/data-model.md §3) are a PVE-host
	// SNAT/masquerade rule and a DNAT/port-forward rule, respectively — each
	// applied as a post-up/post-down iptables stanza pair appended to an
	// *existing* iface stanza (Iface), the same interfaces-file write path
	// iface.raw.replace already established. See edgeop.go for the mutation
	// semantics and host.EncodeNat*Marker for how a rule's full state is
	// round-tripped through the generated lines themselves (no second,
	// shadow store).
	OpNatMasqueradeCreate  OpType = "nat.masquerade.create"
	OpNatMasqueradeDelete  OpType = "nat.masquerade.delete"
	OpNatPortForwardCreate OpType = "nat.portforward.create"
	OpNatPortForwardUpdate OpType = "nat.portforward.update"
	OpNatPortForwardDelete OpType = "nat.portforward.delete"

	// OpRouteStaticCreate/Update/Delete (T-1403's "route" op group) adds an
	// additional/policy static route via a post-up/post-down `ip route`
	// stanza pair appended to an existing iface stanza — a node's *default*
	// gateway stays owned by iface.update's own Gateway field (docs/data-
	// model.md §3); this op group never sets it.
	OpRouteStaticCreate OpType = "route.static.create"
	OpRouteStaticUpdate OpType = "route.static.update"
	OpRouteStaticDelete OpType = "route.static.delete"

	// OpVFProvision is T-1506's "vf" op group: configures Target's (a
	// PhysNic acting as an SR-IOV PF) virtual-function pool. Applied as a
	// post-up/post-down stanza pair on Target's own iface — the same
	// interfaces-file write path the nat/route families already use. See
	// vfop.go for the mutation semantics.
	OpVFProvision OpType = "vf.provision"
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

// VlanCreate stages a new VLAN sub-interface, or (when OVS is true) a new
// OVS Int Port. For a plain 802.1q sub-interface, Name is conventionally
// derived as "<Parent>.<VID>" (as seen throughout T-102's testdata corpus,
// e.g. "bond0.10", "vmbr0.20") — Target.ID is expected to already be in
// that form; VlanName can be used to compute it before constructing the op.
// An OVS Int Port's name is caller-chosen (OVS int ports are not named by
// convention — see testdata/interfaces/04-ovs-bridge.interfaces's
// "vlan20").
//
// OVS selects ovs_type=OVSIntPort/ovs_bridge/ovs_options (tag=/trunks=)
// instead of vlan-raw-device/vlan-id (inventory.KindVlan has no dedicated
// OVS-kind sibling the way Bridge/Bond do, so this field carries the
// distinction — see internal/change.VlanCreateParams' doc comment). Parent
// is the OVS bridge name when OVS is true. VID is the OVS access "tag" (0 =
// untagged/native); Trunks is an optional additional trunked VLAN range set.
//
// Gateway (T-703) renders a `gateway` option after the addresses, exactly
// like BridgeCreate.Gateway — a VLAN sub-interface that takes over a node's
// management address needs the node's default route too.
type VlanCreate struct {
	Target    inventory.Ref
	Parent    string
	Gateway   string
	Comments  string
	Addresses []string
	Trunks    []inventory.VidRange
	VID       int
	MTU       int
	Autostart bool
	OVS       bool
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

// --- iface.raw.replace ---------------------------------------------------

// IfaceRawReplace replaces a node's entire /etc/network/interfaces content
// with Content verbatim. Unlike every other op in this package, Mutate does
// not AST-edit f in place — it re-parses Content into a fresh host.File and
// swaps f's Entries wholesale, so the rendered result is Content itself
// (host.File.Render() reproduces an unmutated parse byte-for-byte). Target
// is a inventory.KindNode ref (Node, ID both the node name) since this op
// has no single iface-namespace entity target — it replaces the whole
// file. BaseHash (the sha256 hex of the file's content at editor-open time)
// is a internal/change-level concern for the hash-conflict guard, not
// something this package's file-mutation logic needs, so it is not carried
// here — see internal/change's IfaceRawReplaceParams.
type IfaceRawReplace struct {
	Target  inventory.Ref
	Content string
}

func (o IfaceRawReplace) Kind() OpType       { return OpIfaceRawReplace }
func (o IfaceRawReplace) Ref() inventory.Ref { return o.Target }

// --- nat.masquerade.* / nat.portforward.* / route.static.* (T-1403) -------

// NatMasqueradeCreate is op "nat.masquerade.create": a PVE-host SNAT/
// MASQUERADE rule for traffic leaving Iface, sourced from SourceCIDR.
// Target (Ref{Kind: "nat-rule", ID: <caller-chosen id>}) names the rule
// itself, which has no interfaces(5) stanza of its own — Iface names the
// *existing* stanza (typically the uplink/WAN-facing iface) the generated
// post-up/post-down lines attach to. See edgeop.go.
type NatMasqueradeCreate struct {
	Target     inventory.Ref
	Iface      string
	SourceCIDR string
	Comment    string
}

func (o NatMasqueradeCreate) Kind() OpType       { return OpNatMasqueradeCreate }
func (o NatMasqueradeCreate) Ref() inventory.Ref { return o.Target }

// NatMasqueradeDelete removes a nat.masquerade rule's post-up/post-down
// lines wherever they currently live in the file (found by Target's marker,
// not by re-deriving Iface — see edgeop.go's removeMarkedLines). There is
// no nat.masquerade.update op (docs/data-model.md §3): rotating a
// masquerade rule's shape is delete-and-recreate, mirroring T-1401's own
// key-rotation-is-never-in-place convention for the same "no silent
// overwrite of a generated rule" reason.
type NatMasqueradeDelete struct{ Target inventory.Ref }

func (o NatMasqueradeDelete) Kind() OpType       { return OpNatMasqueradeDelete }
func (o NatMasqueradeDelete) Ref() inventory.Ref { return o.Target }

// NatPortForwardCreate is op "nat.portforward.create": a DNAT rule
// forwarding ExtPort/Proto arriving on Iface to IntIP:IntPort — the classic
// "port forward" a home-lab operator adds to expose one guest's service.
type NatPortForwardCreate struct {
	Target  inventory.Ref
	Iface   string
	Proto   string // tcp|udp
	IntIP   string
	Comment string
	ExtPort int
	IntPort int
}

func (o NatPortForwardCreate) Kind() OpType       { return OpNatPortForwardCreate }
func (o NatPortForwardCreate) Ref() inventory.Ref { return o.Target }

// NatPortForwardUpdate replaces an existing port-forward rule's fields
// (whichever pointers are non-nil; nil fields keep the rule's currently
// stored value — see edgeop.go's mutateNatPortForwardUpdate for how the old
// stored fields are recovered from the marker before being merged with
// these overrides and re-rendered).
type NatPortForwardUpdate struct {
	Iface   *string
	Proto   *string
	IntIP   *string
	Comment *string
	ExtPort *int
	IntPort *int
	Target  inventory.Ref
}

func (o NatPortForwardUpdate) Kind() OpType       { return OpNatPortForwardUpdate }
func (o NatPortForwardUpdate) Ref() inventory.Ref { return o.Target }

// NatPortForwardDelete removes a port-forward rule's lines wherever they
// currently live.
type NatPortForwardDelete struct{ Target inventory.Ref }

func (o NatPortForwardDelete) Kind() OpType       { return OpNatPortForwardDelete }
func (o NatPortForwardDelete) Ref() inventory.Ref { return o.Target }

// RouteStaticCreate is op "route.static.create": an additional/policy
// static route (destCidr via gateway, dev Iface) — a node's *default*
// gateway stays owned by IfaceUpdate.Gateway; this op never sets it (see
// route validation's own doc comment in internal/change).
type RouteStaticCreate struct {
	Target   inventory.Ref
	Iface    string
	DestCIDR string
	Gateway  string
	Comment  string
	Metric   int
}

func (o RouteStaticCreate) Kind() OpType       { return OpRouteStaticCreate }
func (o RouteStaticCreate) Ref() inventory.Ref { return o.Target }

// RouteStaticUpdate replaces an existing static route's fields (same
// merge-with-stored-state semantics as NatPortForwardUpdate).
type RouteStaticUpdate struct {
	Iface    *string
	DestCIDR *string
	Gateway  *string
	Comment  *string
	Metric   *int
	Target   inventory.Ref
}

func (o RouteStaticUpdate) Kind() OpType       { return OpRouteStaticUpdate }
func (o RouteStaticUpdate) Ref() inventory.Ref { return o.Target }

// RouteStaticDelete removes a static route's lines wherever they currently
// live.
type RouteStaticDelete struct{ Target inventory.Ref }

func (o RouteStaticDelete) Kind() OpType       { return OpRouteStaticDelete }
func (o RouteStaticDelete) Ref() inventory.Ref { return o.Target }

// --- vf.provision (T-1506) -------------------------------------------------

// VFProvision is op "vf.provision": configures Target's (a PhysNic acting
// as an SR-IOV PF) virtual-function pool. Exactly one of Count/VFs is set
// (internal/change's schema validation enforces this before an op ever
// reaches here) — see host.ResolveVFPlan, the single shared function this
// package's mutator and internal/change's validators both call to expand
// Count/VFs + the top-level MacAddr/VLAN/SpoofCheck/Trust defaults into a
// concrete per-VF plan, so validation and apply can never disagree about
// what a vf.provision op actually configures.
type VFProvision struct {
	SpoofCheck *bool
	Trust      *bool
	Target     inventory.Ref
	MacAddr    string
	VFs        []host.VFSpec
	VLAN       int
	Count      int
}

func (o VFProvision) Kind() OpType       { return OpVFProvision }
func (o VFProvision) Ref() inventory.Ref { return o.Target }

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
	Iface                *string              `json:"iface"`
	Comments             *string              `json:"comments"`
	Gateway              *string              `json:"gateway"`
	Autostart            *bool                `json:"autostart"`
	STP                  *bool                `json:"stp"`
	MTU                  *int                 `json:"mtu"`
	Metric               *int                 `json:"metric"`
	IntPort              *int                 `json:"intPort"`
	ExtPort              *int                 `json:"extPort"`
	VlanAware            *bool                `json:"vlanAware"`
	Comment              *string              `json:"comment"`
	DestCIDR             *string              `json:"destCidr"`
	IntIP                *string              `json:"intIp"`
	Proto                *string              `json:"proto"`
	SourceCIDR           *string              `json:"sourceCidr"`
	VLAN                 *int                 `json:"vlan"`
	SpoofCheck           *bool                `json:"spoofCheck"`
	MacAddr              *string              `json:"macAddr"`
	Trust                *bool                `json:"trust"`
	VFs                  []host.VFSpec        `json:"vfs"`
	Count                int                  `json:"count"`
	XmitHashPolicy       string               `json:"xmitHashPolicy"`
	LacpRate             string               `json:"lacpRate"`
	NewName              string               `json:"newName"`
	Mode                 string               `json:"mode"`
	Content              string               `json:"content"`
	Parent               string               `json:"parent"`
	Port                 string               `json:"port"`
	Bridge               string               `json:"bridge"`
	Ports                []string             `json:"ports"`
	Trunks               []inventory.VidRange `json:"trunks"`
	Addresses            []string             `json:"addresses"`
	Slaves               []string             `json:"slaves"`
	Vids                 []inventory.VidRange `json:"vids"`
	VID                  int                  `json:"vid"`
	MIIMon               int                  `json:"miimon"`
	RemoveAddress        bool                 `json:"removeAddress"`
	OVS                  bool                 `json:"ovs"`
	RemoveXmitHashPolicy bool                 `json:"removeXmitHashPolicy"`
	RemoveLacpRate       bool                 `json:"removeLacpRate"`
	RemoveVids           bool                 `json:"removeVids"`
	RemoveGateway        bool                 `json:"removeGateway"`
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
			Gateway: strOr(p.Gateway),
			MTU:     intOr(p.MTU), Comments: strOr(p.Comments), Autostart: boolOr(p.Autostart),
			OVS: p.OVS, Trunks: p.Trunks,
		}, nil
	case OpVlanUpdate:
		return VlanUpdate{Target: target, Addresses: p.Addresses, MTU: intOr(p.MTU), Comments: p.Comments}, nil
	case OpVlanDelete:
		return VlanDelete{Target: target}, nil
	case OpIfaceRename:
		return IfaceRename{Target: target, NewName: p.NewName}, nil
	case OpIfaceRawReplace:
		return IfaceRawReplace{Target: target, Content: p.Content}, nil
	case OpNatMasqueradeCreate:
		return NatMasqueradeCreate{
			Target: target, Iface: strOr(p.Iface), SourceCIDR: strOr(p.SourceCIDR), Comment: strOr(p.Comment),
		}, nil
	case OpNatMasqueradeDelete:
		return NatMasqueradeDelete{Target: target}, nil
	case OpNatPortForwardCreate:
		return NatPortForwardCreate{
			Target: target, Iface: strOr(p.Iface), Proto: strOr(p.Proto), IntIP: strOr(p.IntIP),
			Comment: strOr(p.Comment), ExtPort: intOr(p.ExtPort), IntPort: intOr(p.IntPort),
		}, nil
	case OpNatPortForwardUpdate:
		return NatPortForwardUpdate{
			Target: target, Iface: p.Iface, Proto: p.Proto, IntIP: p.IntIP,
			Comment: p.Comment, ExtPort: p.ExtPort, IntPort: p.IntPort,
		}, nil
	case OpNatPortForwardDelete:
		return NatPortForwardDelete{Target: target}, nil
	case OpRouteStaticCreate:
		return RouteStaticCreate{
			Target: target, Iface: strOr(p.Iface), DestCIDR: strOr(p.DestCIDR), Gateway: strOr(p.Gateway),
			Comment: strOr(p.Comment), Metric: intOr(p.Metric),
		}, nil
	case OpRouteStaticUpdate:
		return RouteStaticUpdate{
			Target: target, Iface: p.Iface, DestCIDR: p.DestCIDR, Gateway: p.Gateway,
			Comment: p.Comment, Metric: p.Metric,
		}, nil
	case OpRouteStaticDelete:
		return RouteStaticDelete{Target: target}, nil
	case OpVFProvision:
		return VFProvision{
			Target: target, MacAddr: strOr(p.MacAddr), VFs: p.VFs,
			VLAN: intOr(p.VLAN), Count: p.Count, SpoofCheck: p.SpoofCheck, Trust: p.Trust,
		}, nil
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
