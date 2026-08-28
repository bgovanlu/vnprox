// SPDX-License-Identifier: Apache-2.0

// filerun.go implements docs/features/topology.md §6's fifth check family:
// "interfaces file vs. runtime state (someone edited by hand / ran `ip`
// commands)" — comparing a single entity's own declared (host-interfaces/
// pve-network) fields against its own live (host-netlink) fields: bridge
// port membership, bond slave membership, and MTU. Detection only: which
// side (file or live state) reflects the operator's actual intent cannot
// be inferred safely, so no fix is offered — the user reviews and decides,
// same as the raw interfaces editor.
//
// Bridge port membership is compared after dropping members PVE's own
// pve-firewall/tap-plug plumbing creates and destroys around a guest's
// lifecycle (fwbr*/fwln*/fwpr*, and a guest's own tap*/veth* device) — see
// runtimeOwnedMemberPattern. Those are never written to the interfaces file
// by design (T-3502; verified against pvecube, PVE 9.2.4, see
// planning/reports/evidence/pve-9.2.4-firewall-veths.txt), so their
// live-only presence is not the manual `ip link`/`brctl addif` change this
// check exists to catch.

package drift

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// runtimeOwnedMemberPattern matches interface names PVE itself creates and
// destroys around a guest's lifecycle, never written to
// /etc/network/interfaces by design. Verified against pvecube (PVE 9.2.4,
// live node, 2026-08-19) — both the live membership
// (/sys/class/net/vmbr0/brif: enp1s0,fwpr103p0,fwpr104p0 against a file
// declaring enp1s0 alone) and the naming/lifecycle, read out of the PVE
// package's own installed source (PVE::Network's $compute_fwbr_names and
// tap_plug) rather than assumed from internal/pvemock or docs — see
// planning/reports/evidence/pve-9.2.4-firewall-veths.txt.
//
//	fwbr<vmid>i<netid>   the per-guest-NIC firewall bridge itself
//	fwln<vmid>i<netid>   veth peer inside the firewall bridge (Linux bridge)
//	fwln<vmid>o<netid>   ovs-int-port peer inside the firewall bridge (OVS)
//	fwpr<vmid>p<netid>   veth peer enslaved to the real bridge (vmbr*)
//	tap<vmid>i<netid>    a QEMU guest NIC's tap device
//	veth<vmid>i<netid>   an LXC guest NIC's veth device
//
// tap*/veth* are included even though pve-firewall's own bridge only ever
// touches fwbr/fwln/fwpr: when a guest NIC has firewall=0 (also a common
// GUI-created state — e.g. pvecube's own "opnsense" VM), PVE::Network::
// tap_plug enslaves the guest's tap/veth device DIRECTLY to the real bridge
// with no fwbr indirection at all, and that device is just as absent from
// the interfaces file. Both code paths are documented in the evidence file
// above.
//
// The pattern requires PVE's own <vmid>i<netid>/<vmid>p<netid> numbering, not
// a bare prefix match, so a coincidentally similarly-named hand-added
// interface (e.g. a literal "veth0" a human created with `ip link add`) does
// not get silently swallowed — see TestFileRuntimeDivergence_FirewallVeths's
// table, which covers both directions.
var runtimeOwnedMemberPattern = regexp.MustCompile(`^(fwbr\d+i\d+|fwln\d+[io]\d+|fwpr\d+p\d+|tap\d+i\d+|veth\d+i\d+)$`)

// isRuntimeOwnedMember reports whether name matches one of PVE's own
// runtime-created interface shapes (see runtimeOwnedMemberPattern).
func isRuntimeOwnedMember(name string) bool {
	return runtimeOwnedMemberPattern.MatchString(name)
}

// dropRuntimeOwned removes PVE-runtime-owned members from a membership set
// before it is compared between the interfaces file and the kernel. Applied
// to both sides defensively: the declared side should never contain them (by
// design they are not written to the file), so filtering it is a no-op in
// the real case and only guards against a fixture that got this wrong.
func dropRuntimeOwned(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !isRuntimeOwnedMember(n) {
			out = append(out, n)
		}
	}
	return out
}

