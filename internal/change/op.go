// SPDX-License-Identifier: Apache-2.0

package change

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// OpType identifies one member of the v1 op vocabulary (docs/data-model.md
// §3). String values are the wire "op" field exactly as documented there.
type OpType string

const (
	OpIfaceUpdate OpType = "iface.update"

	// OpIfaceRawReplace is T-208's raw Monaco editor escape hatch
	// (docs/features/change-management.md §7): a single op replacing one
	// node's entire /etc/network/interfaces content wholesale. Target is a
	// KindNode ref (Node and ID both the node name) — the op has no single
	// iface-namespace entity target, it replaces the whole file.
	OpIfaceRawReplace OpType = "iface.raw.replace"

	OpBondCreate OpType = "bond.create"
	OpBondUpdate OpType = "bond.update"
	OpBondDelete OpType = "bond.delete"

	OpBridgeCreate     OpType = "bridge.create"
	OpBridgeUpdate     OpType = "bridge.update"
	OpBridgeDelete     OpType = "bridge.delete"
	OpBridgePortAdd    OpType = "bridge.port.add"
	OpBridgePortRemove OpType = "bridge.port.remove"

	OpVlanCreate OpType = "vlan.create"
	OpVlanUpdate OpType = "vlan.update"
	OpVlanDelete OpType = "vlan.delete"

	// OpIfaceRename renames a logical iface (bridge/bond/vlan) in place —
	// the stanza header, its auto/allow-* references, and every in-file
	// reference to the old name. Target is the interface being renamed;
	// params carry the new name (IfaceRenameParams). Physical NIC (udev)
	// renames are out of scope (a reboot-realized, hardware-specific
	// procedure documented in the editor), and renaming an interface with
	// guests attached is blocked (validate_safety.go) since guest bridge=
	// bindings live in PVE config, not this file.
	OpIfaceRename OpType = "iface.rename"

	OpSdnZoneCreate   OpType = "sdn.zone.create"
	OpSdnZoneUpdate   OpType = "sdn.zone.update"
	OpSdnZoneDelete   OpType = "sdn.zone.delete"
	OpSdnVnetCreate   OpType = "sdn.vnet.create"
	OpSdnVnetUpdate   OpType = "sdn.vnet.update"
	OpSdnVnetDelete   OpType = "sdn.vnet.delete"
	OpSdnSubnetCreate OpType = "sdn.subnet.create"
	OpSdnSubnetUpdate OpType = "sdn.subnet.update"
	OpSdnSubnetDelete OpType = "sdn.subnet.delete"

	// SDN DNS op family (T-1204). PVE stages and applies the DNS plugin
	// config exactly like zones/vnets/subnets, so these route through the
	// same StepSDNStage/StepSDNApply plan (apply_plan.go's sdnStageOpTypes),
	// not a separate apply path. A DNS zone target is Ref{KindSDNDnsZone, ID:
	// domain}; a record target is Ref{KindSDNDnsRecord, ID:
	// "<zone>/<name>/<type>"}.
	OpSdnDnsZoneCreate   OpType = "sdn.dns.zone.create"
	OpSdnDnsZoneUpdate   OpType = "sdn.dns.zone.update"
	OpSdnDnsZoneDelete   OpType = "sdn.dns.zone.delete"
	OpSdnDnsRecordCreate OpType = "sdn.dns.record.create"
	OpSdnDnsRecordUpdate OpType = "sdn.dns.record.update"
	OpSdnDnsRecordDelete OpType = "sdn.dns.record.delete"

	// SDN Fabric op family (T-3101). Fabrics stage and apply through the
	// exact same PUT /cluster/sdn commit every other sdn.* op uses
	// (apply_plan.go's sdnStageOpTypes) — deliberately, per the task card:
	// a fabric op must not widen planning/reports/T-3101-followup-01.md's
	// filed `--lock-token` gap by inventing a bespoke apply path. A fabric
	// target is Ref{Kind: KindSDNFabric, ID: <fabricID>} (cluster-scoped,
	// Node empty like every other sdn-* Kind).
	OpSdnFabricCreate OpType = "sdn.fabric.create"
	OpSdnFabricUpdate OpType = "sdn.fabric.update"
	OpSdnFabricDelete OpType = "sdn.fabric.delete"

	// SDN Controller op family (T-3102). Controllers stage and apply through
	// the exact same PUT /cluster/sdn commit every other sdn.* op uses
	// (apply_plan.go's sdnStageOpTypes) — the same "no bespoke apply path"
	// discipline OpSdnFabricCreate's doc comment states, so this family does
	// not widen planning/reports/T-3101-followup-01.md's filed `--lock-token`
	// gap either. A controller target is Ref{Kind: KindSDNController, ID:
	// <controllerID>} (cluster-scoped, Node empty like every other sdn-*
	// Kind). A zone's own `controller` field (SdnZoneCreateParams.Controller)
	// is unchanged by this family — it stays a plain string reference by id,
	// now resolving to a first-class sibling object instead of an opaque
	// name (internal/sdn.Service's Tree.Controllers, KindSDNController's doc
	// comment).
	OpSdnControllerCreate OpType = "sdn.controller.create"
	OpSdnControllerUpdate OpType = "sdn.controller.update"
	OpSdnControllerDelete OpType = "sdn.controller.delete"

	// SDN IPAM plugin-instance op family (T-3104). This is the configured
	// IPAM *plugin object* itself (its connection config: url/token/section/
	// fingerprint, at /cluster/sdn/ipams) — NOT an address allocation, which
	// stays OpIpamAllocCreate/OpIpamAllocDelete below unchanged. Like
	// Fabric/Controller above, an ipam create/update/delete stages and
	// applies through the exact same PUT /cluster/sdn commit every other
	// sdn.* op uses (apply_plan.go's sdnStageOpTypes) — the same "no bespoke
	// apply path" discipline, so this family does not widen planning/
	// reports/T-3101-followup-01.md's filed `--lock-token` gap either. An
	// ipam target is Ref{Kind: KindSDNIpam, ID: <ipamID>} (cluster-scoped,
	// Node empty like every other sdn-* Kind). A zone's own `ipam` field
	// (SdnZoneCreateParams.IPAM) is unchanged by this family — it stays a
	// plain string reference by id, now resolving to a first-class sibling
	// object instead of an opaque name (internal/sdn.Service's Tree.Ipams,
	// KindSDNIpam's doc comment), the same "reference by id to a sibling
	// object" shape OpSdnControllerCreate's doc comment establishes for
	// Controller. Unlike Fabric (display-only, never zone-referenced), an
	// ipam instance IS live-polled (KindSDNIpam's doc comment) for the same
	// reason Controller is: a zone's `ipam` field needs a live in-use check
	// on delete (this family's acceptance criterion, mirroring T-3102
	// acceptance criterion 5 for controllers).
	OpSdnIpamCreate OpType = "sdn.ipam.create"
	OpSdnIpamUpdate OpType = "sdn.ipam.update"
	OpSdnIpamDelete OpType = "sdn.ipam.delete"

	OpSdnApply OpType = "sdn.apply"

	OpGuestNicUpdate OpType = "guest.nic.update"

	OpFwRuleCreate    OpType = "fw.rule.create"
	OpFwRuleUpdate    OpType = "fw.rule.update"
	OpFwRuleDelete    OpType = "fw.rule.delete"
	OpFwRuleMove      OpType = "fw.rule.move"
	OpFwOptionsUpdate OpType = "fw.options.update"
	OpFwAliasCreate   OpType = "fw.alias.create"
	OpFwAliasUpdate   OpType = "fw.alias.update"
	OpFwAliasDelete   OpType = "fw.alias.delete"
	OpFwIpsetCreate   OpType = "fw.ipset.create"
	OpFwIpsetUpdate   OpType = "fw.ipset.update"
	OpFwIpsetDelete   OpType = "fw.ipset.delete"
	OpFwGroupCreate   OpType = "fw.group.create"
	OpFwGroupUpdate   OpType = "fw.group.update"
	OpFwGroupDelete   OpType = "fw.group.delete"

	OpIpamAllocCreate OpType = "ipam.alloc.create"
	OpIpamAllocDelete OpType = "ipam.alloc.delete"

	// QoS op group (T-1505, docs/data-model.md §3 addition): a bridge-level
	// tc/HTB traffic shape. Per-guest-NIC rate limiting is deliberately NOT
	// a new op here — it already exists as guest.nic.update's RateMbps
	// field (see internal/qos's package doc comment).
	OpQosShapeCreate OpType = "qos.shape.create"
	OpQosShapeUpdate OpType = "qos.shape.update"
	OpQosShapeDelete OpType = "qos.shape.delete"

	// WireGuard op group (T-1401, docs/data-model.md §3 addition). Every
	// wg.* op is an ordinary changeset op — there is no second mutation path
	// for WireGuard (CLAUDE.md's change-engine invariant).
	OpWgTunnelCreate OpType = "wg.tunnel.create"
	OpWgTunnelUpdate OpType = "wg.tunnel.update"
	OpWgTunnelDelete OpType = "wg.tunnel.delete"
	OpWgPeerAdd      OpType = "wg.peer.add"
	OpWgPeerRemove   OpType = "wg.peer.remove"
	// T-1403's "nat"/"route" op groups (docs/data-model.md §3 addition): a
	// PVE-host SNAT/masquerade or DNAT/port-forward rule, and an
	// additional/policy static route, respectively — each applied via
	// NodeAgent's existing interfaces-file write path as a post-up/post-down
	// stanza pair (internal/change/ifaces/edgeop.go), exactly like every
	// other node-file op (see nodeFileOpTypes in apply_plan.go). A node's
	// *default* gateway stays owned by iface.update's own gateway field;
	// route.static.* never sets it.
	OpNatMasqueradeCreate  OpType = "nat.masquerade.create"
	OpNatMasqueradeDelete  OpType = "nat.masquerade.delete"
	OpNatPortForwardCreate OpType = "nat.portforward.create"
	OpNatPortForwardUpdate OpType = "nat.portforward.update"
	OpNatPortForwardDelete OpType = "nat.portforward.delete"
	OpRouteStaticCreate    OpType = "route.static.create"
	OpRouteStaticUpdate    OpType = "route.static.update"
	OpRouteStaticDelete    OpType = "route.static.delete"
	// OpVFProvision is T-1506's "vf" op group (docs/data-model.md §3
	// addition): configures Target's (a PhysNic acting as an SR-IOV PF)
	// virtual-function pool. Target is the PF's own Ref (KindPhysNic) —
	// the entity whose VF pool is being (re)configured, the same "target
	// is the entity the op principally concerns" convention every op in
	// this vocabulary follows — not a synthetic per-VF id, since a single
	// op can provision an entire batch of VFs on one PF in one shot (see
	// VFProvisionParams' doc comment). There is no vf.update/vf.delete op:
	// re-provisioning (a new count, a changed VLAN/MAC/policy) is always a
	// fresh vf.provision — like nat.masquerade's own "no update" rule, this
	// keeps every VF pool change a visible, individually auditable
	// changeset op rather than a silent overwrite. Applied via the ordinary
	// node-file path (internal/change/ifaces), a post-up/post-down stanza
	// pair appended to the PF's own existing iface stanza — never a second
	// mutation mechanism.
	OpVFProvision OpType = "vf.provision"
	// OpSwitchPortUpdate (T-1205: guarded switch config push) is the sole
	// member of the switch.port.* op group — a single op that sets exactly a
	// switch port's VLAN membership, description, and/or LACP settings, and
	// nothing else. Target is a KindSwitchPort ref (Node empty, ID
	// "<switchID>/<port>"). It is an ordinary changeset op flowing through the
	// full stage→validate→diff→apply→confirm/rollback lifecycle (CLAUDE.md's
	// change-engine invariant): there is no second mutation path for a switch.
	// It ships dark — no push is possible unless both a daemon-level flag and
	// the specific switch's `enabled` are true (docs/security.md).
	OpSwitchPortUpdate OpType = "switch.port.update"
)

