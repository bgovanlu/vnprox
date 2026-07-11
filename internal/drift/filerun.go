// filerun.go implements docs/features/topology.md §6's fifth check family:
// "interfaces file vs. runtime state (someone edited by hand / ran `ip`
// commands)" — comparing a single entity's own declared (host-interfaces/
// pve-network) fields against its own live (host-netlink) fields: bridge
// port membership, bond slave membership, and MTU. Detection only: which
// side (file or live state) reflects the operator's actual intent cannot
// be inferred safely, so no fix is offered — the user reviews and decides,
// same as the raw interfaces editor.

package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

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
// unordered name sets) for one bridge or bond entity.
func membershipFinding(ref inventory.Ref, kindLabel string, live, declared []string) (Finding, bool) {
	liveSet, declaredSet := sortedUnique(live), sortedUnique(declared)
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
