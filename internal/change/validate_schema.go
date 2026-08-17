package change

import (
	"net"
	"regexp"
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// ifaceNameRe is a valid Linux interface name for a rename target: a
// leading alphanumeric, then alphanumerics and `._-` (dots appear in VLAN
// sub-interface names like "vmbr0.100"). Length is capped separately at 15
// (IFNAMSIZ-1). See codeIfaceNameInvalid.
var ifaceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const maxIfaceNameLen = 15

// edgeRuleIDRe is the safe charset for a T-1403 nat-rule/static-route
// target id (schemaEdgeRuleID): letters, digits, dots, dashes, underscores
// — a printable, predictable id keeps the rule's generated marker comment
// (host.EncodeNat*Marker) readable in a raw-editor view.
var edgeRuleIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const maxEdgeRuleIDLen = 64

// schemaEdgeRuleID flags an empty, too-long, or out-of-charset nat.*/
// route.static.* create op target id.
func schemaEdgeRuleID(id, ref string, out *[]Finding) {
	switch {
	case id == "":
		*out = append(*out, errorf(codeEdgeRuleIDInvalid, ref, "a nat/route rule requires a non-empty target id"))
	case len(id) > maxEdgeRuleIDLen:
		*out = append(*out, errorf(codeEdgeRuleIDInvalid, ref, "rule id %q is too long (max %d characters)", id, maxEdgeRuleIDLen))
	case !edgeRuleIDRe.MatchString(id):
		*out = append(*out, errorf(codeEdgeRuleIDInvalid, ref, "rule id %q is not valid — use letters, digits, and .-_ only, starting with a letter or digit", id))
	}
}

var validNatProtos = map[string]bool{"tcp": true, "udp": true}

// schemaNatProto flags a nat.portforward.* proto outside tcp|udp.
func schemaNatProto(proto, ref string, out *[]Finding) {
	if !validNatProtos[proto] {
		*out = append(*out, errorf(codeNatProtoInvalid, ref, "proto %q must be one of tcp, udp", proto))
	}
}

// schemaPortNumber flags a nat.portforward.* ext/int port outside
// [1,65535].
func schemaPortNumber(port int, ref string, out *[]Finding) {
	if port < 1 || port > 65535 {
		*out = append(*out, errorf(codePortNumberInvalid, ref, "port %d out of range [1,65535]", port))
	}
}

// sdnIDRe is real PVE's SDN zone/vnet id charset (case-insensitively
// `[a-z][a-z0-9]*`): a leading letter, then letters/digits only — no
// hyphens, underscores, dots, or whitespace. See codeSDNNameInvalid.
var sdnIDRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// schemaSDNName flags an ill-formed SDN zone/vnet id (charset only — see
// codeSDNNameInvalid on why length is not blocked here). A vnet's Ref.ID is
// the "<zone>/<vnet>" form (params_sdn.go), so the leaf after the last "/"
// is the actual PVE-facing id to validate; a zone id carries no "/".
func schemaSDNName(kind, id, ref string, out *[]Finding) {
	leaf := id
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		leaf = id[i+1:]
	}
	if leaf == "" {
		return // emptiness is a referential/required-field concern, not charset
	}
	if !sdnIDRe.MatchString(leaf) {
		*out = append(*out, errorf(codeSDNNameInvalid, ref,
			"%s name %q is not valid — use only letters and digits, starting with a letter (Proxmox rejects other characters)", kind, leaf))
	}
}

// Range/enum constants for the schema validator class (docs/features/
// change-management.md §2 class 1: "types, ranges (VID 1–4094, MTU
// 576–9216, bond mode enums, CIDR syntax)").
const (
	minMTU = 576
	maxMTU = 9216
	minVID = 1
	maxVID = 4094
)

var validBondModes = map[string]bool{
	"balance-rr": true, "active-backup": true, "balance-xor": true,
	"broadcast": true, "802.3ad": true, "balance-tlb": true, "balance-alb": true,
}

// validOVSBondModes is the enum for an OVS bond's Mode (op.Target.Kind ==
// KindOVSBond) — a materially different vocabulary from the Linux bonding
// driver's validBondModes (e.g. "balance-slb"/"balance-tcp" are OVS-only;
// "balance-rr"/"balance-xor"/"broadcast"/"balance-tlb"/"balance-alb" are
// Linux-bonding-only and not valid ovs-vsctl bond_mode values). "802.3ad"
// and "lacp" are accepted as user-facing aliases for LACP-mode OVS bonding
// (internal/change/ifaces.ovsBondModeOptions renders either as
// "bond_mode=balance-slb lacp=active ...", matching
// testdata/interfaces/05-ovs-bond.interfaces).
var validOVSBondModes = map[string]bool{
	"active-backup": true, "balance-slb": true, "balance-tcp": true,
	"802.3ad": true, "lacp": true,
}

// bondModeSet selects the bond-mode enum to validate Mode against, per
// target.Kind (docs/features/change-management.md §5's OVS deliverable:
// "OVS bond mode enums validated").
func bondModeSet(kind inventory.Kind) map[string]bool {
	if kind == inventory.KindOVSBond {
		return validOVSBondModes
	}
	return validBondModes
}

var validLACPRates = map[string]bool{"slow": true, "fast": true}

var validXmitHashPolicies = map[string]bool{
	"layer2": true, "layer2+3": true, "layer3+4": true, "encap2+3": true, "encap3+4": true,
}

// validSdnZoneTypes mirrors real PVE's SDN zone `type` enum, captured from
// a running PVE 9.2.4 node rather than transcribed from documentation:
//
//	--type <evpn | faucet | qinq | simple | vlan | vxlan>
//
// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt). Keep it in sync
// with that capture — TestValidSdnZoneTypesMatchTheCapturedEnum fails if
// it drifts, and says which file to re-read.
//
// "faucet" was missing until 2026-08-16, which meant vnprox refused to
// stage a zone real Proxmox accepts. Note what this map does and does not
// decide: it is the *validator*, not the wizard. Accepting faucet here lets
// an operator who already has a faucet zone edit their SDN config without
// vnprox rejecting it; it deliberately does not add a faucet zone wizard,
// because a faucet zone needs a faucet controller and offering a wizard for
// a combination vnprox cannot complete is worse than offering none. That
// half is T-3101/T-3102.
//
// Note also that "openfabric" and "ospf" are NOT zone types and must not be
// added here — they are SDN *fabric* protocols, a different object family.
// The repository believed otherwise for four phases; see
// pvemock.PVEVersionProfile.SDNFabrics.
var validSdnZoneTypes = map[string]bool{
	"simple": true, "vlan": true, "qinq": true, "vxlan": true, "evpn": true, "faucet": true,
}