// noTargetOps is the (deliberately tiny) set of ops with no natural target
// entity. sdn.apply fires a cluster-wide pending-SDN-config apply — it
// isn't an operation on any one entity, unlike every other op, whose
// target is the entity it creates/updates/deletes, or (for the
// fw.alias/ipset/group and ipam.alloc ops, which have no dedicated
// inventory.Kind of their own) the ruleset/subnet scope it belongs to.
var noTargetOps = map[OpType]bool{
	OpSdnApply: true,
}

// Params is implemented by every op's typed parameter struct (params_*.go).
// The unexported marker method closes the set to this package, mirroring
// internal/inventory.Entity's identical pattern for its own tagged union.
type Params interface {
	isChangeParams()
}

// paramFactories maps every known OpType to a constructor for its params
// struct. op_test.go asserts this set exactly matches the OpType constants
// above (every documented op has a factory, and every factory corresponds
// to a documented op), so a mismatch fails a test rather than panicking at
// decode time in production.
var paramFactories = map[OpType]func() Params{
	OpIfaceUpdate:     func() Params { return &IfaceUpdateParams{} },
	OpIfaceRename:     func() Params { return &IfaceRenameParams{} },
	OpIfaceRawReplace: func() Params { return &IfaceRawReplaceParams{} },

	OpBondCreate: func() Params { return &BondCreateParams{} },
	OpBondUpdate: func() Params { return &BondUpdateParams{} },
	OpBondDelete: func() Params { return &BondDeleteParams{} },

	OpBridgeCreate:     func() Params { return &BridgeCreateParams{} },
	OpBridgeUpdate:     func() Params { return &BridgeUpdateParams{} },
	OpBridgeDelete:     func() Params { return &BridgeDeleteParams{} },
	OpBridgePortAdd:    func() Params { return &BridgePortAddParams{} },
	OpBridgePortRemove: func() Params { return &BridgePortRemoveParams{} },

	OpVlanCreate: func() Params { return &VlanCreateParams{} },
	OpVlanUpdate: func() Params { return &VlanUpdateParams{} },
	OpVlanDelete: func() Params { return &VlanDeleteParams{} },

	OpSdnZoneCreate:   func() Params { return &SdnZoneCreateParams{} },
	OpSdnZoneUpdate:   func() Params { return &SdnZoneUpdateParams{} },
	OpSdnZoneDelete:   func() Params { return &SdnZoneDeleteParams{} },
	OpSdnVnetCreate:   func() Params { return &SdnVnetCreateParams{} },
	OpSdnVnetUpdate:   func() Params { return &SdnVnetUpdateParams{} },
	OpSdnVnetDelete:   func() Params { return &SdnVnetDeleteParams{} },
	OpSdnSubnetCreate: func() Params { return &SdnSubnetCreateParams{} },
	OpSdnSubnetUpdate: func() Params { return &SdnSubnetUpdateParams{} },
	OpSdnSubnetDelete: func() Params { return &SdnSubnetDeleteParams{} },

	OpSdnDnsZoneCreate:   func() Params { return &SdnDnsZoneCreateParams{} },
	OpSdnDnsZoneUpdate:   func() Params { return &SdnDnsZoneUpdateParams{} },
	OpSdnDnsZoneDelete:   func() Params { return &SdnDnsZoneDeleteParams{} },
	OpSdnDnsRecordCreate: func() Params { return &SdnDnsRecordCreateParams{} },
	OpSdnDnsRecordUpdate: func() Params { return &SdnDnsRecordUpdateParams{} },
	OpSdnDnsRecordDelete: func() Params { return &SdnDnsRecordDeleteParams{} },

	OpSdnFabricCreate: func() Params { return &SdnFabricCreateParams{} },
	OpSdnFabricUpdate: func() Params { return &SdnFabricUpdateParams{} },
	OpSdnFabricDelete: func() Params { return &SdnFabricDeleteParams{} },

	OpSdnControllerCreate: func() Params { return &SdnControllerCreateParams{} },
	OpSdnControllerUpdate: func() Params { return &SdnControllerUpdateParams{} },
	OpSdnControllerDelete: func() Params { return &SdnControllerDeleteParams{} },

	OpSdnIpamCreate: func() Params { return &SdnIpamCreateParams{} },
	OpSdnIpamUpdate: func() Params { return &SdnIpamUpdateParams{} },
	OpSdnIpamDelete: func() Params { return &SdnIpamDeleteParams{} },

	OpSdnApply: func() Params { return &SdnApplyParams{} },

	OpGuestNicUpdate: func() Params { return &GuestNicUpdateParams{} },

	OpFwRuleCreate:    func() Params { return &FwRuleCreateParams{} },
	OpFwRuleUpdate:    func() Params { return &FwRuleUpdateParams{} },
	OpFwRuleDelete:    func() Params { return &FwRuleDeleteParams{} },
	OpFwRuleMove:      func() Params { return &FwRuleMoveParams{} },
	OpFwOptionsUpdate: func() Params { return &FwOptionsUpdateParams{} },
	OpFwAliasCreate:   func() Params { return &FwAliasCreateParams{} },
	OpFwAliasUpdate:   func() Params { return &FwAliasUpdateParams{} },
	OpFwAliasDelete:   func() Params { return &FwAliasDeleteParams{} },
	OpFwIpsetCreate:   func() Params { return &FwIpsetCreateParams{} },
	OpFwIpsetUpdate:   func() Params { return &FwIpsetUpdateParams{} },
	OpFwIpsetDelete:   func() Params { return &FwIpsetDeleteParams{} },
	OpFwGroupCreate:   func() Params { return &FwGroupCreateParams{} },
	OpFwGroupUpdate:   func() Params { return &FwGroupUpdateParams{} },
	OpFwGroupDelete:   func() Params { return &FwGroupDeleteParams{} },

	OpIpamAllocCreate: func() Params { return &IpamAllocCreateParams{} },
	OpIpamAllocDelete: func() Params { return &IpamAllocDeleteParams{} },

	OpQosShapeCreate: func() Params { return &QosShapeCreateParams{} },
	OpQosShapeUpdate: func() Params { return &QosShapeUpdateParams{} },
	OpQosShapeDelete: func() Params { return &QosShapeDeleteParams{} },

	OpWgTunnelCreate:       func() Params { return &WgTunnelCreateParams{} },
	OpWgTunnelUpdate:       func() Params { return &WgTunnelUpdateParams{} },
	OpWgTunnelDelete:       func() Params { return &WgTunnelDeleteParams{} },
	OpWgPeerAdd:            func() Params { return &WgPeerAddParams{} },
	OpWgPeerRemove:         func() Params { return &WgPeerRemoveParams{} },
	OpNatMasqueradeCreate:  func() Params { return &NatMasqueradeCreateParams{} },
	OpNatMasqueradeDelete:  func() Params { return &NatMasqueradeDeleteParams{} },
	OpNatPortForwardCreate: func() Params { return &NatPortForwardCreateParams{} },
	OpNatPortForwardUpdate: func() Params { return &NatPortForwardUpdateParams{} },
	OpNatPortForwardDelete: func() Params { return &NatPortForwardDeleteParams{} },
	OpRouteStaticCreate:    func() Params { return &RouteStaticCreateParams{} },
	OpRouteStaticUpdate:    func() Params { return &RouteStaticUpdateParams{} },
	OpRouteStaticDelete:    func() Params { return &RouteStaticDeleteParams{} },
	OpVFProvision:          func() Params { return &VFProvisionParams{} },
	OpSwitchPortUpdate:     func() Params { return &SwitchPortUpdateParams{} },
}

