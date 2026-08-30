// SPDX-License-Identifier: Apache-2.0

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
	// WireGuard schema codes (T-1401): a peer public key that isn't a valid
	// base64 32-byte Curve25519 key, and a listen/endpoint port out of the
	// 1–65535 range.
	codeWgKeyInvalid       = "schema.wg_key_invalid"
	codeWgPortInvalid      = "schema.wg_port_out_of_range"
	codeDHCPRangeInvalid   = "schema.dhcp_range_invalid"
	codeSDNZoneTypeInvalid = "schema.sdn_zone_type_invalid"
	// codeSDNNameInvalid flags an sdn.zone.create/vnet.create whose id
	// contains characters real PVE's SDN id format rejects. Real PVE
	// validates zone/vnet ids against (case-insensitively) `[a-z][a-z0-9]*`
	// — a letter followed by letters/digits, no hyphens/underscores/dots/
	// whitespace — before it will stage the config, so an ill-formed id is
	// the classic mid-apply "Parameter verification failed" (issue #3). This
	// is a context-free per-op charset check (schema class); the *length*
	// limit PVE also enforces is deliberately NOT a blocking error here —
	// the exact cap is version-dependent and unverified against live PVE
	// (needs-hardware-validation), and existing golden fixtures/tests carry
	// longer ids — so length is surfaced as a non-blocking wizard warning
	// (web/src/sdn/wizards/validation.ts) instead.
	codeSDNNameInvalid = "schema.sdn_name_invalid"
	// codeSDNFabricIDInvalid flags an sdn.fabric.create whose id does not
	// match real PVE's captured fabric id pattern — 2 to 8 characters,
	// alphanumeric with interior hyphens allowed
	// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt's `--id` pattern),
	// a materially different (shorter, hyphen-permitting) charset from
	// codeSDNNameInvalid's zone/vnet pattern, so it gets its own code
	// rather than reusing that one.
	codeSDNFabricIDInvalid = "schema.sdn_fabric_id_invalid"
	// codeSDNFabricProtocolInvalid flags an sdn.fabric.create/update whose
	// protocol is not one of bgp|openfabric|ospf|wireguard, or whose
	// protocol-conditional fields (csnpInterval/helloInterval/routeFilter/
	// area/redistribute/persistentKeepalive) don't match the combination
	// real PVE's own conditional schema allows for the chosen protocol —
	// symmetric with pvemock's own sdnFabricProtocolError
	// (internal/pvemock/sdn_fabric.go) so the two validators can never
	// quietly disagree about which combination is legal.
	codeSDNFabricProtocolInvalid = "schema.sdn_fabric_protocol_invalid"
	// codeSDNControllerIDInvalid flags an sdn.controller.create whose id
	// does not match real PVE's captured controller id pattern
	// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt's `--controller`
	// pattern: `[a-zA-Z][a-zA-Z0-9_-]*[a-zA-Z0-9]`) — a materially different
	// charset from both codeSDNNameInvalid's zone/vnet pattern and
	// codeSDNFabricIDInvalid's fabric pattern (this one permits underscores
	// and has no fixed length cap), so it gets its own code.
	codeSDNControllerIDInvalid = "schema.sdn_controller_id_invalid"
	// codeSDNControllerTypeInvalid flags an sdn.controller.create/update
	// whose type is not one of bgp|evpn|faucet|isis, or whose
	// type-conditional fields don't match the combination this file's
	// sdnControllerTypeFields allows for the chosen type — the controller
	// counterpart of codeSDNFabricProtocolInvalid, symmetric with
	// pvemock's own sdnControllerTypeError (internal/pvemock/
	// sdn_controller.go) so the two validators can never quietly disagree.
	codeSDNControllerTypeInvalid = "schema.sdn_controller_type_invalid"
	// codeSDNIpamIDInvalid flags an sdn.ipam.create whose id does not match
	// real PVE's captured ipam id pattern (planning/reports/evidence/
	// pve-9.2.4-sdn-schema.txt's `--ipam` pattern:
	// `[a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9]`) — no underscores/hyphens allowed at
	// all, unlike codeSDNControllerIDInvalid's pattern, so it gets its own
	// code.
	codeSDNIpamIDInvalid = "schema.sdn_ipam_id_invalid"
	// codeSDNIpamTypeInvalid flags an sdn.ipam.create/update whose type is
	// not one of netbox|phpipam|pve, or whose type-conditional fields don't
	// match the combination this file's sdnIpamTypeFields allows for the
	// chosen type — the ipam counterpart of codeSDNControllerTypeInvalid,
	// symmetric with pvemock's own sdnIpamTypeError (internal/pvemock/
	// sdn_ipam.go) so the two validators can never quietly disagree. Unlike
	// Fabric/Controller's per-type field split, this one is params_sdn_ipam.go's
	// own documented inference (the capture gives no per-type breakdown for
	// this family) rather than a captured fact.
	codeSDNIpamTypeInvalid = "schema.sdn_ipam_type_invalid"
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
	// codeIfaceNameInvalid flags an iface.rename whose new name is empty or
	// not a valid Linux interface name (issue #2): at most 15 characters
	// (IFNAMSIZ-1), a leading alphanumeric, then alphanumerics and `._-`
	// only — no whitespace or slash, which the kernel/ifupdown2 reject.
	codeIfaceNameInvalid   = "schema.iface_name_invalid"
	codeRateInvalid        = "schema.rate_invalid"
	codeFwDirectionInvalid = "schema.fw_direction_invalid"
	codeFwActionInvalid    = "schema.fw_action_invalid"
	codeFwPolicyInvalid    = "schema.fw_policy_invalid"
	codeFwLogInvalid       = "schema.fw_log_invalid"
	codeFwPosInvalid       = "schema.fw_pos_invalid"
	// codeFwScopeInvalid (T-3103) flags an fw.rule/fw.options op whose
	// field is syntactically valid but not valid for the *scope* its target
	// names: a "forward" direction rule at guest scope (real PVE has no
	// forward chain on a guest's own vNIC — only cluster, node, and vnet
	// scope do); defaultIn/defaultOut on an fw.options.update targeting a
	// vnet ruleset (real PVE's vnet options endpoint has no policy_in/
	// policy_out fields, hardware-captured — planning/reports/evidence/
	// pve-9.2.4-sdn-schema.txt); logLevelForward set anywhere except vnet
	// scope (only vnet scope is hardware-confirmed to accept it); or
	// defaultForward at guest scope (no forward chain there either).
	codeFwScopeInvalid = "schema.fw_scope_invalid"
	// codeOVSTrunkNotAllowed flags a vlan.create with a non-empty Trunks
	// list but OVS false: trunks are an OVS Int Port concept (ovs-vsctl's
	// Port "trunks" column) — a plain 802.1q sub-interface always carries
	// exactly one VID (Vid itself), so a Trunks list there is meaningless.
	codeOVSTrunkNotAllowed = "schema.ovs_trunk_not_allowed"
	codeFwMacroUnknown     = "schema.fw_macro_unknown"
	// codeVFPlanInvalid (T-1506) flags a vf.provision op whose Count/VFs
	// shape is malformed: neither set, both set, a non-positive Count, a
	// negative/duplicate VFSpec.ID, or a MacAddr set alongside Count > 1
	// (a MAC shared across more than one freshly-numbered VF).
	codeVFPlanInvalid = "schema.vf_plan_invalid"

	// --- T-1403's nat.*/route.static.* schema codes ---------------------

	// codeEdgeRuleIDInvalid flags a nat.*/route.static.* create op whose
	// target id is empty, too long, or outside the safe charset (the id is
	// round-tripped through the rule's own generated marker comment —
	// host.EncodeNat*Marker — so a predictable, printable charset keeps
	// that comment readable in a raw-editor view, even though the
	// underlying url.Values encoding would tolerate more).
	codeEdgeRuleIDInvalid = "schema.edge_rule_id_invalid"
	// codeNatProtoInvalid flags a nat.portforward.* proto outside tcp|udp.
	codeNatProtoInvalid = "schema.nat_proto_invalid"
	// codePortNumberInvalid flags a nat.portforward.* ext/int port outside
	// [1,65535].
	codePortNumberInvalid = "schema.port_number_invalid"

	// --- T-1505's qos.shape.* schema codes -------------------------------

	// codeQosRateInvalid flags a qos.shape.* op whose rateMbit is
	// non-positive, or whose ceilMbit (when set) is below rateMbit — real
	// tc/HTB rejects an HTB class whose ceil is lower than its own
	// guaranteed rate (AC2).
	codeQosRateInvalid = "schema.qos_rate_invalid"
	// codeQosVlanOutOfRange flags a qos.shape.* matchVlan outside the
	// 1-4094 802.1Q VID range (0 and 4095 are reserved).
	codeQosVlanOutOfRange = "schema.qos_vlan_out_of_range"

	// T-1204 SDN DNS schema codes. codeDNSNameInvalid flags a
	// sdn.dns.record.create/zone whose name/domain isn't a valid hostname/
	// FQDN label; codeDNSRecordTypeInvalid a record whose type is outside
	// the accepted A/AAAA/PTR/CNAME/TXT set; codeDNSRecordValueInvalid a
	// record whose value doesn't match its type (an A record whose value is
	// not an IPv4 address, an AAAA whose value is not IPv6). All three are
	// context-free per-op checks; the exact real-PVE/PowerDNS validation is
	// unconfirmed against live hardware (needs-hardware-validation).
	codeDNSNameInvalid        = "schema.dns_name_invalid"
	codeDNSRecordTypeInvalid  = "schema.dns_record_type_invalid"
	codeDNSRecordValueInvalid = "schema.dns_record_value_invalid"
	// T-4112 added three more, for the half of this family that turned out
	// to describe a PowerDNS server connection rather than a DNS zone.
	// codeDNSPluginTypeInvalid flags a type outside PVE 9.2.4's one-value
	// enum ("powerdns" — read off the node, not from documentation);
	// codeDNSFingerprintInvalid a pin that is not a SHA-256 digest, which
	// must be a stage-time refusal because vnprox will not silently fall
	// back to unpinned TLS; codeDNSReverseMaskInvalid an IPv6 reverse-zone
	// mask that is not a multiple of 4, which PowerdnsPlugin.pm itself dies
	// on rather than rounding.
	codeDNSPluginTypeInvalid  = "schema.dns_plugin_type_invalid"
	codeDNSFingerprintInvalid = "schema.dns_fingerprint_invalid"
	codeDNSReverseMaskInvalid = "schema.dns_reverse_mask_invalid"

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
	// codeDNSZoneNotFound (T-1204) flags a sdn.dns.record.* op whose owning
	// DNS zone does not exist — neither in the base snapshot nor created by
	// an earlier sdn.dns.zone.create in the same changeset (net-effect fold).
	codeDNSZoneNotFound      = "referential.dns_zone_not_found"
	codeNodeNotFound         = "referential.node_not_found"
	codeBridgeOrVnetNotFound = "referential.bridge_or_vnet_not_found"
	// codeRenameTargetExists flags an iface.rename whose new name already
	// names another interface on the same node (issue #2).
	codeRenameTargetExists = "referential.rename_target_exists"
	codeVIDOverlap         = "referential.vid_overlap"
	codeAddressOverlap     = "referential.address_overlap"
	codeAddressOutOfSubnet = "referential.address_out_of_subnet"
	codeFwPosOutOfRange    = "referential.fw_pos_out_of_range"
	// codeOVSKindMismatch (T-407) flags mixing Linux-bridge/bond entities
	// into an OVS bridge/bond's port/slave list or vice versa (docs/features/
	// change-management.md §5's OVS kind-selector spec: "mixing Linux-bridge
	// ports into OVS bridges and vice versa -> error"), and the symmetric
	// case for an OVS Int Port's parent (must be an OVS bridge) vs. a plain
	// VLAN sub-interface's parent (must not be one).
	codeOVSKindMismatch  = "referential.ovs_kind_mismatch"
	codeFwObjectNotFound = "referential.fw_object_not_found"
	// codeQosBridgeNotFound (T-1505) flags a qos.shape.* op whose Bridge
	// does not name a currently known bridge on the target's node — the
	// tc/HTB shape would have nothing to attach to.
	codeQosBridgeNotFound = "referential.qos_bridge_not_found"
	// codeIfaceNotFound (T-1403) flags a nat.*/route.static.* op whose Iface
	// does not name a currently known interface(5) stanza on the target's
	// node — the post-up/post-down lines have nowhere to attach.
	codeIfaceNotFound = "referential.iface_not_found"
	// codeRouteGatewayUnreachable (T-1403) flags a route.static.create/
	// update whose Gateway does not fall inside any currently-configured
	// address's subnet on the target's node — real `ip route add ... via
	// <gw>` fails identically ("Nexthop has invalid gateway") when no
	// directly-connected interface can reach it.
	codeRouteGatewayUnreachable = "referential.route_gateway_unreachable"
	// codeFwObjectInUse is T-502 acceptance criterion 2: deleting an
	// alias/ipset/security-group still referenced by at least one rule is
	// blocked. internal/fw.UsageCounts already gives the exact reference
	// list (scope, ruleset ref, position) the editor UI's deep-links need
	// — see checkFwObjectDeletable's doc comment.
	codeFwObjectInUse = "referential.fw_object_in_use"
	// codeSdnControllerInUse is T-3102 acceptance criterion 5: deleting an
	// SDN controller still named by at least one zone's `controller` field
	// is blocked, with the referencing zone count/list — the SDN-object
	// counterpart of codeFwObjectInUse, same "Validate()-time blocking
	// Finding, not a bare Go error" shape (checkSdnControllerDeletable).
	codeSdnControllerInUse = "referential.sdn_controller_in_use"
	// codeSdnIpamInUse is T-3104's acceptance criterion 2 analogue of T-3102
	// acceptance criterion 5: deleting an SDN ipam plugin instance still
	// named by at least one zone's `ipam` field is blocked, with the
	// referencing zone count/list — the ipam counterpart of
	// codeSdnControllerInUse (checkSdnIpamDeletable).
	codeSdnIpamInUse = "referential.sdn_ipam_in_use"
	// codePFNotFound (T-1506) flags a vf.provision op whose Target does not
	// resolve to an existing physnic — the standard "target must exist"
	// referential check every op family already gets, named for this one
	// since VFProvisionParams carries no separate PF field to check
	// (Target itself is the PF, per op.go's OpVFProvision doc comment).
	codePFNotFound = "referential.pf_not_found"
	// codeVFSpoofcheckMismatch (T-1506) is the changeset-validate-time half
	// of the vf_spoofcheck_mismatch check (the drift-finding half lives in
	// internal/drift/sriov.go): a staged vf.provision op would configure a
	// VF whose VLAN/spoof-check setting diverges from its PF's own
	// bridge's VLAN-awareness/VID-set policy (internal/topology.BridgeFor
	// + the same policy comparison drift's standing check reuses).
	codeVFSpoofcheckMismatch = "referential.vf_spoofcheck_mismatch"

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
	// codeRenameGuestsAttached flags an iface.rename of a bridge/vlan with
	// running guests still attached to its old name (issue #2): guest
	// bridge= bindings live in PVE guest config, which this file-only op
	// does not rewrite, so the rename would orphan them. Net-effect-aware
	// (a same-changeset guest.nic.update reattaching them clears it) and
	// AllowDangerousOps-downgradable, exactly like codeGuestBearingBridge.
	codeRenameGuestsAttached = "safety.rename_guests_attached"
	// codeSubnetHasAllocations is T-402's other listed deletion guard
	// (validate_safety.go's subnetDeletionGuardFindings, closed out after
	// T-405 gave this package a live cluster-wide IPAM read to check
	// against): sdn.subnet.delete on a subnet that still has one or more
	// active IPAM allocations (net-effect-aware — an ipam.alloc.delete in
	// the same changeset clears the count, mirroring codeGuestBearingBridge's
	// reattach-in-same-changeset pattern).
	codeSubnetHasAllocations = "safety.subnet_has_allocations"
	// codeDNSZoneHasRecords (T-1204 acceptance criterion 3) flags a
	// sdn.dns.zone.delete whose target zone still has one or more records
	// that this same changeset does not also delete — deleting the zone
	// would orphan them, so the changeset must cascade the record deletes.
	// Net-effect-aware (a sdn.dns.record.delete for each record in the same
	// changeset clears it) and AllowDangerousOps-downgradable, exactly like
	// the subnet/vnet deletion guards.
	codeDNSZoneHasRecords = "safety.dns_zone_has_records"

	// --- switch push (T-1205: guarded switch config push) -----------------
	// The switch.port.* op family's authorization + interlock checks
	// (switchValidate, validate_switch.go). The first three are blocking
	// authorization gates (feature ships dark by construction); the last is a
	// no-override safety interlock mirroring T-703's protected-interface rule.

	// codeSwitchPushDisabled flags any switch.port.* op when the daemon-level
	// [switches] enabled flag is off — no switch push is possible at all
	// (docs/security.md). This gate fires regardless of any individual
	// switch's own enabled state.
	codeSwitchPushDisabled = "switch.push_disabled"
	// codeSwitchNotEnabled flags a switch.port.* op targeting a switch that is
	// either not registered or registered with enabled=false — per-switch
	// explicit opt-in (docs/security.md).
	codeSwitchNotEnabled = "switch.not_enabled"
	// codeSwitchPortNotPVEFacing flags a switch.port.* op targeting a port
	// whose LLDP-observed neighbor is not a known PVE node's PhysNic — the
	// port-scoping guarantee that a push can only ever touch a PVE-node uplink
	// (T-1205 AC3), rejected before any driver call.
	codeSwitchPortNotPVEFacing = "switch.port_not_pve_facing"
	// codeProtectedSwitchPort is T-1205's management-path interlock extended
	// one hop onto the uplink switch port carrying a node's management path: a
	// switch.port.update whose net effect strips the management VLAN from that
	// port is hard-blocked with NO override (mirroring T-703's "no override in
	// UI"), because severing a switch's management VLAN can cut connectivity to
	// hardware vnprox cannot itself recover. It is emitted by switchValidate,
	// not safetyValidate, so AllowDangerousOps never downgrades it.
	codeProtectedSwitchPort = "safety.protected_switch_port"

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
	// codeSDNVNIRequired flags a vnet in a vxlan/evpn zone whose effective
	// tag (the VNI, for those zone types) is 0. Real PVE requires a VNI for
	// a vxlan/evpn vnet and rejects one without at stage time — the guided
	// EVPN/VXLAN wizards used to draft tag 0 silently (issue #3). Zone-type-
	// aware and net-effect-folded like its sdn-class siblings, so a vnet and
	// the zone that gives it its type created in the same changeset are
	// resolved together.
	codeSDNVNIRequired = "sdn.vni_required"
	// codeSDNOverlayReadiness is T-4106's composed overlay-readiness
	// verdict: one finding per touched vxlan/evpn zone combining BGP
	// session state (internal/evpn), VTEP reachability, and measured MTU
	// headroom (internal/mtuprobe) — a single code so a client sees one
	// thing per zone to correlate, not three; the message text itself
	// names which sub-signal(s) failed or could not be determined (see
	// zoneOverlayFinding, validate_overlay.go). SeverityError when BGP is
	// confirmed down or VTEP is confirmed unreachable (blocks, like any
	// other sdn-class error above); SeverityWarning when a signal could
	// not be determined at all, or the MTU headroom check fails.
	codeSDNOverlayReadiness = "sdn.overlay_readiness"
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

	// --- policy-as-code (T-2601: the declarative organisational rule set,
	// policy.go/policy_eval.go). Deliberately ONE code rather than a code
	// per rule: the rule set is operator-authored data, so a per-rule code
	// space would not be a stable vocabulary at all. Which rule fired is
	// carried in the message (rule id + description, acceptance criterion
	// 1); the severity is the rule's own (`deny` -> SeverityError,
	// `warn` -> SeverityWarning), so a client that already routes on
	// severity needs no new handling.

	codePolicyViolation = "policy.violation"
	// codePolicyInvalid flags a stored policy set this build cannot parse
	// (a hand-edited row, or a set written by a newer daemon). It is
	// SeverityError and therefore blocking: a cluster that has declared a
	// policy must never quietly validate as if it had none. The daemon
	// refuses to start on an unparsable policy *file* (LoadPolicyFile), so
	// this is the store-side backstop for the same rule.
	codePolicyInvalid = "policy.invalid"

	// --- cross-node consistency (class 4, T-801: docs/features/
	// change-management.md §2's cross-node class — the same comparisons
	// internal/drift runs against live state, run instead against this
	// changeset's *projected* cluster state, so cross-node breakage a
	// changeset introduces or leaves uncorrected is caught at review time
	// rather than only by the next 30s drift cycle). All three are
	// SeverityError (they block apply): a changeset that would leave the
	// cluster inconsistent should not apply without the operator seeing it.
	// The comparison logic is shared with drift via internal/xnode (one
	// implementation, not two names for one problem); severity and the
	// change.Op fix patch are this package's own.

	// codeCrossnodeBridge flags a same-named bridge whose presence,
	// VLAN-awareness, or VID set diverges across the projected cluster (a
	// VLAN carried on one node's trunk but not a same-named bridge
	// elsewhere — every cluster node is a potential migration target). The
	// VLAN-awareness/VID-set forms carry a fix restoring parity; the
	// presence form does not (creating a missing bridge needs a physical-
	// port decision), mirroring drift's own fixable/not-fixable split.
	codeCrossnodeBridge = "crossnode.bridge_divergence"
	// codeCrossnodeMTU flags a same-named bridge whose MTU diverges across
	// the projected cluster, with a fix aligning outliers to the majority
	// MTU (the same alignment strategy drift's fixable MTU case uses).
	codeCrossnodeMTU = "crossnode.mtu_consistency"
	// codeCrossnodeSDN flags an SDN zone that, in the projected cluster,
	// lists a member node whose realizing bridge does not exist there —
	// closing the gap sdnValidate leaves, where a plain bridge.delete/
	// iface.rename op (no sdn.* op) silently breaks an *untouched* zone's
	// realization on one node. Detection-only, matching drift's own stance
	// (creating the bridge needs a physical-port decision this validator
	// cannot make safely).
	codeCrossnodeSDN = "crossnode.sdn_realization"

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

	// --- federation cluster scoping (T-1201) ----------------------------
	// codeCrossClusterRef is the stable, blocking error code an op whose
	// target Ref belongs to a different attached cluster than the
	// changeset's own ClusterID earns (validate_crosscluster.go). Config
	// ownership stays strictly per-cluster — there is no cross-cluster
	// mutation primitive, so a changeset that would span clusters can never
	// even validate, let alone apply.
	codeCrossClusterRef = "federation.cross_cluster_ref"

	// --- T-4014's tc.mirror.* codes --------------------------------------

	// codeTcMirrorSameIface flags a tc.mirror.create/update whose
	// sourceIface and destIface name the same interface — a session cannot
	// mirror an interface to itself.
	codeTcMirrorSameIface = "schema.tc_mirror_same_iface"
	// codeTcMirrorDurationInvalid flags a tc.mirror.create op whose
	// maxDurationSec is not positive — T-4014's card is explicit that a
	// mirror session "must have a maximum duration": zero/negative is
	// never "unbounded", it is simply invalid.
	codeTcMirrorDurationInvalid = "schema.tc_mirror_duration_invalid"
	// codeTcMirrorBandwidthInvalid flags a tc.mirror.create op whose
	// declared maxMbit (when set) is non-positive.
	codeTcMirrorBandwidthInvalid = "schema.tc_mirror_bandwidth_invalid"
	// codeTcMirrorSourceNotFound / codeTcMirrorDestNotFound (referential
	// class) flag a tc.mirror.create/update whose sourceIface/destIface
	// does not currently exist on the target's node — reusing
	// checkEdgeRuleIface's identical "interface must exist" check
	// nat.*/route.static.* already run (validate_referential.go), one
	// code per field so a client can tell which side is missing.
	codeTcMirrorSourceNotFound = "referential.tc_mirror_source_not_found"
	codeTcMirrorDestNotFound   = "referential.tc_mirror_dest_not_found"
	// codeTcMirrorSourceInUse flags a tc.mirror.create whose sourceIface is
	// already the source of another currently-active mirror session on the
	// same node — T-4014's card requirement "no conflicting existing
	// qdisc": internal/tcmirror's clsact qdisc is exclusively owned by one
	// session (RenderTCTeardown's doc comment), so a second session on the
	// same source would either collide at the tc layer or silently steal
	// the first session's filters. Checked against vnprox's own app-owned
	// tc_mirror_sessions store (SafetyOptions.TcMirror.Usage), the same
	// "app-owned intent is authoritative, not live tc state" stance
	// 0053_tc_mirror_sessions.sql documents for the whole feature, mirroring
	// how every other op family in this codebase checks conflicts against
	// its own store rather than shelling out to inspect live state.
	codeTcMirrorSourceInUse = "safety.tc_mirror_source_in_use"
	// codeTcMirrorProtectedDest flags a tc.mirror.create/update whose
	// destIface names a protected interface (management IP or corosync
	// link) — reusing protected.go's exact management-path protection
	// (ProtectedSet), the same interlock, same AllowDangerousOps override
	// semantics, and same finding code (codeProtectedInterface) every other
	// mgmt-path-cutting op already carries, per T-4014's own card: "reusing
	// internal/change's existing management-path protection... rather than
	// a new check with different semantics." No separate code constant is
	// declared for this — see protectedInterfaceFindings' tc.mirror branch
	// in validate_safety.go, which emits codeProtectedInterface directly.

	// codeTcMirrorConcurrencyCap / codeTcMirrorBandwidthCap (T-4014
	// acceptance criterion 2) flag a tc.mirror.create that would exceed the
	// server-configured concurrent-session count or aggregate declared-
	// bandwidth ceiling for its node — a hard validate-time rejection,
	// never a silent clamp (internal/capture's Caps clamp silently; this
	// deliberately does not — see TcMirrorCreateParams' doc comment).
	codeTcMirrorConcurrencyCap = "safety.tc_mirror_concurrency_cap"
	codeTcMirrorBandwidthCap   = "safety.tc_mirror_bandwidth_cap"
	// codeTcMirrorDurationCap flags a tc.mirror.create/update whose
	// maxDurationSec exceeds the server-configured ceiling
	// (TcMirrorLimits.MaxDurationSec) — distinct from
	// codeTcMirrorDurationInvalid (which flags a non-positive value
	// regardless of any ceiling).
	codeTcMirrorDurationCap = "safety.tc_mirror_duration_cap"
)
