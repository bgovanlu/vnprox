package change

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