// sdnFabricIDRe is real PVE's SDN fabric id charset+length, captured
// verbatim (planning/reports/evidence/pve-9.2.4-sdn-schema.txt):
//
//	[a-zA-Z0-9][a-zA-Z0-9-]{0,6}[a-zA-Z0-9]
//
// 2 to 8 characters, alphanumeric with interior hyphens — a materially
// different (shorter, hyphen-permitting) charset from sdnIDRe's zone/vnet
// pattern above, so it is its own regex rather than a variant of that one.
var sdnFabricIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,6}[a-zA-Z0-9]$`)

// schemaSDNFabricID flags an ill-formed SDN fabric id — unlike
// schemaSDNName, PVE's own regex already bounds the length (2-8 chars), so
// (unlike codeSDNNameInvalid's zone/vnet charset check) there is no
// separate non-blocking length warning: an out-of-range fabric id is
// blocking here.
func schemaSDNFabricID(id, ref string, out *[]Finding) {
	if id == "" {
		return // emptiness is a referential/required-field concern, not charset
	}
	if !sdnFabricIDRe.MatchString(id) {
		*out = append(*out, errorf(codeSDNFabricIDInvalid, ref,
			"sdn fabric id %q is not valid — must be 2-8 characters, alphanumeric with interior hyphens (Proxmox rejects other shapes)", id))
	}
}

// validSdnFabricProtocols mirrors real PVE's SDN fabric `--protocol` enum,
// captured from the same PVE 9.2.4 node as validSdnZoneTypes (planning/
// reports/evidence/pve-9.2.4-sdn-schema.txt):
//
//	--protocol  <bgp | openfabric | ospf | wireguard>
//
// Keep it in sync with that capture — TestValidSdnFabricProtocolsMatchTheCapturedEnum
// (validate_schema_enum_test.go) fails if it drifts, and says which file to
// re-read, the same discipline validSdnZoneTypes' guard test uses. One of
// these — wireguard — is genuinely WireGuard, but a different management
// plane than params_wg.go's T-1401 tunnels (see inventory.KindSDNFabric's
// doc comment); nothing here or in params_wg.go references the other
// family's types.
var validSdnFabricProtocols = map[string]bool{
	"bgp": true, "openfabric": true, "ospf": true, "wireguard": true,
}

// sdnFabricProtocolFields names which of SdnFabricCreateParams' protocol-
// conditional fields the capture's "Conditional options:" blocks allow for
// each protocol — the schema-validator half of the same rule pvemock's own
// sdnFabricProtocolError (internal/pvemock/sdn_fabric.go) enforces
// server-side. Field names are this file's own vocabulary (matching the
// switch below), not the wire's camelCase or PVE's snake_case.
var sdnFabricProtocolFields = map[string]map[string]bool{
	"bgp":        {"redistribute": true},
	"openfabric": {"csnpInterval": true, "helloInterval": true, "routeFilter": true},
	"ospf":       {"area": true, "redistribute": true, "routeFilter": true},
	"wireguard":  {"persistentKeepalive": true},
}

// schemaSDNFabricProtocolFields flags an sdn.fabric.create/update whose
// protocol-conditional fields don't match the allowed set for protocol —
// e.g. csnpInterval set on a "bgp" fabric. protocol must already be known
// valid (schemaValidateOp only calls this after the enum check above
// passes) — an unrecognized protocol has no entry in
// sdnFabricProtocolFields, so every conditional field would spuriously
// flag; callers guard against that by checking validSdnFabricProtocols
// first.
func schemaSDNFabricProtocolFields(protocol string, set map[string]bool, ref string, out *[]Finding) {
	allowed := sdnFabricProtocolFields[protocol]
	for field, isSet := range set {
		if isSet && !allowed[field] {
			*out = append(*out, errorf(codeSDNFabricProtocolInvalid, ref,
				"sdn fabric field %q is not valid for protocol %q", field, protocol))
		}
	}
}

// sdnControllerIDRe is real PVE's SDN controller id charset, captured
// verbatim (planning/reports/evidence/pve-9.2.4-sdn-schema.txt):
//
//	[a-zA-Z][a-zA-Z0-9_-]*[a-zA-Z0-9]
//
// — a leading letter, then any mix of letters/digits/underscore/hyphen, then
// a trailing letter or digit (so a lone-letter id is valid but a trailing
// underscore/hyphen is not) — a materially different charset from both
// sdnIDRe's zone/vnet pattern (no underscore/hyphen at all) and
// sdnFabricIDRe's fabric pattern (no underscore, and length-capped at 8;
// this one carries no captured length cap), so it is its own regex.
var sdnControllerIDRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*[a-zA-Z0-9]$`)

// schemaSDNControllerID flags an ill-formed SDN controller id.
func schemaSDNControllerID(id, ref string, out *[]Finding) {
	if id == "" {
		return // emptiness is a referential/required-field concern, not charset
	}
	if !sdnControllerIDRe.MatchString(id) {
		*out = append(*out, errorf(codeSDNControllerIDInvalid, ref,
			"sdn controller id %q is not valid — must start with a letter, end with a letter or digit, and contain only letters, digits, underscores and hyphens in between (Proxmox rejects other shapes)", id))
	}
}

// validSdnControllerTypes mirrors real PVE's SDN controller `--type` enum,
// captured from the same PVE 9.2.4 node as validSdnZoneTypes/
// validSdnFabricProtocols (planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt):
//
//	--type <bgp | evpn | faucet | isis>
//
// Keep it in sync with that capture — TestValidSdnControllerTypesMatchTheCapturedEnum
// (validate_schema_enum_test.go) fails if it drifts, the same discipline
// validSdnZoneTypes'/validSdnFabricProtocols' guard tests use. "faucet" here
// is the controller a faucet zone needs (validSdnZoneTypes' own "faucet"
// entry's doc comment: "a faucet zone needs a faucet controller") — landing
// both consistently is this task's own explicit brief, not a coincidence.
var validSdnControllerTypes = map[string]bool{
	"bgp": true, "evpn": true, "faucet": true, "isis": true,
}

// sdnControllerTypeFields names which of SdnControllerCreateParams'
// type-conditional fields are legal for each controller type — the
// controller counterpart of sdnFabricProtocolFields. Unlike the fabric
// capture, the controller capture has no "Conditional options:" grouping,
// so this assignment is inferred from each field's own description
// (params_sdn_controller.go's doc comment states the same inference and
// flags it in needs-hardware-validation.md) rather than read directly off a
// grouped block: bgp gets asn/bgpMode/bgpMultipathAsPathRelax/ebgp/
// ebgpMultihop/peers; evpn gets fabric/peerGroupName/routeMapIn/
// routeMapOut; isis gets isisDomain/isisIfaces/isisNet; faucet gets none —
// do not invent fields for it. node/nodes/loopback are general (every type
// may set them) and so are deliberately absent from every entry below, the
// same "general fields excluded from the conditional check" convention
// schemaSDNFabricProtocolFields' caller already uses for a fabric's
// ipPrefix/ip6Prefix.
var sdnControllerTypeFields = map[string]map[string]bool{
	"bgp": {
		"asn": true, "bgpMode": true, "bgpMultipathAsPathRelax": true,
		"ebgp": true, "ebgpMultihop": true, "peers": true,
	},
	"evpn": {
		"fabric": true, "peerGroupName": true, "routeMapIn": true, "routeMapOut": true,
	},
	"isis": {
		"isisDomain": true, "isisIfaces": true, "isisNet": true,
	},
	"faucet": {},
}

// schemaSDNControllerTypeFields flags an sdn.controller.create/update whose
// type-conditional fields don't match the allowed set for typ — e.g. asn
// set on an "isis" controller. typ must already be known valid (callers
// check validSdnControllerTypes first, same discipline
// schemaSDNFabricProtocolFields' caller uses).
func schemaSDNControllerTypeFields(typ string, set map[string]bool, ref string, out *[]Finding) {
	allowed := sdnControllerTypeFields[typ]
	for field, isSet := range set {
		if isSet && !allowed[field] {
			*out = append(*out, errorf(codeSDNControllerTypeInvalid, ref,
				"sdn controller field %q is not valid for type %q", field, typ))
		}
	}
}

// validFwDirections includes "group" alongside the real traffic
// directions "in"/"out"/"forward": a rule row whose Direction is "group" is
// not a traffic-direction rule at all but a security-group reference
// (T-501's documented convention, matching real PVE's own "type":"group"
// rule shape — see internal/fw/resolve.go's appendRule doc comment), so it
// must pass this same-field schema check too.
//
// "forward" (T-3103) is hardware-captured at cluster, node, and vnet scope
// (planning/reports/evidence/pve-9.2.4-sdn-schema.txt's "--type <forward |
// group | in | out>", independently confirmed at all three) — pinned
// against that capture by validate_schema_enum_test.go. It is deliberately
// NOT unconditionally valid everywhere: guest scope was not part of that
// capture, and real pve-firewall has no FORWARD chain on a guest's own
// vNIC (only a routing/host enforcement point has one) — see
// schemaFwDirectionForTarget, which rejects "forward" specifically at
// guest scope using this same base set for everything else.
var validFwDirections = map[string]bool{"in": true, "out": true, "group": true, "forward": true}

var validFwActions = map[string]bool{"ACCEPT": true, "DROP": true, "REJECT": true}