// KnownOpTypes returns every OpType this package can decode, for tests
// (op_test.go) and for callers (e.g. a future frontend-facing "what ops
// exist" endpoint) that want the canonical list rather than re-deriving it.
func KnownOpTypes() []OpType {
	out := make([]OpType, 0, len(paramFactories))
	for t := range paramFactories {
		out = append(out, t)
	}
	return out
}

// Op is one changeset operation: docs/data-model.md §3's tagged union
// `{"op": "<type>", "target": Ref, "params": {...}}`. Target is encoded on
// the wire as a Ref triplet string ("kind:node:id", inventory.Ref.String's
// format) rather than as a nested {kind,node,id} object, matching how
// every other Ref-typed field in this codebase's JSON contracts is
// represented (see internal/topology/types.go's Node.ID / EntityDetail.Ref
// / RelatedRef.Ref) — one encoding convention for Refs across the whole
// API surface.
//
// ID (T-2003, added additively to the v1.7-frozen changeset wire contract —
// docs/architecture.md §10/§13's "new optional fields may be added without a
// version bump") is a stable, opaque identifier a review Comment attaches to
// (review.go). It has no meaning to the change engine itself — ops are not
// otherwise identified by anything stable (two bridge.update ops against the
// same target are legitimately different edits) — and nothing in this
// package ever assigns or reads it; Service.create/UpdateDraft (service.go's
// assignOpIDs) stamp one onto every op that arrives without one, so it
// survives a GET -> edit -> PUT round trip for any op the edit leaves
// untouched (see useDrawerActions.ts's addOps/replaceOps, which always
// re-submit the existing ops array verbatim aside from the ops actually
// being added/removed). Omitted on the wire (never emitted as `""`) so a
// pre-T-2003 caller's JSON is byte-identical.
type Op struct {
	ID     string
	Params Params
	Target inventory.Ref
	Type   OpType
}

