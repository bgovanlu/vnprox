// sriov.go implements T-1506's vf_spoofcheck_mismatch standing drift check:
// an already-diverged live SR-IOV VF (host-netlink observed, inventory.
// PhysNic.SRIOVVFs) whose configured VLAN/spoof-check setting no longer
// matches its PF's own bridge's VLAN-awareness/VID-set policy —
// topology.VFPolicyMismatch, the identical comparison internal/change's
// changeset-validate-time check (validate_referential.go's
// checkVFProvision) reuses for a *staged* vf.provision op, so the two can
// never disagree about what "consistent" means. Detection only, mirroring
// checkPendingInterfaces' own stance (pending.go): correcting an
// already-diverged VF is a real infrastructure action outside the v1 op
// vocabulary's read side — the fix is a fresh, reviewed vf.provision
// changeset, not something a drift checker does unattended.

package drift

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// checkVFSpoofcheckMismatch is the CheckVFSpoofcheckMismatch family.
func checkVFSpoofcheckMismatch(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		pn, ok := e.(*inventory.PhysNic)
		if !ok || len(pn.SRIOVVFs) == 0 {
			continue
		}
		bridge := topology.BridgeFor(snap, pn.GetRef())
		if bridge == nil {
			continue
		}
		for _, vf := range pn.SRIOVVFs {
			if !topology.VFPolicyMismatch(vf, bridge) {
				continue
			}
			detail := fmt.Sprintf(
				"VF %s on PF %s (node %s) diverges from bridge %s's VLAN-awareness/VID-set policy (vf vlan=%d spoofCheck=%t)",
				vf.ID, pn.Name, pn.GetRef().Node, bridge.Name, vf.VLAN, vf.SpoofCheck,
			)
			out = append(out, newFinding(CheckVFSpoofcheckMismatch, SeverityWarning, detail,
				[]string{pn.GetRef().Node}, []string{vf.String()}))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