// checkFileRuntimeDivergence is the CheckFileRuntimeDivergence family.
func checkFileRuntimeDivergence(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Bridge:
			if f, ok := membershipFinding(v.GetRef(), "bridge", v.PortNames, v.DeclaredPortNames); ok {
				out = append(out, f)
			}
			if f, ok := mtuDivergenceFinding(v.GetRef(), "bridge", v.MTU, v.MTUDeclared); ok {
				out = append(out, f)
			}
		case *inventory.Bond:
			if f, ok := membershipFinding(v.GetRef(), "bond", v.Slaves, v.DeclaredSlaves); ok {
				out = append(out, f)
			}
			if f, ok := mtuDivergenceFinding(v.GetRef(), "bond", v.MTU, v.MTUDeclared); ok {
				out = append(out, f)
			}
		case *inventory.PhysNic:
			if f, ok := mtuDivergenceFinding(v.GetRef(), "physical NIC", v.MTU, v.MTUDeclared); ok {
				out = append(out, f)
			}
		case *inventory.VlanIface:
			if f, ok := mtuDivergenceFinding(v.GetRef(), "VLAN interface", v.MTU, v.MTUDeclared); ok {
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// membershipFinding compares live vs. declared port/slave membership (as
// unordered name sets) for one bridge or bond entity. Members PVE itself
// creates and destroys around a guest's lifecycle (pve-firewall's veth
// plumbing, and a guest's own tap/veth device when its NIC has firewall=0)
// are dropped from the live side first — see runtimeOwnedMemberPattern —
// since they are never written to the interfaces file by design and their
// live-only presence is not evidence of a manual change.
func membershipFinding(ref inventory.Ref, kindLabel string, live, declared []string) (Finding, bool) {
	liveSet, declaredSet := sortedUnique(dropRuntimeOwned(live)), sortedUnique(dropRuntimeOwned(declared))
	if len(liveSet) == 0 || len(declaredSet) == 0 {
		// One side unreported: nothing to compare (avoids false positives
		// before both host-netlink and host-interfaces/pve-network have
		// polled at least once).
		return Finding{}, false
	}
	if sameSet(liveSet, declaredSet) {
		return Finding{}, false
	}
	added, removed := setDiff(liveSet, declaredSet)
	detail := fmt.Sprintf("%s %s on node %s: live (netlink) membership is %s, but the interfaces file declares %s — a manual `ip link` change may have been made outside vnprox",
		kindLabel, ref.ID, ref.Node, describeSet(liveSet, added), describeSet(declaredSet, removed))
	f := newFinding(CheckFileRuntimeDivergence, SeverityWarning, detail, []string{ref.Node}, []string{ref.String()})
	return f, true
}

// mtuDivergenceFinding compares an entity's own runtime MTU against its own
// declared MTU (both must be reported and non-equal to fire).
func mtuDivergenceFinding(ref inventory.Ref, kindLabel string, runtime, declared int) (Finding, bool) {
	if runtime == 0 || declared == 0 || runtime == declared {
		return Finding{}, false
	}
	detail := fmt.Sprintf("%s %s on node %s: live (netlink) MTU is %d, but the interfaces file declares %d — a manual `ip link set mtu` change may have been made outside vnprox",
		kindLabel, ref.ID, ref.Node, runtime, declared)
	f := newFinding(CheckFileRuntimeDivergence, SeverityWarning, detail, []string{ref.Node}, []string{ref.String()})
	return f, true
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setDiff returns (in a, not in b) and (in b, not in a) for two sorted,
// deduped slices.
func setDiff(a, b []string) (onlyA, onlyB []string) {
	bSet := make(map[string]bool, len(b))
	for _, x := range b {
		bSet[x] = true
	}
	aSet := make(map[string]bool, len(a))
	for _, x := range a {
		aSet[x] = true
	}
	for _, x := range a {
		if !bSet[x] {
			onlyA = append(onlyA, x)
		}
	}
	for _, x := range b {
		if !aSet[x] {
			onlyB = append(onlyB, x)
		}
	}
	return onlyA, onlyB
}

// describeSet renders a set for the finding message, marking the members
// that don't appear on the other side.
func describeSet(all, distinct []string) string {
	if len(all) == 0 {
		return "(none)"
	}
	distinctSet := make(map[string]bool, len(distinct))
	for _, d := range distinct {
		distinctSet[d] = true
	}
	parts := make([]string, len(all))
	for i, a := range all {
		if distinctSet[a] {
			parts[i] = a + "*"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, ",")
}