// validFwForwardPolicies is policy_forward's own accepted set (T-3103,
// hardware-captured at cluster and vnet scope: planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt's "--policy_forward <ACCEPT | DROP>") —
// deliberately narrower than validFwActions: REJECT is not a valid forward
// policy, unlike policy_in/policy_out.
var validFwForwardPolicies = map[string]bool{"ACCEPT": true, "DROP": true}

// validFwLogLevelsForward is log_level_forward's own accepted set (T-3103,
// hardware-captured only at vnet scope: planning/reports/evidence/
// pve-9.2.4-sdn-schema.txt's "--log_level_forward <alert | crit | debug |
// emerg | err | info | nolog | notice | warning>"). Deliberately a
// separate set from validFwLogLevels (the per-rule Log field's enum,
// unrelated to this ruleset-level option) rather than reused: that set's
// "low"/"" entries were never captured for log_level_forward, and this one
// carries "debug", which validFwLogLevels does not — the two enums are not
// confirmed to be the same set, so this task does not assume they are.
var validFwLogLevelsForward = map[string]bool{
	"alert": true, "crit": true, "debug": true, "emerg": true, "err": true,
	"info": true, "nolog": true, "notice": true, "warning": true,
}

// validFwLogLevels are PVE's documented firewall log levels; "" is always
// allowed since Log is an omitempty field on every fw op that carries it.
var validFwLogLevels = map[string]bool{
	"": true, "nolog": true, "low": true, "notice": true, "info": true,
	"warning": true, "err": true, "crit": true, "alert": true, "emerg": true,
}

// refOf returns op's target as a Ref string for a Finding, or "" for the
// one op with no target (sdn.apply — see op.go's noTargetOps).
func refOf(op Op) string {
	if op.Target.IsZero() {
		return ""
	}
	return op.Target.String()
}

