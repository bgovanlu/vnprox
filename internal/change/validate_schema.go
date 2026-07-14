package change

import (
	"net"
	"regexp"
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

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

var validSdnZoneTypes = map[string]bool{"simple": true, "vlan": true, "qinq": true, "vxlan": true, "evpn": true}

// validFwDirections includes "group" alongside the real traffic
// directions "in"/"out": a rule row whose Direction is "group" is not a
// traffic-direction rule at all but a security-group reference (T-501's
// documented convention, matching real PVE's own "type":"group" rule
// shape — see internal/fw/resolve.go's appendRule doc comment), so it must
// pass this same-field schema check too.
var validFwDirections = map[string]bool{"in": true, "out": true, "group": true}

var validFwActions = map[string]bool{"ACCEPT": true, "DROP": true, "REJECT": true}

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
		schemaFwDirection(p.Direction, ref, &out)
		schemaFwActionForDirection(p.Direction, p.Action, ref, &out)
		schemaFwLog(p.Log, ref, &out)
		schemaFwMacro(p.Macro, ref, &out)
		if p.Pos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "pos %d must not be negative", p.Pos))
		}

	case *FwRuleUpdateParams:
		direction := ""
		if p.Direction != nil {
			schemaFwDirection(*p.Direction, ref, &out)
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
		if p.DefaultIn != nil && !validFwActions[*p.DefaultIn] {
			out = append(out, errorf(codeFwPolicyInvalid, ref, "defaultIn %q must be one of ACCEPT, DROP, REJECT", *p.DefaultIn))
		}
		if p.DefaultOut != nil && !validFwActions[*p.DefaultOut] {
			out = append(out, errorf(codeFwPolicyInvalid, ref, "defaultOut %q must be one of ACCEPT, DROP, REJECT", *p.DefaultOut))
		}

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
	}

	return out
}

func schemaFwDirection(v, ref string, out *[]Finding) {
	if !validFwDirections[v] {
		*out = append(*out, errorf(codeFwDirectionInvalid, ref, "direction %q must be one of in, out", v))
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