// opEnvelope is the wire shape both MarshalJSON and UnmarshalJSON encode/
// decode through. Target is a pointer so a JSON `null` or an entirely
// absent key are both distinguishable from an empty string, and so an
// omitted target for the ops in noTargetOps round-trips as `null` rather
// than an empty-string sentinel.
type opEnvelope struct {
	Op     OpType          `json:"op"`
	ID     string          `json:"id,omitempty"`
	Target *string         `json:"target"`
	Params json.RawMessage `json:"params"`
}

// MarshalJSON encodes o per the opEnvelope shape above.
func (o Op) MarshalJSON() ([]byte, error) {
	paramsJSON := json.RawMessage("{}")
	if o.Params != nil {
		b, err := json.Marshal(o.Params)
		if err != nil {
			return nil, fmt.Errorf("change: marshaling params for op %s: %w", o.Type, err)
		}
		paramsJSON = b
	}

	var target *string
	if !o.Target.IsZero() {
		s := o.Target.String()
		target = &s
	}

	data, err := json.Marshal(opEnvelope{Op: o.Type, ID: o.ID, Target: target, Params: paramsJSON})
	if err != nil {
		return nil, fmt.Errorf("change: marshaling op %s: %w", o.Type, err)
	}
	return data, nil
}

// unknownFieldPattern extracts the offending field name from the stdlib
// encoding/json decoder's DisallowUnknownFields error message — there is
// no structured field for this in the standard library's error type, only
// a message of the form `json: unknown field "foo"` — so decode errors
// below can report a precise JSON path instead of just re-surfacing the
// raw Go error text.
var unknownFieldPattern = regexp.MustCompile(`unknown field "([^"]+)"`)