func validCIDR(s string) bool {
	if s == "" {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

func validIP(s string) bool {
	if s == "" {
		return true
	}
	return net.ParseIP(s) != nil
}

func validMAC(s string) bool {
	if s == "" {
		return true
	}
	_, err := net.ParseMAC(s)
	return err == nil
}

// validDHCPRange checks the loose "startIP-endIP" shape docs/data-model.md
// documents for SdnSubnet.DHCPRanges.
func validDHCPRange(s string) bool {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return false
	}
	return net.ParseIP(strings.TrimSpace(parts[0])) != nil && net.ParseIP(strings.TrimSpace(parts[1])) != nil
}

// schemaValidate is validator class 1: per-op types/ranges/enums/syntax,
// independent of the inventory snapshot or any other op in the changeset.
//
// Note: the "iface.raw.replace must be exclusive on its node" check lives
// in validate_raw.go's rawReplaceExclusiveFindings instead of here, even
// though it is schema-shaped (pure, no snapshot needed) — it must run
// against the *pre-expansion* ops Service.validate receives, before
// expandRawReplaceOps injects that op's own synthesized delta (which also
// targets the same node and would otherwise trip this same check against
// itself).
func schemaValidate(ops []Op) []Finding {
	var out []Finding
	for _, op := range ops {
		out = append(out, schemaValidateOp(op)...)
	}
	return out
}

func schemaValidateOp(op Op) []Finding {
	ref := refOf(op)
	var out []Finding

	switch p := op.Params.(type) {
	case *IfaceUpdateParams:
		schemaMTUPtr(op, p.MTU, ref, &out)
		schemaAddressesPtr(p.Addresses, ref, &out)
		schemaIPPtr(p.Gateway, ref, &out)

	case *IfaceRenameParams:
		name := strings.TrimSpace(p.NewName)
		switch {
		case name == "":
			out = append(out, errorf(codeIfaceNameInvalid, ref, "iface.rename requires a new name"))
		case len(name) > maxIfaceNameLen:
			out = append(out, errorf(codeIfaceNameInvalid, ref, "interface name %q is too long — the kernel allows at most %d characters", name, maxIfaceNameLen))
		case !ifaceNameRe.MatchString(name):
			out = append(out, errorf(codeIfaceNameInvalid, ref, "interface name %q is not valid — use letters, digits, and .-_ only, starting with a letter or digit", name))
		}

	case *IfaceRawReplaceParams:
		// Content's syntax is checked by Service.expandRawReplaceOps
		// (validate_raw.go) before this pipeline runs — a parse failure
		// there produces a raw.parse_error finding directly, since it
		// needs host.ParseInterfaces's line-precise error, not a schema
		// rule. Nothing further to check here structurally.

	case *BondCreateParams:
		if p.Mode == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "bond.create requires mode"))
		} else if !bondModeSet(op.Target.Kind)[p.Mode] {
			out = append(out, errorf(codeBondModeInvalid, ref, "bond mode %q is not a recognized mode", p.Mode))
		}
		if len(p.Slaves) == 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "bond.create requires at least one slave"))
		} else {
			schemaDuplicateStrings(p.Slaves, ref, codeDuplicateSlave, "slave %q listed twice", &out)
		}
		if op.Target.Kind == inventory.KindOVSBond && p.Bridge == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "ovs bond.create requires bridge"))
		}
		if p.LACPRate != "" && !validLACPRates[p.LACPRate] {
			out = append(out, errorf(codeLACPRateInvalid, ref, "lacpRate %q must be one of slow, fast", p.LACPRate))
		}
		if p.XmitHashPolicy != "" && !validXmitHashPolicies[p.XmitHashPolicy] {
			out = append(out, errorf(codeXmitHashInvalid, ref, "xmitHashPolicy %q is not a recognized policy", p.XmitHashPolicy))
		}
		if p.MIIMon < 0 {
			out = append(out, errorf(codeMIIMonInvalid, ref, "miimon %d must not be negative", p.MIIMon))
		}
		schemaMTU(op, p.MTU, ref, &out)

	case *BondUpdateParams:
		if p.Mode != nil && !bondModeSet(op.Target.Kind)[*p.Mode] {
			out = append(out, errorf(codeBondModeInvalid, ref, "bond mode %q is not a recognized mode", *p.Mode))
		}
		if p.Slaves != nil {
			schemaDuplicateStrings(*p.Slaves, ref, codeDuplicateSlave, "slave %q listed twice", &out)
		}
		if p.LACPRate != nil && *p.LACPRate != "" && !validLACPRates[*p.LACPRate] {
			out = append(out, errorf(codeLACPRateInvalid, ref, "lacpRate %q must be one of slow, fast", *p.LACPRate))
		}
		if p.XmitHashPolicy != nil && *p.XmitHashPolicy != "" && !validXmitHashPolicies[*p.XmitHashPolicy] {
			out = append(out, errorf(codeXmitHashInvalid, ref, "xmitHashPolicy %q is not a recognized policy", *p.XmitHashPolicy))
		}
		if p.MIIMon != nil && *p.MIIMon < 0 {
			out = append(out, errorf(codeMIIMonInvalid, ref, "miimon %d must not be negative", *p.MIIMon))
		}
		schemaMTUPtr(op, p.MTU, ref, &out)

	case *BondDeleteParams:
		// no params to validate.

	case *BridgeCreateParams:
		schemaDuplicateStrings(p.Ports, ref, codeDuplicatePort, "port %q listed twice", &out)
		schemaVidRanges(op, p.Vids, ref, &out)
		schemaAddresses(p.Addresses, ref, &out)
		schemaIP(p.Gateway, ref, &out)
		schemaMTU(op, p.MTU, ref, &out)

	case *BridgeUpdateParams:
		if p.Vids != nil {
			schemaVidRanges(op, *p.Vids, ref, &out)
		}
		if p.Addresses != nil {
			schemaAddresses(*p.Addresses, ref, &out)
		}
		if p.Gateway != nil {
			schemaIP(*p.Gateway, ref, &out)
		}
		schemaMTUPtr(op, p.MTU, ref, &out)

	case *BridgeDeleteParams:
		// no params to validate.

	case *BridgePortAddParams:
		if p.Port == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "bridge.port.add requires port"))
		}

	case *BridgePortRemoveParams:
		if p.Port == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "bridge.port.remove requires port"))
		}

	case *VlanCreateParams:
		if p.Parent == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "vlan.create requires parent"))
		}
		// A plain 802.1q sub-interface's vid is always meaningful (its
		// entire identity, per VlanName's "<parent>.<vid>" convention). An
		// OVS Int Port's vid is instead an optional access "tag" — 0 is a
		// legitimate "untagged/native, possibly trunk-only" port, so it is
		// only range-checked when non-zero (schemaVidRanges' sibling
		// checkVIDRangeAllowZero convention).
		if p.OVS {
			if p.Vid != 0 {
				if f := checkVIDRange(p.Vid, ref); f != nil {
					f.Fix = fixClampVID(op)
					out = append(out, *f)
				}
			}
			schemaVidRanges(op, p.Trunks, ref, &out)
		} else {
			if p.Vid < minVID || p.Vid > maxVID {
				f := errorf(codeVIDOutOfRange, ref, "vid %d out of range [%d,%d]", p.Vid, minVID, maxVID)
				f.Fix = fixClampVID(op)
				out = append(out, f)
			}
			if len(p.Trunks) > 0 {
				out = append(out, errorf(codeOVSTrunkNotAllowed, ref, "trunks is only valid for an OVS Int Port (ovs: true)"))
			}
		}
		schemaAddresses(p.Addresses, ref, &out)
		schemaIP(p.Gateway, ref, &out)
		schemaMTU(op, p.MTU, ref, &out)

	case *VlanUpdateParams:
		if p.Addresses != nil {
			schemaAddresses(*p.Addresses, ref, &out)
		}
		schemaMTUPtr(op, p.MTU, ref, &out)

	case *VlanDeleteParams:
		// no params to validate.

	case *SdnZoneCreateParams:
		schemaSDNName("sdn zone", op.Target.ID, ref, &out)
		if !validSdnZoneTypes[p.Type] {
			out = append(out, errorf(codeSDNZoneTypeInvalid, ref, "sdn zone type %q is not recognized", p.Type))
		}
		schemaMTU(op, p.MTU, ref, &out)

	case *SdnZoneUpdateParams:
		schemaMTUPtr(op, p.MTU, ref, &out)

	case *SdnZoneDeleteParams:
		// no params to validate.

	case *SdnVnetCreateParams:
		schemaSDNName("sdn vnet", op.Target.ID, ref, &out)
		if p.Zone == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "sdn.vnet.create requires zone"))
		}
		if p.Tag != 0 {
			f := checkVIDRange(p.Tag, ref)
			if f != nil {
				f.Fix = fixClampVID(op)
				out = append(out, *f)
			}
		}

	case *SdnVnetUpdateParams:
		if p.Tag != nil {
			f := checkVIDRangeAllowZero(*p.Tag, ref)
			if f != nil {
				f.Fix = fixClampVID(op)
				out = append(out, *f)
			}
		}

	case *SdnVnetDeleteParams:
		// no params to validate.

	case *SdnSubnetCreateParams:
		if p.Vnet == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "sdn.subnet.create requires vnet"))
		}
		if p.CIDR == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "sdn.subnet.create requires cidr"))
		} else if !validCIDR(p.CIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", p.CIDR))
		}
		schemaIP(p.Gateway, ref, &out)
		schemaGatewayInCIDR(op, p.Gateway, p.CIDR, ref, &out)
		for _, dr := range p.DHCPRanges {
			if !validDHCPRange(dr) {
				out = append(out, errorf(codeDHCPRangeInvalid, ref, "dhcp range %q is not a valid start-end pair", dr))
			}
		}

	case *SdnSubnetUpdateParams:
		if p.Gateway != nil {
			schemaIP(*p.Gateway, ref, &out)
			// An update op's params carry no CIDR field (immutable —
			// params_sdn.go's SdnSubnetUpdateParams doc comment); the
			// subnet's CIDR is always op.Target.ID instead (the subnet's
			// id *is* its CIDR, docs/data-model.md's SdnSubnet.ID doc
			// comment), so the same per-op check applies with no
			// snapshot lookup needed.
			schemaGatewayInCIDR(op, *p.Gateway, op.Target.ID, ref, &out)
		}
		if p.DHCPRanges != nil {
			for _, dr := range *p.DHCPRanges {
				if !validDHCPRange(dr) {
					out = append(out, errorf(codeDHCPRangeInvalid, ref, "dhcp range %q is not a valid start-end pair", dr))
				}
			}
		}

	case *SdnSubnetDeleteParams:
		// no params to validate.

	case *SdnFabricCreateParams:
		schemaSDNFabricID(op.Target.ID, ref, &out)
		if !validSdnFabricProtocols[p.Protocol] {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "sdn fabric protocol %q is not recognized", p.Protocol))
		} else {
			schemaSDNFabricProtocolFields(p.Protocol, map[string]bool{
				"csnpInterval":        p.CSNPInterval != 0,
				"helloInterval":       p.HelloInterval != 0,
				"routeFilter":         p.RouteFilter != "",
				"area":                p.Area != "",
				"redistribute":        len(p.Redistribute) > 0,
				"persistentKeepalive": p.PersistentKeepalive != 0,
			}, ref, &out)
		}
		if p.CSNPInterval != 0 && (p.CSNPInterval < 1 || p.CSNPInterval > 600) {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "csnpInterval %d out of range [1,600]", p.CSNPInterval))
		}
		if p.HelloInterval != 0 && (p.HelloInterval < 1 || p.HelloInterval > 600) {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "helloInterval %d out of range [1,600]", p.HelloInterval))
		}
		if p.PersistentKeepalive < 0 || p.PersistentKeepalive > 65535 {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "persistentKeepalive %d out of range [0,65535]", p.PersistentKeepalive))
		}

	case *SdnFabricUpdateParams:
		// Protocol is not part of this params type (immutable — see
		// params_sdn_fabric.go's SdnFabricUpdateParams doc comment), so the
		// per-protocol conditional check above cannot run here without
		// knowing the fabric's existing protocol, which schema class 1
		// (pure, per-op, no snapshot) does not have. Only the bare numeric
		// ranges are checked at this class; a field set for the wrong
		// protocol on update is a referential-class concern this card
		// deliberately does not add (see the task report).
		if p.CSNPInterval != nil && *p.CSNPInterval != 0 && (*p.CSNPInterval < 1 || *p.CSNPInterval > 600) {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "csnpInterval %d out of range [1,600]", *p.CSNPInterval))
		}
		if p.HelloInterval != nil && *p.HelloInterval != 0 && (*p.HelloInterval < 1 || *p.HelloInterval > 600) {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "helloInterval %d out of range [1,600]", *p.HelloInterval))
		}
		if p.PersistentKeepalive != nil && (*p.PersistentKeepalive < 0 || *p.PersistentKeepalive > 65535) {
			out = append(out, errorf(codeSDNFabricProtocolInvalid, ref, "persistentKeepalive %d out of range [0,65535]", *p.PersistentKeepalive))
		}

	case *SdnFabricDeleteParams:
		// no params to validate.

	case *SdnControllerCreateParams:
		schemaSDNControllerID(op.Target.ID, ref, &out)
		if !validSdnControllerTypes[p.Type] {
			out = append(out, errorf(codeSDNControllerTypeInvalid, ref, "sdn controller type %q is not recognized", p.Type))
		} else {
			schemaSDNControllerTypeFields(p.Type, map[string]bool{
				"asn":                     p.ASN != 0,
				"bgpMode":                 p.BgpMode != "",
				"bgpMultipathAsPathRelax": p.BgpMultipathAsPathRelax,
				"ebgp":                    p.Ebgp,
				"ebgpMultihop":            p.EbgpMultihop != 0,
				"peers":                   len(p.Peers) > 0,
				"fabric":                  p.Fabric != "",
				"peerGroupName":           p.PeerGroupName != "",
				"routeMapIn":              p.RouteMapIn != "",
				"routeMapOut":             p.RouteMapOut != "",
				"isisDomain":              p.IsisDomain != "",
				"isisIfaces":              len(p.IsisIfaces) > 0,
				"isisNet":                 p.IsisNet != "",
			}, ref, &out)
		}
		if p.BgpMode != "" && p.BgpMode != "auto" && p.BgpMode != "external" && p.BgpMode != "internal" {
			out = append(out, errorf(codeSDNControllerTypeInvalid, ref, "bgpMode %q must be one of auto, external, internal", p.BgpMode))
		}
		if p.ASN < 0 || p.ASN > 4294967295 {
			out = append(out, errorf(codeSDNControllerTypeInvalid, ref, "asn %d out of range [0,4294967295]", p.ASN))
		}

	case *SdnControllerUpdateParams:
		// Type is not part of this params type (immutable — see
		// params_sdn_controller.go's SdnControllerUpdateParams doc comment),
		// so the per-type conditional check above cannot run here without
		// knowing the controller's existing type, which schema class 1
		// (pure, per-op, no snapshot) does not have — the same deliberate
		// gap SdnFabricUpdateParams' case leaves for its own protocol.
		if p.BgpMode != nil && *p.BgpMode != "" && *p.BgpMode != "auto" && *p.BgpMode != "external" && *p.BgpMode != "internal" {
			out = append(out, errorf(codeSDNControllerTypeInvalid, ref, "bgpMode %q must be one of auto, external, internal", *p.BgpMode))
		}
		if p.ASN != nil && (*p.ASN < 0 || *p.ASN > 4294967295) {
			out = append(out, errorf(codeSDNControllerTypeInvalid, ref, "asn %d out of range [0,4294967295]", *p.ASN))
		}

	case *SdnControllerDeleteParams:
		// no params to validate.

	case *SdnDnsZoneCreateParams:
		if !validDNSName(op.Target.ID) {
			out = append(out, errorf(codeDNSNameInvalid, ref, "dns zone %q is not a valid domain name", op.Target.ID))
		}
		if p.TTL < 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "dns zone ttl %d must not be negative", p.TTL))
		}

	case *SdnDnsZoneUpdateParams:
		if p.TTL != nil && *p.TTL < 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "dns zone ttl %d must not be negative", *p.TTL))
		}

	case *SdnDnsZoneDeleteParams:
		// no params to validate.

	case *SdnDnsRecordCreateParams:
		if p.Zone == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "sdn.dns.record.create requires zone"))
		}
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "sdn.dns.record.create requires name"))
		} else if !validDNSName(p.Name) {
			out = append(out, errorf(codeDNSNameInvalid, ref, "dns record name %q is not a valid hostname/FQDN", p.Name))
		}
		schemaDNSRecordType(p.Type, ref, &out)
		schemaDNSRecordValue(p.Type, p.Value, ref, &out)
		if p.TTL < 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "dns record ttl %d must not be negative", p.TTL))
		}

	case *SdnDnsRecordUpdateParams:
		// Zone/Name/Type are identity (op.Target.ID's "<zone>/<name>/<type>"
		// composite); only value/ttl are editable. The record type is parsed
		// back from the target so the value is checked against it.
		if p.Value != nil {
			schemaDNSRecordValue(dnsTypeFromRecordID(op.Target.ID), *p.Value, ref, &out)
		}
		if p.TTL != nil && *p.TTL < 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "dns record ttl %d must not be negative", *p.TTL))
		}

	case *SdnDnsRecordDeleteParams:
		// no params to validate.

	case *SdnApplyParams:
		// no params to validate.

	case *GuestNicUpdateParams:
		if p.BridgeOrVnet != nil && *p.BridgeOrVnet == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "guest.nic.update's bridgeOrVnet must not be empty when set"))
		}
		if p.Vid != nil && (*p.Vid < 0 || *p.Vid > maxVID) {
			out = append(out, errorf(codeVIDOutOfRange, ref, "vid %d out of range [0,%d]", *p.Vid, maxVID))
		}
		if p.RateMbps != nil && *p.RateMbps < 0 {
			out = append(out, errorf(codeRateInvalid, ref, "rateMbps %d must not be negative", *p.RateMbps))
		}

	case *FwRuleCreateParams:
		schemaFwDirectionForTarget(p.Direction, op.Target, ref, &out)
		schemaFwActionForDirection(p.Direction, p.Action, ref, &out)
		schemaFwLog(p.Log, ref, &out)
		schemaFwMacro(p.Macro, ref, &out)
		if p.Pos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "pos %d must not be negative", p.Pos))
		}

	case *FwRuleUpdateParams:
		direction := ""
		if p.Direction != nil {
			schemaFwDirectionForTarget(*p.Direction, op.Target, ref, &out)
			direction = *p.Direction
		}
		if p.Action != nil {
			schemaFwActionForDirection(direction, *p.Action, ref, &out)
		}
		if p.Log != nil {
			schemaFwLog(*p.Log, ref, &out)
		}
		if p.Macro != nil {
			schemaFwMacro(*p.Macro, ref, &out)
		}
		if p.Pos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "pos %d must not be negative", p.Pos))
		}

	case *FwRuleDeleteParams:
		if p.Pos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "pos %d must not be negative", p.Pos))
		}

	case *FwRuleMoveParams:
		if p.FromPos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "fromPos %d must not be negative", p.FromPos))
		}
		if p.ToPos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "toPos %d must not be negative", p.ToPos))
		}

	case *FwOptionsUpdateParams:
		schemaFwOptionsForScope(p, op.Target, ref, &out)

	case *FwAliasCreateParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.alias.create requires name"))
		}
		if p.CIDR == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.alias.create requires cidr"))
		} else if !validCIDR(p.CIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", p.CIDR))
		}

	case *FwAliasUpdateParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.alias.update requires name"))
		}
		if p.CIDR != nil && !validCIDR(*p.CIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", *p.CIDR))
		}

	case *FwAliasDeleteParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.alias.delete requires name"))
		}

	case *FwIpsetCreateParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.ipset.create requires name"))
		}
		for _, c := range p.CIDRs {
			if !validCIDR(c) {
				out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", c))
			}
		}

	case *FwIpsetUpdateParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.ipset.update requires name"))
		}
		if p.CIDRs != nil {
			for _, c := range *p.CIDRs {
				if !validCIDR(c) {
					out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", c))
				}
			}
		}

	case *FwIpsetDeleteParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.ipset.delete requires name"))
		}

	case *FwGroupCreateParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.group.create requires name"))
		}
		for _, rule := range p.Rules {
			schemaFwDirection(rule.Direction, ref, &out)
			schemaFwAction(rule.Action, ref, &out)
		}

	case *FwGroupUpdateParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.group.update requires name"))
		}
		if p.Rules != nil {
			for _, rule := range *p.Rules {
				schemaFwDirection(rule.Direction, ref, &out)
				schemaFwAction(rule.Action, ref, &out)
			}
		}

	case *FwGroupDeleteParams:
		if p.Name == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "fw.group.delete requires name"))
		}

	case *VFProvisionParams:
		schemaVFProvision(p, ref, &out)

	case *IpamAllocCreateParams:
		if p.CIDR == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "ipam.alloc.create requires cidr"))
		} else if !validCIDR(p.CIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", p.CIDR))
		}
		if p.MAC != "" && !validMAC(p.MAC) {
			out = append(out, errorf(codeMACInvalid, ref, "mac %q is not a valid MAC address", p.MAC))
		}

	case *IpamAllocDeleteParams:
		if p.CIDR == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "ipam.alloc.delete requires cidr"))
		} else if !validCIDR(p.CIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "cidr %q is not a valid CIDR", p.CIDR))
		}

	case *QosShapeCreateParams:
		if p.Bridge == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "qos.shape.create requires bridge"))
		}
		if p.MatchCIDR != "" && !validCIDR(p.MatchCIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "matchCidr %q is not a valid CIDR", p.MatchCIDR))
		}
		schemaQosVlan(p.MatchVlan, ref, &out)
		schemaQosRate(p.RateMbit, p.CeilMbit, ref, &out)

	case *QosShapeUpdateParams:
		if p.MatchCIDR != nil && *p.MatchCIDR != "" && !validCIDR(*p.MatchCIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "matchCidr %q is not a valid CIDR", *p.MatchCIDR))
		}
		schemaQosVlan(p.MatchVlan, ref, &out)
		if p.RateMbit != nil {
			schemaQosRate(*p.RateMbit, p.CeilMbit, ref, &out)
		} else if p.CeilMbit != nil && *p.CeilMbit <= 0 {
			out = append(out, errorf(codeQosRateInvalid, ref, "ceilMbit %d must be positive", *p.CeilMbit))
		}

	case *QosShapeDeleteParams:
		// no params to validate.

	case *WgTunnelCreateParams:
		if strings.TrimSpace(p.IfName) == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "wg.tunnel.create requires ifName"))
		} else if len(p.IfName) > maxIfaceNameLen || !ifaceNameRe.MatchString(p.IfName) {
			out = append(out, errorf(codeIfaceNameInvalid, ref, "interface name %q is not valid — use letters, digits, and .-_ only, at most %d characters", p.IfName, maxIfaceNameLen))
		}
		schemaWgPort(p.ListenPort, ref, &out)
		schemaMTU(op, p.MTU, ref, &out)
		schemaAddresses(p.Addresses, ref, &out)

	case *WgTunnelUpdateParams:
		if p.ListenPort != nil {
			schemaWgPort(*p.ListenPort, ref, &out)
		}
		schemaMTUPtr(op, p.MTU, ref, &out)
		if p.Addresses != nil {
			schemaAddresses(*p.Addresses, ref, &out)
		}

	case *WgTunnelDeleteParams:
		// Nothing structural to check — target identity is validated at decode.

	case *WgPeerAddParams:
		schemaWgKey(p.PublicKey, ref, &out)
		schemaAddresses(p.AllowedIPs, ref, &out)

	case *WgPeerRemoveParams:
		schemaWgKey(p.PublicKey, ref, &out)
	case *NatMasqueradeCreateParams:
		schemaEdgeRuleID(op.Target.ID, ref, &out)
		if p.Iface == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "nat.masquerade.create requires iface"))
		}
		if p.SourceCIDR == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "nat.masquerade.create requires sourceCidr"))
		} else if !validCIDR(p.SourceCIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "sourceCidr %q is not a valid CIDR", p.SourceCIDR))
		}

	case *NatMasqueradeDeleteParams:
		// no params to validate.

	case *NatPortForwardCreateParams:
		schemaEdgeRuleID(op.Target.ID, ref, &out)
		if p.Iface == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "nat.portforward.create requires iface"))
		}
		schemaNatProto(p.Proto, ref, &out)
		schemaIP(p.IntIP, ref, &out)
		if p.IntIP == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "nat.portforward.create requires intIp"))
		}
		schemaPortNumber(p.ExtPort, ref, &out)
		schemaPortNumber(p.IntPort, ref, &out)

	case *NatPortForwardUpdateParams:
		if p.Proto != nil {
			schemaNatProto(*p.Proto, ref, &out)
		}
		if p.IntIP != nil && *p.IntIP != "" {
			schemaIP(*p.IntIP, ref, &out)
		}
		if p.ExtPort != nil {
			schemaPortNumber(*p.ExtPort, ref, &out)
		}
		if p.IntPort != nil {
			schemaPortNumber(*p.IntPort, ref, &out)
		}

	case *NatPortForwardDeleteParams:
		// no params to validate.

	case *RouteStaticCreateParams:
		schemaEdgeRuleID(op.Target.ID, ref, &out)
		if p.Iface == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "route.static.create requires iface"))
		}
		if p.DestCIDR == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "route.static.create requires destCidr"))
		} else if !validCIDR(p.DestCIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "destCidr %q is not a valid CIDR", p.DestCIDR))
		}
		if p.Gateway == "" {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "route.static.create requires gateway"))
		} else {
			schemaIP(p.Gateway, ref, &out)
		}
		if p.Metric < 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "metric %d must not be negative", p.Metric))
		}

	case *RouteStaticUpdateParams:
		if p.DestCIDR != nil && *p.DestCIDR != "" && !validCIDR(*p.DestCIDR) {
			out = append(out, errorf(codeCIDRInvalid, ref, "destCidr %q is not a valid CIDR", *p.DestCIDR))
		}
		if p.Gateway != nil && *p.Gateway != "" {
			schemaIP(*p.Gateway, ref, &out)
		}
		if p.Metric != nil && *p.Metric < 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "metric %d must not be negative", *p.Metric))
		}

	case *RouteStaticDeleteParams:
		// no params to validate.
	}

	return out
}

