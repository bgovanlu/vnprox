// SPDX-License-Identifier: Apache-2.0

package whatif

import (
	"strconv"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// augmentSnapshot returns a snapshot identical to base but with extraGuests
// (synthetic Guest/GuestNic entities) added, reconstructed through a fresh
// inventory.Graph so attachment resolution (bridge/vnet linking) runs
// exactly as it would for a live poll.
//
// This mirrors the source-routing pattern internal/failsim's (unexported)
// rebuildExcluding uses to reconstruct a post-failure snapshot from a flat
// entity list — the same plumbing, run here for addition instead of
// removal. It is snapshot-reconstruction bookkeeping, not impact analysis:
// the actual "what breaks" computation (connectivity, quorum, Ceph,
// management-path) is still entirely delegated to failsim.Simulate, called
// on the snapshot this function returns.
func augmentSnapshot(base inventory.Snapshot, extraGuests []inventory.Entity) inventory.Snapshot {
	g := inventory.NewGraph()

	hostByNode := map[string][]inventory.Entity{}
	bySource := map[inventory.Source][]inventory.Entity{}
	for _, e := range base.All() {
		ref := e.GetRef()
		switch ref.Kind {
		case inventory.KindPhysNic, inventory.KindBond, inventory.KindBridge,
			inventory.KindVlan, inventory.KindOVSBridge, inventory.KindOVSBond:
			hostByNode[ref.Node] = append(hostByNode[ref.Node], e)
		case inventory.KindLldpNeighbor:
			bySource[inventory.SourceHostLLDP] = append(bySource[inventory.SourceHostLLDP], e)
		case inventory.KindNode:
			bySource[inventory.SourcePVECluster] = append(bySource[inventory.SourcePVECluster], e)
		case inventory.KindSDNZone, inventory.KindSDNVnet, inventory.KindSDNSubnet,
			inventory.KindSDNDnsZone, inventory.KindSDNDnsRecord:
			bySource[inventory.SourcePVESDN] = append(bySource[inventory.SourcePVESDN], e)
		case inventory.KindGuest, inventory.KindGuestNic:
			bySource[inventory.SourcePVEGuest] = append(bySource[inventory.SourcePVEGuest], e)
		case inventory.KindFwRuleset:
			bySource[inventory.SourcePVEFirewall] = append(bySource[inventory.SourcePVEFirewall], e)
		}
	}
	bySource[inventory.SourcePVEGuest] = append(bySource[inventory.SourcePVEGuest], extraGuests...)

	for node, ents := range hostByNode {
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, ents)
		g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, ents)
	}
	for src, ents := range bySource {
		g.ApplyPoll(src, inventory.Scope{}, ents)
	}
	return g.Snapshot()
}

// syntheticGuests builds n synthetic Guest+GuestNic entities for profile,
// placed on attachment.Node and wired to attachment.Name — the same shape a
// real GuestNic takes (see internal/inventory/attachment_test.go), so
// inventory's own linking resolves BridgeOrVnet exactly as it would for a
// live-polled guest. VMIDs start at syntheticVMIDBase so they cannot collide
// with a real guest's vmid (PVE vmids are capped well below this range).
func syntheticGuests(profile GuestProfile, n int) []inventory.Entity {
	const syntheticVMIDBase = 900000
	node := profile.Attachment.Node

	out := make([]inventory.Entity, 0, n*(1+max(1, profile.NICCount)))
	nics := profile.NICCount
	if nics < 1 {
		nics = 1
	}
	for i := 0; i < n; i++ {
		vmid := syntheticVMIDBase + i
		vmidStr := strconv.Itoa(vmid)
		guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmidStr}
		out = append(out, &inventory.Guest{
			Ref: guestRef, VMID: vmid, Node: node, Name: "whatif-synthetic-" + vmidStr,
			Type: "qemu", Status: "running",
		})
		for k := 0; k < nics; k++ {
			key := "net" + strconv.Itoa(k)
			out = append(out, &inventory.GuestNic{
				Ref:        inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: vmidStr + "/" + key},
				Guest:      guestRef,
				Key:        key,
				TargetName: profile.Attachment.Name,
			})
		}
	}
	return out
}
