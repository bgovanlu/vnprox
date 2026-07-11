package change

import (
	"bytes"
	"net"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

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
//
// allocations is T-406's DHCP-range-overlap input (SafetyOptions.Allocations,
// see that field's doc comment) — threaded through here rather than only to
// the one check that needs it, matching how snap is already threaded
// through unconditionally for every other check.
func advisoryValidate(ops []Op, snap inventory.Snapshot, allocations []DHCPRangeAllocation) []Finding {
	var out []Finding
	for _, op := range ops {
		out = append(out, advisoryValidateOp(op, snap, allocations)...)
	}
	return out
}

func advisoryValidateOp(op Op, snap inventory.Snapshot, allocations []DHCPRangeAllocation) []Finding {
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

	case *SdnSubnetCreateParams:
		if len(params.DHCPRanges) > 0 {
			checkDHCPRangeOverlap(op.Target, params.DHCPRanges, ref, allocations, &out)
		}

	case *SdnSubnetUpdateParams:
		if params.DHCPRanges != nil {
			checkDHCPRangeOverlap(op.Target, *params.DHCPRanges, ref, allocations, &out)
		}
	}

	return out
}

// checkDHCPRangeOverlap is T-406 acceptance criterion 4: a staged/updated
// subnet DHCP range that overlaps one or more existing IPAM allocations
// (allocations — see SafetyOptions.Allocations' doc comment, and this
// package's completion report for why this data is threaded in as an
// already-fetched slice rather than a live-fetch seam this pure package
// would own itself) warns, listing the specific overlapping addresses (and
// their hostname/MAC when known) so the operator can see exactly what
// they'd be stepping on. Never blocks: real PVE itself does not reject
// this at config-apply time (a dnsmasq DHCP pool freely coexists with
// statically-reserved addresses inside its range — the standard "static
// reservation carved out of a DHCP pool" pattern), so this is advisory,
// not referential.
func checkDHCPRangeOverlap(target inventory.Ref, ranges []string, ref string, allocations []DHCPRangeAllocation, out *[]Finding) {
	if len(allocations) == 0 {
		return
	}
	subnetCIDR := target.ID
	for _, r := range ranges {
		start, end, ok := parseDHCPRangeIPs(r)
		if !ok {
			// schema class (validate_schema.go's validDHCPRange) already
			// flags an unparsable range as a blocking error, which
			// short-circuits before advisory ever runs — this is just
			// defensive against being called directly (e.g. from a test)
			// with a range schemaValidate would have rejected.
			continue
		}
		var hits []DHCPRangeAllocation
		for _, a := range allocations {
			if a.Subnet != subnetCIDR {
				continue
			}
			ip := net.ParseIP(a.IP)
			if ip == nil || !ipInRange(ip, start, end) {
				continue
			}
			hits = append(hits, a)
		}
		if len(hits) == 0 {
			continue
		}
		*out = append(*out, warnf(codeAdvisoryDHCPRangeOverlap, ref,
			"dhcp range %s overlaps %d existing allocation(s): %s", r, len(hits), describeDHCPAllocations(hits)))
	}
}

// parseDHCPRangeIPs parses s ("startIP-endIP", validDHCPRange's own shape)
// into its two endpoint IPs.
func parseDHCPRangeIPs(s string) (start, end net.IP, ok bool) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return nil, nil, false
	}
	start = net.ParseIP(strings.TrimSpace(parts[0]))
	end = net.ParseIP(strings.TrimSpace(parts[1]))
	if start == nil || end == nil {
		return nil, nil, false
	}
	return start, end, true
}

// ipInRange reports whether ip falls within [start, end] inclusive,
// comparing each IP's 16-byte (v4-in-v6) form so IPv4 and IPv6 addresses
// compare consistently regardless of which literal form net.ParseIP
// produced for each.
func ipInRange(ip, start, end net.IP) bool {
	ip16, start16, end16 := ip.To16(), start.To16(), end.To16()
	if ip16 == nil || start16 == nil || end16 == nil {
		return false
	}
	return bytes.Compare(ip16, start16) >= 0 && bytes.Compare(ip16, end16) <= 0
}

// describeDHCPAllocations renders hits as a human-readable, comma-joined
// "ip (who)" list for checkDHCPRangeOverlap's warning message — "who" is
// the allocation's hostname, falling back to its MAC, falling back to just
// the bare IP when neither is known.
func describeDHCPAllocations(hits []DHCPRangeAllocation) string {
	labels := make([]string, 0, len(hits))
	for _, h := range hits {
		switch {
		case h.Hostname != "":
			labels = append(labels, h.IP+" ("+h.Hostname+")")
		case h.MAC != "":
			labels = append(labels, h.IP+" ("+h.MAC+")")
		default:
			labels = append(labels, h.IP)
		}
	}
	return strings.Join(labels, ", ")
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