// schemaQosRate is T-1505 acceptance criterion 2: rateMbit must be
// positive, and ceilMbit (when set) must be >= rateMbit — real tc/HTB
// rejects a class whose ceil is below its own guaranteed rate.
func schemaQosRate(rateMbit int, ceilMbit *int, ref string, out *[]Finding) {
	if rateMbit <= 0 {
		*out = append(*out, errorf(codeQosRateInvalid, ref, "rateMbit %d must be positive", rateMbit))
	}
	if ceilMbit != nil {
		if *ceilMbit <= 0 {
			*out = append(*out, errorf(codeQosRateInvalid, ref, "ceilMbit %d must be positive", *ceilMbit))
		} else if *ceilMbit < rateMbit {
			*out = append(*out, errorf(codeQosRateInvalid, ref, "ceilMbit %d must be >= rateMbit %d", *ceilMbit, rateMbit))
		}
	}
}

// schemaQosVlan flags a qos.shape.* matchVlan outside the 802.1Q VID range
// [1,4094] (0/4095 are reserved) — a nil vlan (no VLAN match) is always
// valid.
func schemaQosVlan(vlan *int, ref string, out *[]Finding) {
	if vlan == nil {
		return
	}
	if *vlan < minVID || *vlan > maxVID {
		*out = append(*out, errorf(codeQosVlanOutOfRange, ref, "matchVlan %d out of range [%d,%d]", *vlan, minVID, maxVID))
	}
}

