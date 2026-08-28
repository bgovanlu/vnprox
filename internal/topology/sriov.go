// SPDX-License-Identifier: Apache-2.0

// sriov.go backs T-1506's SR-IOV & accelerated NIC lifecycle: guest<->VF
// correlation (today-invisible passthrough devices, surfaced live in the
// inspector rather than baked into a stored guess — see
// ResolveVFAssignments) and the VF/bridge policy comparison shared by
// internal/change's changeset-validate-time vf_spoofcheck_mismatch check
// and internal/drift's standing drift finding of the identical name, so
// the two can never disagree about what "consistent" means.

package topology

import (
	"regexp"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// pciAddrPattern matches a PVE-style PCI address, with or without the
// leading domain (PVE accepts both "0000:01:00.1" and the short "01:00.1"
// form — the domain defaults to "0000" when omitted, the same default the
// kernel's own sysfs bus addressing uses).
var pciAddrPattern = regexp.MustCompile(`^(?:([0-9a-fA-F]{4}):)?([0-9a-fA-F]{2}):([0-9a-fA-F]{2})\.([0-9a-fA-F])$`)

// parseHostPCIAddr extracts and normalizes the leading PCI address from one
// PVE guest hostpciN config value (e.g. "0000:01:00.1,pcie=1" or the short
// "01:00.1"). ok is false for a value with no recognizable PCI address at
// all — notably PVE's resource-mapping form ("mapping=<name>", an
// indirection through /etc/pve/mapping/pci.cfg this package does not
// resolve) — so an unrecognized value is left uncorrelated, never guessed.
func parseHostPCIAddr(raw string) (string, bool) {
	first := raw
	if i := strings.IndexByte(raw, ','); i >= 0 {
		first = raw[:i]
	}
	m := pciAddrPattern.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", false
	}
	domain := m[1]
	if domain == "" {
		domain = "0000"
	}
	return strings.ToLower(domain + ":" + m[2] + ":" + m[3] + "." + m[4]), true
}

// ResolveVFAssignments returns pf's (a PhysNic Ref) currently-configured VFs
// (inventory.PhysNic.SRIOVVFs) with AssignedGuest resolved live against
// every guest's hostpci config on the same node — host-netlink alone has no
// way to know which guest owns a passthrough VF, so this correlation is
// computed on read (the inspector, T-1506's "surfaced ... as an attached
// entity") rather than baked into the collector-owned entity, mirroring
// this package's Detail's identical live-computed treatment of a bridge's
// FDB owner labels. Returns nil if pf does not resolve to a PhysNic, or
// resolves to one with no VFs. A VF whose PCIAddr does not match any
// guest's parsed hostpci PCI address keeps the zero AssignedGuest Ref
// (unmatched, never a wrong guess).
func ResolveVFAssignments(snap inventory.Snapshot, pf inventory.Ref) []inventory.VirtualFunction {
	e, ok := snap.Get(pf)
	if !ok {
		return nil
	}
	nic, ok := e.(*inventory.PhysNic)
	if !ok || len(nic.SRIOVVFs) == 0 {
		return nil
	}

	// PCI address -> guest Ref, scoped to pf's own node: hostpci passthrough
	// is always node-local (a guest can only be assigned a VF belonging to
	// a PF on the node it runs on).
	byPCI := map[string]inventory.Ref{}
	for _, ent := range snap.All() {
		g, ok := ent.(*inventory.Guest)
		if !ok || g.Node != pf.Node {
			continue
		}
		for _, raw := range g.HostPCI {
			if addr, ok := parseHostPCIAddr(raw); ok {
				byPCI[addr] = g.GetRef()
			}
		}
	}

	out := make([]inventory.VirtualFunction, len(nic.SRIOVVFs))
	for i, vf := range nic.SRIOVVFs {
		out[i] = vf
		if vf.PCIAddr == "" {
			continue
		}
		if guestRef, ok := byPCI[strings.ToLower(vf.PCIAddr)]; ok {
			out[i].AssignedGuest = guestRef
		}
	}
	return out
}

// VFPolicyMismatch reports whether vf's configured VLAN/spoof-check
// diverges from bridge's own VLAN-awareness/VID-set policy — T-1506's
// vf_spoofcheck_mismatch check (docs/features's SR-IOV section):
//
//   - bridge nil (the PF isn't attached to any bridge, directly or via an
//     enslaving bond — BridgeFor found nothing) means there is no bridge
//     policy to compare against, so nothing is flagged.
//   - A non-VLAN-aware (access-mode) bridge has no VLAN concept at all; a
//     VF tagging traffic under it is inherently inconsistent.
//   - A VLAN-aware bridge's own VID-set access control is undermined by a
//     VF that can spoof its MAC/VLAN, so SpoofCheck disabled is a mismatch.
//   - A VLAN-aware bridge whose declared VID set does not include the VF's
//     tag is a mismatch too (the VF tags traffic the bridge won't
//     recognize/forward correctly).
func VFPolicyMismatch(vf inventory.VirtualFunction, bridge *inventory.Bridge) bool {
	if bridge == nil {
		return false
	}
	if !bridge.VlanAware {
		return vf.VLAN != 0
	}
	if !vf.SpoofCheck {
		return true
	}
	if vf.VLAN != 0 && !vidInRanges(vf.VLAN, bridge.Vids) {
		return true
	}
	return false
}

func vidInRanges(vid int, ranges []inventory.VidRange) bool {
	for _, r := range ranges {
		if vid >= r.Low && vid <= r.High {
			return true
		}
	}
	return false
}
