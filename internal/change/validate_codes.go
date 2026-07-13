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
	// codeGatewayNotInSubnet is T-701 acceptance criterion 2: a
	// sdn.subnet.create/update whose gateway is a syntactically valid IP
	// (codeIPInvalid already covers "not an IP at all") but does not fall
	// inside the subnet's own CIDR — real PVE's SubnetPlugin rejects this
	// at subnet stage time (T-701 root-cause analysis §4), so it is a
	// schema-class error (pure, per-op: the subnet's CIDR is always known
	// from the op itself — SdnSubnetCreateParams.CIDR, or op.Target.ID for
	// an update, per that param type's own doc comment that Target.ID
	// *is* the CIDR) rather than needing the sdn class's cross-op
	// projection fold.
	codeGatewayNotInSubnet = "schema.gateway_not_in_subnet"
	codeRateInvalid        = "schema.rate_invalid"
	codeFwDirectionInvalid = "schema.fw_direction_invalid"
	codeFwActionInvalid    = "schema.fw_action_invalid"
	codeFwPolicyInvalid    = "schema.fw_policy_invalid"
	codeFwLogInvalid       = "schema.fw_log_invalid"
	codeFwPosInvalid       = "schema.fw_pos_invalid"
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
	// codeSubnetHasAllocations is T-402's other listed deletion guard
	// (validate_safety.go's subnetDeletionGuardFindings, closed out after
	// T-405 gave this package a live cluster-wide IPAM read to check
	// against): sdn.subnet.delete on a subnet that still has one or more
	// active IPAM allocations (net-effect-aware — an ipam.alloc.delete in
	// the same changeset clears the count, mirroring codeGuestBearingBridge's
	// reattach-in-same-changeset pattern).
	codeSubnetHasAllocations = "safety.subnet_has_allocations"

	// --- sdn (T-402: docs/features/sdn.md §4's documented pre-apply
	// validation — "zone node coverage, bridge existence on member nodes,
	// MTU sanity" — plus tag uniqueness and the vnet-deletion interlock).
	// Node-coverage/bridge-existence/tag-uniqueness are blocking (real PVE
	// apply would itself fail against these), run right after the
	// referential class and before safety; the vnet-deletion guard is
	// safety-interlock-shaped (mirrors codeGuestBearingBridge exactly, incl.
	// AllowDangerousOps downgrade) and lives in validate_safety.go instead.

	codeSDNBridgeMissing = "sdn.bridge_missing_on_node"
	codeSDNTagDuplicate  = "sdn.tag_duplicate"
	// codeSNATRequiresGateway is T-701 acceptance criterion 2: a subnet
	// whose *effective* state (this changeset's own net effect, folded
	// over the base snapshot — see effectiveSubnets) has snat=true but no
	// gateway. Real PVE's SubnetPlugin rejects this shape at subnet stage
	// time (T-701 root-cause analysis §4: "SNAT + no gateway"), so it is
	// blocking like its sdn-class siblings above, not merely advisory.
	codeSNATRequiresGateway = "sdn.snat_requires_gateway"
	// codeEvpnGatewayMissing and codeSNATRequiresExitNode are T-701
	// acceptance criterion 3: real PVE *accepts* both shapes (unlike
	// codeSNATRequiresGateway's blocking case above), but traffic through
	// the subnet is silently broken — an EVPN subnet's anycast gateway is
	// never realized on any node with no gateway configured, and SNAT
	// traffic has nowhere to leave through when its EVPN zone has no
	// effective exit nodes (T-701 root-cause analysis §4). Both are
	// SeverityWarning, emitted by advisoryValidate (validate_advisory.go's
	// evpnSubnetAdvisoryFindings) rather than sdnValidate — the effective-
	// state fold they need (subnet -> vnet -> zone) is SDN-shaped, so the
	// code string stays in this "sdn." namespace even though the class
	// that emits it does not.
	codeEvpnGatewayMissing   = "sdn.evpn_gateway_missing"
	codeSNATRequiresExitNode = "sdn.snat_requires_exit_node"

	// --- advisory (class 5: style/health warnings) ----------------------

	codeAdvisoryBondHashPolicy = "advisory.bond_missing_layer34_hash"
	codeAdvisoryBridgeComment  = "advisory.bridge_missing_comment"
	codeAdvisorySingleSlave    = "advisory.bond_single_slave"
	codeAdvisoryVxlanMTU       = "advisory.vxlan_mtu_no_headroom"
	// codeAdvisoryDHCPRangeOverlap is T-406 acceptance criterion 4: a
	// staged/updated sdn.subnet DHCP range that overlaps one or more
	// existing IPAM allocations warns (never blocks — an operator may
	// deliberately shrink live allocations out of the pool later, or the
	// overlap may be a false alarm against a stale allocation), listing
	// the specific overlapping addresses in the message.
	codeAdvisoryDHCPRangeOverlap = "advisory.dhcp_range_overlap"

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
