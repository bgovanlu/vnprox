package change

import "github.com/bgovanlu/vnprox/internal/inventory"

// advisoryValidate is validator class 5 (docs/features/change-management.md
// §2 item 5: "style/health warnings ... bond without
// xmit_hash_policy layer3+4 on 802.3ad, bridge without description,
// single-slave bond"). Every finding here is SeverityWarning: these never
// block apply, only the "apply with warnings" checkbox.
//
// For update ops, the existing entity's current declared state (from snap)
// is merged with the op's partial override before evaluating a check, so
// e.g. a bond.update that only sets XmitHashPolicy still correctly warns
// (or doesn't) if the bond's Mode is already 802.3ad without needing that
// op to also touch Mode.
func advisoryValidate(ops []Op, snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, op := range ops {
		out = append(out, advisoryValidateOp(op, snap)...)
	}
	return out
}

func advisoryValidateOp(op Op, snap inventory.Snapshot) []Finding {
	ref := refOf(op)
	var out []Finding

	switch params := op.Params.(type) {
	case *BondCreateParams:
		checkBondAdvisory(ref, params.Mode, params.XmitHashPolicy, params.Slaves, &out)

	case *BondUpdateParams:
		var existingMode, existingHash string
		var existingSlaves []string
		if e, ok := snap.Get(op.Target); ok {
			if b, ok := e.(*inventory.Bond); ok {
				existingMode, existingHash = b.Mode, b.XmitHashPolicy
				existingSlaves = firstNonEmpty(b.Slaves, b.DeclaredSlaves)
			}
		}
		mode, hash, slaves := existingMode, existingHash, existingSlaves
		if params.Mode != nil {
			mode = *params.Mode
		}
		if params.XmitHashPolicy != nil {
			hash = *params.XmitHashPolicy
		}
		if params.Slaves != nil {
			slaves = *params.Slaves
		}
		checkBondAdvisory(ref, mode, hash, slaves, &out)

	case *BridgeCreateParams:
		if params.Comments == "" {
			out = append(out, warnf(codeAdvisoryBridgeComment, ref, "bridge %s has no description", op.Target.ID))
		}

	case *BridgeUpdateParams:
		comments := ""
		if e, ok := snap.Get(op.Target); ok {
			if b, ok := e.(*inventory.Bridge); ok {
				comments = b.Comments
			}
		}
		if params.Comments != nil {
			comments = *params.Comments
		}
		if comments == "" {
			out = append(out, warnf(codeAdvisoryBridgeComment, ref, "bridge %s has no description", op.Target.ID))
		}

	case *SdnZoneCreateParams:
		if params.Type == "vxlan" || params.Type == "evpn" {
			checkVxlanMTU(op, params.MTU, ref, &out)
		}

	case *SdnZoneUpdateParams:
		if params.MTU != nil {
			typ := ""
			if e, ok := snap.Get(op.Target); ok {
				if z, ok := e.(*inventory.SdnZone); ok {
					typ = z.Type
				}
			}
			if typ == "vxlan" || typ == "evpn" {
				checkVxlanMTU(op, *params.MTU, ref, &out)
			}
		}
	}

	return out
}

// checkVxlanMTU is docs/features/sdn.md §2's VXLAN wizard MTU math, run as
// an advisory check on any vxlan/evpn zone.create/update carrying an
// explicit MTU (docs/features/sdn.md §4's "MTU sanity"): a zone MTU that
// leaves no room for VXLAN's encapsulation overhead over the assumed
// underlay path MTU (underlayMTU, validate_sdn.go) degrades or silently
// drops encapsulated traffic rather than an apply-time hard failure, so
// this is a warning (never blocking) with a one-click fix clamping it to
// exactly the PVE-recommended figure — T-402 acceptance criterion 3: "1500
// underlay + vnet MTU 1500 → warning with fix patch (set 1450)". mtu == 0
// (unset — PVE applies its own sane default) is not flagged.
func checkVxlanMTU(op Op, mtu int, ref string, out *[]Finding) {
	if mtu == 0 {
		return
	}
	safe := underlayMTU - vxlanOverhead
	if mtu > safe {
		f := warnf(codeAdvisoryVxlanMTU, ref,
			"zone mtu %d leaves no headroom for VXLAN's %d-byte encapsulation overhead over the assumed %d-byte underlay path MTU — encapsulated traffic may be fragmented or dropped; set it to %d",
			mtu, vxlanOverhead, underlayMTU, safe)
		f.Fix = fixSetVxlanMTU(op, safe)
		*out = append(*out, f)
	}
}

// checkBondAdvisory evaluates the two bond-shaped advisories against an
// already-merged effective (mode, xmitHashPolicy, slaves) view.
func checkBondAdvisory(ref, mode, hash string, slaves []string, out *[]Finding) {
	if mode == "802.3ad" && hash != "layer3+4" {
		*out = append(*out, warnf(codeAdvisoryBondHashPolicy, ref,
			"802.3ad bonds should set xmitHashPolicy=layer3+4 for even traffic distribution across the aggregate"))
	}
	if len(slaves) == 1 {
		*out = append(*out, warnf(codeAdvisorySingleSlave, ref,
			"a single-slave bond provides no redundancy or bandwidth benefit over using the interface directly"))
	}
}
