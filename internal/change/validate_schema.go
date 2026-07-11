package change

import (
	"net"
	"strings"
)

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

var validLACPRates = map[string]bool{"slow": true, "fast": true}

var validXmitHashPolicies = map[string]bool{
	"layer2": true, "layer2+3": true, "layer3+4": true, "encap2+3": true, "encap3+4": true,
}

var validSdnZoneTypes = map[string]bool{"simple": true, "vlan": true, "qinq": true, "vxlan": true, "evpn": true}

var validFwDirections = map[string]bool{"in": true, "out": true}

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
		} else if !validBondModes[p.Mode] {
			out = append(out, errorf(codeBondModeInvalid, ref, "bond mode %q is not a recognized mode", p.Mode))
		}
		if len(p.Slaves) == 0 {
			out = append(out, errorf(codeRequiredFieldMissing, ref, "bond.create requires at least one slave"))
		} else {
			schemaDuplicateStrings(p.Slaves, ref, codeDuplicateSlave, "slave %q listed twice", &out)
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
		if p.Mode != nil && !validBondModes[*p.Mode] {
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
		if p.Vid < minVID || p.Vid > maxVID {
			f := errorf(codeVIDOutOfRange, ref, "vid %d out of range [%d,%d]", p.Vid, minVID, maxVID)
			f.Fix = fixClampVID(op)
			out = append(out, f)
		}
		schemaAddresses(p.Addresses, ref, &out)
		schemaMTU(op, p.MTU, ref, &out)

	case *VlanUpdateParams:
		if p.Addresses != nil {
			schemaAddresses(*p.Addresses, ref, &out)
		}
		schemaMTUPtr(op, p.MTU, ref, &out)

	case *VlanDeleteParams:
		// no params to validate.

	case *SdnZoneCreateParams:
		if !validSdnZoneTypes[p.Type] {
			out = append(out, errorf(codeSDNZoneTypeInvalid, ref, "sdn zone type %q is not recognized", p.Type))
		}
		schemaMTU(op, p.MTU, ref, &out)

	case *SdnZoneUpdateParams:
		schemaMTUPtr(op, p.MTU, ref, &out)

	case *SdnZoneDeleteParams:
		// no params to validate.

	case *SdnVnetCreateParams:
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
		for _, dr := range p.DHCPRanges {
			if !validDHCPRange(dr) {
				out = append(out, errorf(codeDHCPRangeInvalid, ref, "dhcp range %q is not a valid start-end pair", dr))
			}
		}

	case *SdnSubnetUpdateParams:
		if p.Gateway != nil {
			schemaIP(*p.Gateway, ref, &out)
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
		schemaFwAction(p.Action, ref, &out)
		schemaFwLog(p.Log, ref, &out)
		if p.Pos < 0 {
			out = append(out, errorf(codeFwPosInvalid, ref, "pos %d must not be negative", p.Pos))
		}

	case *FwRuleUpdateParams:
		if p.Direction != nil {
			schemaFwDirection(*p.Direction, ref, &out)
		}
		if p.Action != nil {
			schemaFwAction(*p.Action, ref, &out)
		}
		if p.Log != nil {
			schemaFwLog(*p.Log, ref, &out)
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
