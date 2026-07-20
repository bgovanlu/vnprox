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
	OpSdnApply        OpType = "sdn.apply"

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
	OpSdnApply:        func() Params { return &SdnApplyParams{} },

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

	OpSwitchPortUpdate: func() Params { return &SwitchPortUpdateParams{} },
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
type Op struct {
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

	data, err := json.Marshal(opEnvelope{Op: o.Type, Target: target, Params: paramsJSON})
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
