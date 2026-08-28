// SPDX-License-Identifier: Apache-2.0

// vlancheck.go implements docs/features/lldp-discovery.md §2's VLAN
// cross-check: "VLANs the bridge/bond expects vs. VLANs the switch
// advertises on that port, with mismatches flagged (\"bridge vmbr1 is
// VLAN-aware for 10–30 but switch port Gi1/0/14 advertises only 10,20\")."

package topology

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// VLAN cross-check finding codes.
const (
	VlanCheckOK              = "vlan_cross_check_ok"
	VlanCheckMissingOnSwitch = "vlan_cross_check_missing_on_switch"
	VlanCheckMissingOnBridge = "vlan_cross_check_missing_on_bridge"
)

// VlanFinding is one bridge/bond-vs-switch-port VLAN comparison result.
// BridgeRef names the VLAN-aware bridge whose expected VIDs were compared;
// NeighborRef names the inventory.LldpNeighbor (the switch port) compared
// against. Severity mirrors docs/api.md's changeset finding vocabulary
// ("info"|"warning") even though this is a read-only topology finding, not
// a change-engine validation result, so the frontend can reuse the same
// severity styling.
type VlanFinding struct {
	BridgeRef   string   `json:"bridgeRef"`
	NeighborRef string   `json:"neighborRef"`
	Code        string   `json:"code"`
	Severity    string   `json:"severity"`
	Message     string   `json:"message"`
	Expected    []string `json:"expected"`          // bridge/bond-expected VIDs, rendered ("10-30" style ranges collapsed)
	Advertised  []string `json:"advertised"`        // switch-advertised VIDs (PVID + tagged)
	Missing     []int    `json:"missing,omitempty"` // expected but not advertised (missing-on-switch)
	Extra       []int    `json:"extra,omitempty"`   // advertised but not expected (missing-on-bridge)
}

// VlanFindings walks every LldpNeighbor in snap whose local NIC resolves to
// a VLAN-aware bridge (directly, or via an enslaving bond) and compares the
// bridge's expected VID set against the neighbor's advertised VLANs (PVID +
// tagged), producing one finding per (bridge, neighbor) pair — always one,
// even on a clean match (spec's cross-check is a standing display, not just
// an alert list; AC2's "matching ... produce the documented finding" case).
func VlanFindings(snap inventory.Snapshot) []VlanFinding {
	var out []VlanFinding
	for _, e := range snap.All() {
		n, ok := e.(*inventory.LldpNeighbor)
		if !ok || n.LocalNic.IsZero() {
			continue
		}
		bridge := BridgeFor(snap, n.LocalNic)
		if bridge == nil || !bridge.VlanAware {
			continue
		}
		out = append(out, vlanFinding(bridge, n))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BridgeRef != out[j].BridgeRef {
			return out[i].BridgeRef < out[j].BridgeRef
		}
		return out[i].NeighborRef < out[j].NeighborRef
	})
	return out
}

// BridgeFor resolves nicRef's Bridge, either directly (the NIC is a bridge
// port itself) or transitively through an enslaving Bond (the common
// bonded-uplink case: PhysNic -enslaved-by-> Bond -port-of-> Bridge).
// Exported (T-1506) so other packages needing "which bridge does this NIC's
// traffic ultimately ride" — internal/drift's vf_spoofcheck_mismatch check,
// internal/change's matching vf.provision validation — share this one
// resolver rather than re-implementing the bond-transitive walk.
func BridgeFor(snap inventory.Snapshot, nicRef inventory.Ref) *inventory.Bridge {
	for _, edge := range snap.EdgesOf(nicRef) {
		if edge.From != nicRef {
			continue
		}
		switch edge.Kind {
		case inventory.EdgePortOf:
			if b, ok := snap.Get(edge.To); ok {
				if br, ok := b.(*inventory.Bridge); ok {
					return br
				}
			}
		case inventory.EdgeEnslavedBy:
			if bond, ok := snap.Get(edge.To); ok {
				if _, ok := bond.(*inventory.Bond); ok {
					if br := BridgeFor(snap, edge.To); br != nil {
						return br
					}
				}
			}
		}
	}
	return nil
}

func vlanFinding(bridge *inventory.Bridge, n *inventory.LldpNeighbor) VlanFinding {
	expected := expandVidRanges(bridge.Vids)
	advertised := map[int]bool{}
	if n.VLAN != 0 {
		advertised[n.VLAN] = true
	}
	for _, v := range n.TaggedVLANs {
		advertised[v] = true
	}

	var missing, extra []int // missing: expected but not advertised; extra: advertised but not expected
	for v := range expected {
		if !advertised[v] {
			missing = append(missing, v)
		}
	}
	for v := range advertised {
		if !expected[v] {
			extra = append(extra, v)
		}
	}
	sort.Ints(missing)
	sort.Ints(extra)

	f := VlanFinding{
		BridgeRef:   bridge.GetRef().String(),
		NeighborRef: n.GetRef().String(),
		Expected:    vidRangeStrings(bridge.Vids),
		Advertised:  intStrings(sortedKeys(advertised)),
	}
	switch {
	case len(missing) == 0 && len(extra) == 0:
		f.Code = VlanCheckOK
		f.Severity = "info"
		f.Message = fmt.Sprintf("bridge %s and switch port %s agree on VLANs %s",
			bridge.Name, n.PortID, strings.Join(f.Advertised, ","))
	case len(missing) > 0:
		// docs/features/lldp-discovery.md §2's example is this case:
		// "bridge vmbr1 is VLAN-aware for 10–30 but switch port Gi1/0/14
		// advertises only 10,20."
		f.Code = VlanCheckMissingOnSwitch
		f.Severity = "warning"
		f.Missing = missing
		f.Message = fmt.Sprintf("bridge %s is VLAN-aware for %s but switch port %s advertises only %s",
			bridge.Name, strings.Join(f.Expected, ","), n.PortID, strings.Join(f.Advertised, ","))
	default:
		f.Code = VlanCheckMissingOnBridge
		f.Severity = "warning"
		f.Extra = extra
		f.Message = fmt.Sprintf("switch port %s advertises VLANs %s that bridge %s does not expect (configured for %s)",
			n.PortID, intStrings(extra), bridge.Name, strings.Join(f.Expected, ","))
	}
	return f
}

func expandVidRanges(ranges []inventory.VidRange) map[int]bool {
	out := map[int]bool{}
	for _, r := range ranges {
		for v := r.Low; v <= r.High; v++ {
			out[v] = true
		}
	}
	return out
}

func vidRangeStrings(ranges []inventory.VidRange) []string {
	out := make([]string, len(ranges))
	for i, r := range ranges {
		out[i] = r.String()
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func intStrings(vs []int) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = strconv.Itoa(v)
	}
	return out
}