// UnmarshalJSON strictly decodes one Op per the opEnvelope shape, rejecting
// unknown fields at both the envelope level and within params, and
// validating the op type against the known v1 vocabulary (T-201 acceptance
// criterion 1). Errors are always *OpDecodeError so callers (the draft CRUD
// API) can report the offending path in a 400 validation_failed response
// without needing to parse a plain error string.
func (o *Op) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var env opEnvelope
	if err := dec.Decode(&env); err != nil {
		return decodeErrorAt("", err)
	}

	if env.Op == "" {
		return &OpDecodeError{Path: "op", Message: "op type is required"}
	}
	factory, known := paramFactories[env.Op]
	if !known {
		return &OpDecodeError{Path: "op", Message: fmt.Sprintf("unknown op type %q", env.Op)}
	}

	var target inventory.Ref
	if env.Target == nil || *env.Target == "" {
		if !noTargetOps[env.Op] {
			return &OpDecodeError{Path: "target", Message: fmt.Sprintf("target is required for op %q", env.Op)}
		}
	} else {
		ref, err := inventory.ParseRef(*env.Target)
		if err != nil {
			return &OpDecodeError{Path: "target", Message: err.Error()}
		}
		target = ref
	}

	params := factory()
	paramsData := env.Params
	if len(paramsData) == 0 {
		paramsData = []byte("{}")
	}
	pdec := json.NewDecoder(bytes.NewReader(paramsData))
	pdec.DisallowUnknownFields()
	if err := pdec.Decode(params); err != nil {
		return decodeErrorAt("params", err)
	}

	o.Type = env.Op
	o.ID = env.ID
	o.Target = target
	o.Params = params
	return nil
}

// decodeErrorAt wraps a stdlib json decode error into an *OpDecodeError,
// prefixing prefix (e.g. "params") onto any field name recovered from an
// "unknown field" message so the path reads "params.mtuu" rather than just
// "mtuu". For any other decode error (malformed JSON, a type mismatch),
// prefix (or "op" if prefix is empty, i.e. the error is at the envelope
// level itself) is used as the best-effort path.
func decodeErrorAt(prefix string, err error) *OpDecodeError {
	msg := err.Error()
	if m := unknownFieldPattern.FindStringSubmatch(msg); m != nil {
		path := m[1]
		if prefix != "" {
			path = prefix + "." + path
		}
		return &OpDecodeError{Path: path, Message: fmt.Sprintf("unknown field %q", m[1])}
	}
	path := prefix
	if path == "" {
		path = "op"
	}
	return &OpDecodeError{Path: path, Message: msg}
}
