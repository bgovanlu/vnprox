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
)
