// SPDX-License-Identifier: Apache-2.0

package runbook_test

// Shared inventory-graph builders for this package's tests. Mirrors
// internal/blueprint's testhelpers_test.go convention: build a minimal
// *inventory.Graph directly via ApplyPoll with hand-built entities, since
// Render is a pure function of an inventory.Snapshot (plus, for the
// fw-rule template, a ReadContext.FwAnalytics) — exercising it against a
// small, targeted graph is faster and clearer about exactly which case is
// under test than round-tripping through pvemock for every one.
// service_pvemock_test.go's round-trip test covers the real stack
// end-to-end, per T-4003's acceptance criterion 3.

import (
	"strconv"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func newGraph() *inventory.Graph {
	return inventory.NewGraph()
}

// addSdnZone ApplyPolls a single cluster-scoped SdnZone (source pve-sdn,
// scope: cluster, kind sdn-zone) — a standalone helper because zones are
// polled independently of vnets/subnets in real inventory ingest too.
func addSdnZone(g *inventory.Graph, id string) inventory.Ref {
	ref := inventory.Ref{Kind: inventory.KindSDNZone, ID: id}
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNZone}},
		[]inventory.Entity{&inventory.SdnZone{Ref: ref, ID: id}})
	return ref
}

// addSdnVnet ApplyPolls a single cluster-scoped SdnVnet referencing zone
// (which need not currently exist — that is exactly orphan_vnet's case).
func addSdnVnet(g *inventory.Graph, id, zone string) inventory.Ref {
	ref := inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNVnet}},
		[]inventory.Entity{&inventory.SdnVnet{Ref: ref, ID: id, Zone: zone}})
	return ref
}

// addBridge ApplyPolls a single VLAN-aware bridge on node, trunking vids.
func addBridge(g *inventory.Graph, node, name string, vids []inventory.VidRange) inventory.Ref {
	ref := inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}},
		[]inventory.Entity{&inventory.Bridge{Ref: ref, Name: name, VlanAware: true, Vids: vids}})
	return ref
}

// nicSpec is one guest+NIC to seed via addGuestsWithNics.
//
//nolint:govet // fieldalignment: small test-only fixture struct, declaration-order readability over packing
type nicSpec struct {
	typ        string // "qemu" | "lxc"
	vmid       int
	bridgeName string // nic.TargetName ("bridge=" style)
	vid        int    // nic.Vid
}

// addGuestsWithNics ApplyPolls every spec's guest, then every spec's
// GuestNic, each in ONE poll call per kind — ApplyPoll reconciles a
// scope by removing anything in-scope a poll omits (inventory.Graph.
// ApplyPoll's own doc comment), so two independent single-entity calls for
// the same node+kind would have the second retire the first; see this
// package's blueprint-testhelpers-alike comment above. BridgeOrVnet/
// EffectiveVid are deliberately NOT set directly on the NIC: they are
// recomputed by the graph's own link.go resolveGuestNic during ApplyPoll's
// resolve step (from TargetName/Vid) exactly as real ingest does, so a test
// fixture exercises the same resolution path production code does rather
// than a shortcut around it.
func addGuestsWithNics(g *inventory.Graph, node string, specs []nicSpec) []inventory.Ref {
	guests := make([]inventory.Entity, 0, len(specs))
	nics := make([]inventory.Entity, 0, len(specs))
	refs := make([]inventory.Ref, 0, len(specs))
	for _, s := range specs {
		vmidStr := strconv.Itoa(s.vmid)
		guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmidStr}
		guests = append(guests, &inventory.Guest{Ref: guestRef, Name: "g" + vmidStr, Type: s.typ, Node: node, Status: "running", VMID: s.vmid})

		nicRef := inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: vmidStr + "/net0"}
		nics = append(nics, &inventory.GuestNic{Ref: nicRef, Guest: guestRef, Key: "net0", TargetName: s.bridgeName, Vid: s.vid})
		refs = append(refs, guestRef)
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindGuest}}, guests)
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindGuestNic}}, nics)
	return refs
}

// addGuestFwRuleset ApplyPolls a guest-scoped FwRuleset (Ref per
// internal/change/params_fw.go's documented "guest/<kind>/<vmid>"
// convention, mirrored exactly from internal/change/apply_fw_test.go's own
// fwGuestRef helper) carrying rules.
func addGuestFwRuleset(g *inventory.Graph, node, typ string, vmid int, rules []inventory.FwRule) inventory.Ref {
	ref := inventory.Ref{Kind: inventory.KindFwRuleset, Node: node, ID: "guest/" + typ + "/" + strconv.Itoa(vmid)}
	g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindFwRuleset}},
		[]inventory.Entity{&inventory.FwRuleset{Ref: ref, Scope: inventory.FwScopeGuest, Enabled: true, Rules: rules}})
	return ref
}