// schemaWgKey flags a WireGuard peer public key that isn't a valid base64
// 32-byte Curve25519 key (T-1401 schema class).
func schemaWgKey(key, ref string, out *[]Finding) {
	if strings.TrimSpace(key) == "" {
		*out = append(*out, errorf(codeRequiredFieldMissing, ref, "a WireGuard peer requires a publicKey"))
		return
	}
	if _, err := wireguard.DecodeKey(key); err != nil {
		*out = append(*out, errorf(codeWgKeyInvalid, ref, "publicKey %q is not a valid base64 WireGuard key", key))
	}
}

// schemaWgPort flags a WireGuard listen port outside 1–65535 (0 == "not set",
// skipped — WireGuard picks a random port when none is configured).
func schemaWgPort(port int, ref string, out *[]Finding) {
	if port == 0 {
		return
	}
	if port < 1 || port > 65535 {
		*out = append(*out, errorf(codeWgPortInvalid, ref, "listenPort %d is out of range (1–65535)", port))
	}
}

// schemaVFProvision validates a vf.provision op's Count/VFs shape (T-1506):
// exactly one of Count/VFs set, Count positive when set, no MacAddr with
// Count > 1 (see VFProvisionParams' doc comment), every VFSpec.ID
// non-negative and not repeated, and every VLAN/MacAddr (top-level and
// per-VFSpec) individually well-formed.
func schemaVFProvision(p *VFProvisionParams, ref string, out *[]Finding) {
	switch {
	case p.Count > 0 && len(p.VFs) > 0:
		*out = append(*out, errorf(codeVFPlanInvalid, ref, "vf.provision must set exactly one of count or vfs, not both"))
	case p.Count <= 0 && len(p.VFs) == 0:
		*out = append(*out, errorf(codeVFPlanInvalid, ref, "vf.provision requires count or vfs"))
	case p.Count < 0:
		*out = append(*out, errorf(codeVFPlanInvalid, ref, "count %d must be positive", p.Count))
	}
	if p.Count > 1 && p.MacAddr != "" {
		*out = append(*out, errorf(codeVFPlanInvalid, ref, "macAddr cannot be set with count > 1 (would duplicate it across every VF)"))
	}
	if f := checkVIDRangeAllowZero(p.VLAN, ref); f != nil {
		*out = append(*out, *f)
	}
	if p.MacAddr != "" && !validMAC(p.MacAddr) {
		*out = append(*out, errorf(codeMACInvalid, ref, "macAddr %q is not a valid MAC address", p.MacAddr))
	}

	seenIDs := map[int]bool{}
	for _, v := range p.VFs {
		if v.ID < 0 {
			*out = append(*out, errorf(codeVFPlanInvalid, ref, "vf id %d must not be negative", v.ID))
		} else if seenIDs[v.ID] {
			*out = append(*out, errorf(codeVFPlanInvalid, ref, "vf id %d listed twice", v.ID))
		}
		seenIDs[v.ID] = true
		if f := checkVIDRangeAllowZero(v.VLAN, ref); f != nil {
			*out = append(*out, *f)
		}
		if v.MacAddr != "" && !validMAC(v.MacAddr) {
			*out = append(*out, errorf(codeMACInvalid, ref, "vf %d macAddr %q is not a valid MAC address", v.ID, v.MacAddr))
		}
	}
}

