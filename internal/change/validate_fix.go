package change

import "net"

// This file builds the machine-applicable `fix` patches (docs/api.md's
// finding shape: `fix?` — "an []Op patch the UI can offer one-click")
// docs/features/change-management.md §2 calls out as the flagship example:
// an MTU clamp. Every fix here is a single-op patch sharing the offending
// op's exact Type and Target, with one field's value clamped into range —
// the property test in validate_test.go locates the op to replace by that
// (Type, Target) pair and asserts the substituted changeset revalidates
// clean.

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// fixClampMTU returns a one-op fix that clamps op's MTU field into
// [minMTU, maxMTU]. It returns nil for op types with no MTU field (no
// computable fix for those findings).
func fixClampMTU(op Op) []Op {
	switch p := op.Params.(type) {
	case *BondCreateParams:
		cp := *p
		cp.MTU = clampInt(cp.MTU, minMTU, maxMTU)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *BondUpdateParams:
		cp := *p
		if cp.MTU != nil {
			v := clampInt(*cp.MTU, minMTU, maxMTU)
			cp.MTU = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *BridgeCreateParams:
		cp := *p
		cp.MTU = clampInt(cp.MTU, minMTU, maxMTU)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *BridgeUpdateParams:
		cp := *p
		if cp.MTU != nil {
			v := clampInt(*cp.MTU, minMTU, maxMTU)
			cp.MTU = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *VlanCreateParams:
		cp := *p
		cp.MTU = clampInt(cp.MTU, minMTU, maxMTU)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *VlanUpdateParams:
		cp := *p
		if cp.MTU != nil {
			v := clampInt(*cp.MTU, minMTU, maxMTU)
			cp.MTU = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *IfaceUpdateParams:
		cp := *p
		if cp.MTU != nil {
			v := clampInt(*cp.MTU, minMTU, maxMTU)
			cp.MTU = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *SdnZoneCreateParams:
		cp := *p
		cp.MTU = clampInt(cp.MTU, minMTU, maxMTU)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *SdnZoneUpdateParams:
		cp := *p
		if cp.MTU != nil {
			v := clampInt(*cp.MTU, minMTU, maxMTU)
			cp.MTU = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	default:
		return nil
	}
}

// fixSetVxlanMTU returns a one-op fix that sets op's MTU field to exactly
// mtu (not a clamp — checkVxlanMTU's caller already computed the precise
// PVE-recommended value, underlayMTU-vxlanOverhead). It returns nil for op
// types with no MTU field.
func fixSetVxlanMTU(op Op, mtu int) []Op {
	switch p := op.Params.(type) {
	case *SdnZoneCreateParams:
		cp := *p
		cp.MTU = mtu
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *SdnZoneUpdateParams:
		cp := *p
		v := mtu
		cp.MTU = &v
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	default:
		return nil
	}
}

// fixClampVID returns a one-op fix that clamps op's single VID/Tag field
// into [minVID, maxVID]. It returns nil for op types with no such field.
func fixClampVID(op Op) []Op {
	switch p := op.Params.(type) {
	case *VlanCreateParams:
		cp := *p
		cp.Vid = clampInt(cp.Vid, minVID, maxVID)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *SdnVnetCreateParams:
		cp := *p
		cp.Tag = clampInt(cp.Tag, minVID, maxVID)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *SdnVnetUpdateParams:
		cp := *p
		if cp.Tag != nil {
			v := clampInt(*cp.Tag, minVID, maxVID)
			cp.Tag = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	default:
		return nil
	}
}

// fixClampBridgeVids returns a one-op fix that clamps every VidRange bound
// in a bridge.create/update op's Vids list into [minVID, maxVID]. It
// returns nil for any other op type.
func fixClampBridgeVids(op Op) []Op {
	clampRanges := func(vs []VidRange) []VidRange {
		out := make([]VidRange, len(vs))
		for i, v := range vs {
			out[i] = VidRange{Low: clampInt(v.Low, minVID, maxVID), High: clampInt(v.High, minVID, maxVID)}
		}
		return out
	}
	switch p := op.Params.(type) {
	case *BridgeCreateParams:
		cp := *p
		cp.Vids = clampRanges(cp.Vids)
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *BridgeUpdateParams:
		cp := *p
		if cp.Vids != nil {
			v := clampRanges(*cp.Vids)
			cp.Vids = &v
		}
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	default:
		return nil
	}
}

// firstUsableIP returns cidr's first usable host address (network address
// + 1) as a plain-string IP, or ok=false if cidr does not parse. This is
// deliberately the same "network + 1" convention
// internal/ipam/addr.go's hostAddresses uses for its own start offset —
// the fix this computes and the wizard's own live pre-fill (web/src/ipam/
// nextFree.ts's firstUsableIPv4) always agree, and a /31 or /32 (no
// meaningful network/broadcast pair to exclude) still gets *some* address
// back rather than nothing, matching hostAddresses' own "include every
// address" fallback for those.
func firstUsableIP(cidr string) (string, bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", false
	}
	network := ipnet.IP.Mask(ipnet.Mask)
	ones, bits := ipnet.Mask.Size()
	total := 1 << (bits - ones)
	offset := 0
	if total >= 4 {
		offset = 1
	}
	return offsetIP(network, offset).String(), true
}

// offsetIP returns base + n, treating base as a big-endian integer of its
// own byte length (4 for IPv4, 16 for IPv6) — mirrors internal/ipam/
// addr.go's own offsetIP (a separate, unexported copy: that package's
// helper is internal to package ipam and this package's validators are
// deliberately pure with no cross-package dependency of their own — see
// validate.go's doc comment).
func offsetIP(base net.IP, n int) net.IP {
	ip := append(net.IP(nil), base...)
	for i := len(ip) - 1; i >= 0 && n > 0; i-- {
		sum := int(ip[i]) + n
		ip[i] = byte(sum & 0xff)
		n = sum >> 8
	}
	return ip
}

// fixSetSubnetGateway returns a one-op fix that sets op's Gateway field to
// cidr's first usable address (T-701 acceptance criterion 2's "fix patch
// setting gateway 10.50.0.1"-shaped fix, shared by
// schemaGatewayInCIDR/codeGatewayNotInSubnet and validate_sdn.go's
// codeSNATRequiresGateway). Returns nil for op types with no Gateway field,
// or if cidr does not parse.
func fixSetSubnetGateway(op Op, cidr string) []Op {
	gw, ok := firstUsableIP(cidr)
	if !ok {
		return nil
	}
	switch p := op.Params.(type) {
	case *SdnSubnetCreateParams:
		cp := *p
		cp.Gateway = gw
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	case *SdnSubnetUpdateParams:
		cp := *p
		cp.Gateway = &gw
		return []Op{{Type: op.Type, Target: op.Target, Params: &cp}}
	default:
		return nil
	}
}
