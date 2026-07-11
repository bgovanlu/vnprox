package change

// Finding codes: every code is a stable, dotted identifier
// (`<class>.<check>`) so the golden validation suite (validate_test.go) can
// assert exact (code, ref) matches per docs/api.md's documented finding
// shape (`{severity, code, message, ref?, fix?}`). Codes are grouped below
// by the validator class that emits them (docs/features/change-management.md
// §2), in the order those classes run (see validate.go's ValidateWithSafety).
const (
	// --- schema (class 1: types, ranges, enums, syntax) -----------------

	codeRequiredFieldMissing = "schema.required_field_missing"
	codeMTUOutOfRange        = "schema.mtu_out_of_range"
	codeVIDOutOfRange        = "schema.vid_out_of_range"
	codeVIDRangeInvalid      = "schema.vid_range_invalid"
	codeBondModeInvalid      = "schema.bond_mode_invalid"
	codeLACPRateInvalid      = "schema.lacp_rate_invalid"
	codeXmitHashInvalid      = "schema.xmit_hash_policy_invalid"
	codeMIIMonInvalid        = "schema.miimon_invalid"
	codeDuplicateSlave       = "schema.duplicate_slave"
	codeDuplicatePort        = "schema.duplicate_port"
	codeCIDRInvalid          = "schema.cidr_invalid"
	codeIPInvalid            = "schema.ip_invalid"
	codeMACInvalid           = "schema.mac_invalid"
	codeDHCPRangeInvalid     = "schema.dhcp_range_invalid"
	codeSDNZoneTypeInvalid   = "schema.sdn_zone_type_invalid"
	codeRateInvalid          = "schema.rate_invalid"
	codeFwDirectionInvalid   = "schema.fw_direction_invalid"
	codeFwActionInvalid      = "schema.fw_action_invalid"
	codeFwPolicyInvalid      = "schema.fw_policy_invalid"
	codeFwLogInvalid         = "schema.fw_log_invalid"
	codeFwPosInvalid         = "schema.fw_pos_invalid"
	// codeOVSTrunkNotAllowed flags a vlan.create with a non-empty Trunks
	// list but OVS false: trunks are an OVS Int Port concept (ovs-vsctl's
	// Port "trunks" column) — a plain 802.1q sub-interface always carries
	// exactly one VID (Vid itself), so a Trunks list there is meaningless.
	codeOVSTrunkNotAllowed = "schema.ovs_trunk_not_allowed"
	codeFwMacroUnknown     = "schema.fw_macro_unknown"

	// --- referential (class 2: existence, collisions, overlaps) --------

	codeTargetNotFound       = "referential.target_not_found"
	codeAlreadyExists        = "referential.already_exists"
	codeParentNotFound       = "referential.parent_not_found"
	codeSlaveNotFound        = "referential.slave_not_found"
	codePortNotFound         = "referential.port_not_found"
	codePortNotAttached      = "referential.port_not_attached"
	codeDuplicateEnslavement = "referential.duplicate_enslavement"
	codeZoneNotFound         = "referential.zone_not_found"
	codeVnetNotFound         = "referential.vnet_not_found"
	codeNodeNotFound         = "referential.node_not_found"
	codeBridgeOrVnetNotFound = "referential.bridge_or_vnet_not_found"
	codeVIDOverlap           = "referential.vid_overlap"
	codeAddressOverlap       = "referential.address_overlap"
	codeAddressOutOfSubnet   = "referential.address_out_of_subnet"
	codeFwPosOutOfRange      = "referential.fw_pos_out_of_range"
	// codeOVSKindMismatch (T-407) flags mixing Linux-bridge/bond entities
	// into an OVS bridge/bond's port/slave list or vice versa (docs/features/
	// change-management.md §5's OVS kind-selector spec: "mixing Linux-bridge
	// ports into OVS bridges and vice versa -> error"), and the symmetric
	// case for an OVS Int Port's parent (must be an OVS bridge) vs. a plain
	// VLAN sub-interface's parent (must not be one).
	codeOVSKindMismatch  = "referential.ovs_kind_mismatch"
	codeFwObjectNotFound = "referential.fw_object_not_found"
	// codeFwObjectInUse is T-502 acceptance criterion 2: deleting an
	// alias/ipset/security-group still referenced by at least one rule is
	// blocked. internal/fw.UsageCounts already gives the exact reference
	// list (scope, ruleset ref, position) the editor UI's deep-links need
	// — see checkFwObjectDeletable's doc comment.
	codeFwObjectInUse = "referential.fw_object_in_use"

	// --- safety (class 3: protected interfaces, guest-bearing bridges) --
	// T-203, docs/security.md "Safety interlocks" / docs/features/
	// change-management.md §2 class 3. These are SeverityError findings by
	// default; safetyValidate downgrades them to SeverityWarning when
	// SafetyOptions.AllowDangerousOps is set (docs/security.md: "override
	// only via config flag allow_dangerous_ops") — the code stays the same
	// either way, only Severity changes, so a client can still recognize
	// the check that fired.

	codeProtectedInterface = "safety.protected_interface"
	codeGuestBearingBridge = "safety.guest_bearing_bridge"

	// --- advisory (class 5: style/health warnings) ----------------------

	codeAdvisoryBondHashPolicy = "advisory.bond_missing_layer34_hash"
	codeAdvisoryBridgeComment  = "advisory.bridge_missing_comment"
	codeAdvisorySingleSlave    = "advisory.bond_single_slave"

	// --- raw file replace guard (T-208's iface.raw.replace) -------------
	// These are produced outside the classed pipeline above, by Service's
	// expandRawReplaceOps (validate_raw.go) before ValidateWithSafety ever
	// runs — they concern the raw op itself (its content, and the live
	// file it's about to overwrite), not a decoded per-entity op, so they
	// don't fit the schema/referential/safety/advisory classification.

	codeRawReplaceNotExclusive = "raw.not_exclusive_with_other_ops"
	codeRawReplaceParseError   = "raw.parse_error"
	codeRawReplaceReadFailed   = "raw.read_failed"
	codeRawReplaceHashConflict = "raw.hash_conflict"
)