func schemaFwDirection(v, ref string, out *[]Finding) {
	if !validFwDirections[v] {
		*out = append(*out, errorf(codeFwDirectionInvalid, ref, "direction %q must be one of forward, group, in, out", v))
	}
}

// schemaFwDirectionForTarget is schemaFwDirection plus a scope-aware guard
// (T-3103): "forward" is hardware-captured at cluster/node/vnet scope but
// not guest scope (see validFwDirections' doc comment for why that is not
// treated as unconditionally valid). Only fw.rule.create/update call this —
// fw.group.create/update's member rules use the unscoped schemaFwDirection
// directly, since a security group is not itself scope-bound (it is
// referenced from whichever scope's rule list names it, and could in
// principle be referenced from more than one scope at once).
func schemaFwDirectionForTarget(v string, target inventory.Ref, ref string, out *[]Finding) {
	schemaFwDirection(v, ref, out)
	if v == "forward" && fwScopeOfRef(target) == inventory.FwScopeGuest {
		*out = append(*out, errorf(codeFwScopeInvalid, ref,
			"direction \"forward\" is not valid for a guest-scope firewall rule — real pve-firewall has no forward chain on a guest's own network interface (only cluster, node, and vnet scope do)"))
	}
}

func schemaFwAction(v, ref string, out *[]Finding) {
	if !validFwActions[v] {
		*out = append(*out, errorf(codeFwActionInvalid, ref, "action %q must be one of ACCEPT, DROP, REJECT", v))
	}
}

// schemaFwActionForDirection validates a rule's action field, special-
// casing direction == "group": per T-501's documented convention
// (internal/fw/resolve.go's appendRule doc comment, matching real PVE's
// own "type":"group" rule shape), a group-reference rule's Action field
// holds the referenced security group's *name*, not one of ACCEPT/DROP/
// REJECT — so the enum check doesn't apply; only non-empty is required
// here (does the named group actually exist is a referential-class
// concern, not schema's).
func schemaFwActionForDirection(direction, action, ref string, out *[]Finding) {
	if direction == "group" {
		if action == "" {
			*out = append(*out, errorf(codeFwActionInvalid, ref, "a group-reference rule's action must name the security group"))
		}
		return
	}
	schemaFwAction(action, ref, out)
}

// schemaFwMacro validates that macro, when set, names a macro the built-in
// catalog (internal/fw.KnownMacros) actually recognizes — the "macro
// existence" validator the T-502 task card calls for. An empty macro is
// always valid (most rules don't use one; proto/ports are set directly
// instead).
func schemaFwMacro(macro, ref string, out *[]Finding) {
	if macro == "" {
		return
	}
	if _, ok := fw.MacroExpansion(macro); !ok {
		*out = append(*out, errorf(codeFwMacroUnknown, ref, "macro %q is not a known firewall macro", macro))
	}
}

func schemaFwLog(v, ref string, out *[]Finding) {
	if !validFwLogLevels[v] {
		*out = append(*out, errorf(codeFwLogInvalid, ref, "log %q is not a recognized log level", v))
	}
}

// schemaFwOptionsForScope validates an fw.options.update op's fields
// against both their own enums and the scope its target names (T-3103):
//
//   - defaultIn/defaultOut: valid at cluster/node/guest scope (validFwActions);
//     rejected outright at vnet scope — real PVE's vnet options endpoint has
//     no policy_in/policy_out fields at all (hardware-captured,
//     planning/reports/evidence/pve-9.2.4-sdn-schema.txt's "/cluster/sdn/
//     vnets/labnet/firewall/options" usage: only enable/policy_forward/
//     log_level_forward).
//   - defaultForward: valid at cluster/node/vnet scope (validFwForwardPolicies,
//     ACCEPT|DROP only — no REJECT); rejected at guest scope (no forward
//     chain there).
//   - logLevelForward: only hardware-confirmed at vnet scope
//     (validFwLogLevelsForward); rejected everywhere else rather than
//     guessed at, per this task's "an honest rejection beats a silent wrong
//     write" discipline.
func schemaFwOptionsForScope(p *FwOptionsUpdateParams, target inventory.Ref, ref string, out *[]Finding) {
	scope := fwScopeOfRef(target)

	if p.DefaultIn != nil {
		if scope == inventory.FwScopeVNet {
			*out = append(*out, errorf(codeFwScopeInvalid, ref, "defaultIn is not valid for a vnet-scope firewall ruleset — real PVE's vnet firewall options have no policy_in"))
		} else if !validFwActions[*p.DefaultIn] {
			*out = append(*out, errorf(codeFwPolicyInvalid, ref, "defaultIn %q must be one of ACCEPT, DROP, REJECT", *p.DefaultIn))
		}
	}
	if p.DefaultOut != nil {
		if scope == inventory.FwScopeVNet {
			*out = append(*out, errorf(codeFwScopeInvalid, ref, "defaultOut is not valid for a vnet-scope firewall ruleset — real PVE's vnet firewall options have no policy_out"))
		} else if !validFwActions[*p.DefaultOut] {
			*out = append(*out, errorf(codeFwPolicyInvalid, ref, "defaultOut %q must be one of ACCEPT, DROP, REJECT", *p.DefaultOut))
		}
	}
	if p.DefaultForward != nil {
		if scope == inventory.FwScopeGuest {
			*out = append(*out, errorf(codeFwScopeInvalid, ref, "defaultForward is not valid for a guest-scope firewall ruleset — real pve-firewall has no forward chain on a guest's own network interface"))
		} else if !validFwForwardPolicies[*p.DefaultForward] {
			*out = append(*out, errorf(codeFwPolicyInvalid, ref, "defaultForward %q must be one of ACCEPT, DROP", *p.DefaultForward))
		}
	}
	if p.LogLevelForward != nil {
		if scope != inventory.FwScopeVNet {
			*out = append(*out, errorf(codeFwScopeInvalid, ref, "logLevelForward is only confirmed valid for a vnet-scope firewall ruleset"))
		} else if !validFwLogLevelsForward[*p.LogLevelForward] {
			*out = append(*out, errorf(codeFwLogInvalid, ref, "logLevelForward %q is not a recognized log level", *p.LogLevelForward))
		}
	}
}

// schemaMTU validates a non-pointer, omitempty MTU field (0 means "not
// provided" per JSON encoding, so it is skipped rather than flagged).
func schemaMTU(op Op, mtu int, ref string, out *[]Finding) {
	if mtu == 0 {
		return
	}
	if mtu < minMTU || mtu > maxMTU {
		f := errorf(codeMTUOutOfRange, ref, "mtu %d out of range [%d,%d]", mtu, minMTU, maxMTU)
		f.Fix = fixClampMTU(op)
		*out = append(*out, f)
	}
}

// schemaMTUPtr validates a pointer MTU field: nil means "leave unchanged",
// any present value (including 0) is validated.
func schemaMTUPtr(op Op, mtu *int, ref string, out *[]Finding) {
	if mtu == nil {
		return
	}
	if *mtu < minMTU || *mtu > maxMTU {
		f := errorf(codeMTUOutOfRange, ref, "mtu %d out of range [%d,%d]", *mtu, minMTU, maxMTU)
		f.Fix = fixClampMTU(op)
		*out = append(*out, f)
	}
}

// checkVIDRange validates a required, always-meaningful VID (no "0 means
// unset" convention), returning a Finding pointer (nil if valid).
func checkVIDRange(vid int, ref string) *Finding {
	if vid < minVID || vid > maxVID {
		f := errorf(codeVIDOutOfRange, ref, "vid %d out of range [%d,%d]", vid, minVID, maxVID)
		return &f
	}
	return nil
}

// checkVIDRangeAllowZero validates a VID field where an explicit 0 is a
// legitimate value (untagged), used for pointer fields where "set to zero"
// is distinguishable from "not set" (the pointer itself being nil).
func checkVIDRangeAllowZero(vid int, ref string) *Finding {
	if vid == 0 {
		return nil
	}
	return checkVIDRange(vid, ref)
}

// schemaVidRanges validates a bridge's VLAN trunk ranges: each range's
// bounds must be in [minVID,maxVID] and Low <= High. This does not check
// for pairwise overlap between ranges — that is validator class 2's job
// (referential.go's schemaVidRanges-adjacent checkVIDOverlap), since the
// spec classifies "VID overlaps on a trunk" as a referential check.
func schemaVidRanges(op Op, vids []VidRange, ref string, out *[]Finding) {
	for _, v := range vids {
		if v.Low > v.High {
			*out = append(*out, errorf(codeVIDRangeInvalid, ref, "vid range %d-%d has low > high", v.Low, v.High))
			continue
		}
		if v.Low < minVID || v.Low > maxVID || v.High < minVID || v.High > maxVID {
			f := errorf(codeVIDOutOfRange, ref, "vid range %d-%d out of bounds [%d,%d]", v.Low, v.High, minVID, maxVID)
			f.Fix = fixClampBridgeVids(op)
			*out = append(*out, f)
		}
	}
}

func schemaAddresses(addrs []string, ref string, out *[]Finding) {
	for _, a := range addrs {
		if !validCIDR(a) {
			*out = append(*out, errorf(codeCIDRInvalid, ref, "address %q is not a valid CIDR", a))
		}
	}
}

func schemaAddressesPtr(addrs *[]string, ref string, out *[]Finding) {
	if addrs == nil {
		return
	}
	schemaAddresses(*addrs, ref, out)
}

func schemaIP(ip, ref string, out *[]Finding) {
	if !validIP(ip) {
		*out = append(*out, errorf(codeIPInvalid, ref, "gateway %q is not a valid IP address", ip))
	}
}

func schemaIPPtr(ip *string, ref string, out *[]Finding) {
	if ip == nil {
		return
	}
	schemaIP(*ip, ref, out)
}

// schemaGatewayInCIDR is T-701 acceptance criterion 2's
// codeGatewayNotInSubnet check: a gateway PVE would otherwise accept as a
// syntactically valid IP (schemaIP's job) must actually fall inside the
// subnet's own CIDR — real PVE's SubnetPlugin rejects a gateway outside
// the CIDR at subnet stage time (T-701 root-cause analysis §4). Skipped
// when gateway is empty ("no gateway" is a separate, SNAT-gated concern —
// see validate_sdn.go's snatRequiresGatewayFindings) or when either
// gateway or cidr fails to parse (codeIPInvalid/codeCIDRInvalid already
// flag those independently; layering a second, confusing error on top of
// an already-invalid value would be noise).
func schemaGatewayInCIDR(op Op, gateway, cidr, ref string, out *[]Finding) {
	if gateway == "" || cidr == "" {
		return
	}
	ip := net.ParseIP(gateway)
	if ip == nil {
		return
	}
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return
	}
	if !ipnet.Contains(ip) {
		f := errorf(codeGatewayNotInSubnet, ref, "gateway %q is not within subnet %s", gateway, cidr)
		f.Fix = fixSetSubnetGateway(op, cidr)
		*out = append(*out, f)
	}
}

// schemaDuplicateStrings flags any value repeated within ss.
func schemaDuplicateStrings(ss []string, ref, code, format string, out *[]Finding) {
	seen := map[string]bool{}
	for _, s := range ss {
		if seen[s] {
			*out = append(*out, errorf(code, ref, format, s))
			continue
		}
		seen[s] = true
	}
}

// dnsRecordTypes is the accepted SDN DNS record-type set (T-1204), matching
// internal/pvemock's own accepted set. Real PowerDNS supports many more;
// these are the ones vnprox's DNS management surface exposes.
var dnsRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "PTR": true, "CNAME": true, "TXT": true,
}

// dnsLabelRe validates one hostname label / FQDN name (T-1204): letters,
// digits, hyphen, underscore, dots between labels, optional trailing dot, and
// a leading "@" (zone apex) or "*" (wildcard) as the first character. A
// deliberately permissive superset flagged for hardware validation, not a
// strict RFC-1035 check (see needs-hardware-validation.md).
var dnsLabelRe = regexp.MustCompile(`^(\*|@|[A-Za-z0-9_])[A-Za-z0-9_.-]*\.?$`)

// validDNSName reports whether s is an acceptable DNS record name / zone
// domain.
func validDNSName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	return dnsLabelRe.MatchString(s)
}

// dnsTypeFromRecordID recovers the record type from a sdn-dns-record Ref.ID's
// "<zone>/<name>/<type>" composite (the last "/"-delimited segment), or "" if
// malformed — an update op's params carry no type field of their own.
func dnsTypeFromRecordID(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[i+1:]
	}
	return ""
}

// schemaDNSRecordType flags a record whose type is outside the accepted set.
func schemaDNSRecordType(typ, ref string, out *[]Finding) {
	if typ == "" {
		*out = append(*out, errorf(codeRequiredFieldMissing, ref, "sdn.dns.record.create requires type"))
		return
	}
	if !dnsRecordTypes[typ] {
		*out = append(*out, errorf(codeDNSRecordTypeInvalid, ref, "dns record type %q is not one of A, AAAA, PTR, CNAME, TXT", typ))
	}
}

// schemaDNSRecordValue flags a record whose value doesn't match its type: an
// A record must carry an IPv4 address, an AAAA an IPv6, a PTR any IP. CNAME/
// TXT values are free-form (only non-empty is required, checked by the
// caller). An unknown type is not re-flagged here (schemaDNSRecordType owns
// that).
func schemaDNSRecordValue(typ, value, ref string, out *[]Finding) {
	if value == "" {
		*out = append(*out, errorf(codeRequiredFieldMissing, ref, "sdn.dns.record requires value"))
		return
	}
	switch typ {
	case "A":
		if ip := net.ParseIP(value); ip == nil || ip.To4() == nil {
			*out = append(*out, errorf(codeDNSRecordValueInvalid, ref, "A record value %q is not a valid IPv4 address", value))
		}
	case "AAAA":
		if ip := net.ParseIP(value); ip == nil || ip.To4() != nil {
			*out = append(*out, errorf(codeDNSRecordValueInvalid, ref, "AAAA record value %q is not a valid IPv6 address", value))
		}
	case "PTR":
		if ip := net.ParseIP(value); ip == nil {
			*out = append(*out, errorf(codeDNSRecordValueInvalid, ref, "PTR record value %q is not a valid IP address", value))
		}
	}
}
