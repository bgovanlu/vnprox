package change

import (
	"encoding/json"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// sdnConfigDiffFiles renders the SDN portion of the review screen's config
// diff (T-2003, docs/features/change-management.md §3's "File diff" tab):
// before/after unified diffs of the zone/vnet/subnet families this
// changeset's sdn.* ops touch, at the same synthetic /etc/pve/sdn/*.cfg
// paths the pre-apply snapshot already uses (apply_snapshot.go's
// sdn*SnapshotPath) — so the review screen and the snapshot/restore
// machinery describe the very same three logical files, and a changeset
// spanning both node-file and SDN ops (T-2003 acceptance criterion 4) gets a
// complete config diff instead of silently covering only the interfaces-file
// half.
//
// "Before" is read from the live inventory snapshot — the same ground truth
// every other pre-apply validator reads (Service.inventorySnapshot) — and
// "after" is that same state with this changeset's own sdn.zone/vnet/subnet
// ops folded on top, field for field, mirroring exactly what
// internal/pve/sdn.go's write calls set from those same op params (the same
// projection style validate_sdn.go's effectiveZones/effectiveVnetZones/
// effectiveSubnets already use for validation, just carrying every field
// rather than only the ones a particular check needs).
//
// Deliberately reads no live PVE state (no PVEGateway parameter): the
// changeset-diff route has never required a live PVE session before (only
// apply/rollback do, for cluster-scope steps) — this keeps that invariant,
// so the review screen, and an AI operator's changesets.diff MCP call (T-1701,
// docs/architecture.md §13.1 — the frozen v1 manifest's one diff tool), can
// both see the SDN portion of a diff before a PVE session is even resolved.
func sdnConfigDiffFiles(ops []Op, snap inventory.Snapshot) []ifaces.FileDiff {
	if !opsTouchSDNConfig(ops) {
		return nil
	}
	beforeZones, afterZones := projectSDNZoneConfigs(ops, snap)
	beforeVnets, afterVnets := projectSDNVnetConfigs(ops, snap)
	beforeSubnets, afterSubnets := projectSDNSubnetConfigs(ops, snap)

	return []ifaces.FileDiff{
		sdnConfigFileDiff(sdnZonesSnapshotPath, beforeZones, afterZones),
		sdnConfigFileDiff(sdnVnetsSnapshotPath, beforeVnets, afterVnets),
		sdnConfigFileDiff(sdnSubnetsSnapshotPath, beforeSubnets, afterSubnets),
	}
}

// Note on vnet/subnet id spelling: this file's SDNVnetConfig/SDNSubnetConfig
// entries carry internal/change's own Ref.ID convention throughout
// ("<zone>/<vnet>" for a vnet, the bare CIDR for a subnet — params_sdn.go's
// doc comments), the same convention effectiveVnetZones/effectiveSubnets
// (validate_sdn.go) already treat as ground truth. This deliberately does
// NOT match the *apply-time* SDNConfig snapshot's id spelling (apply_snapshot.go's
// sdnConfigSnapshotFiles), which reads real PVE via PVEGateway.SDNConfig and so
// carries PVE's own bare-vnet-name wire ids (converted by pve.SDNVnetID/
// SDNSubnetID at that boundary — see those functions' doc comments for the
// pre-existing convention split this file inherits, not introduces). Both
// halves are internally self-consistent (a before/after pair from the same
// source), so the diff itself is correct; only the exact id string a vnet
// renders as can differ between this review-time diff and the pre-apply
// snapshot taken moments later at Apply.

// opsTouchSDNConfig reports whether ops contains any sdn.zone/vnet/subnet
// op — the same three families sdnConfigDiffFiles covers (sdn.apply carries
// no config of its own to diff, and the DNS op family — T-1204 — has no
// synthetic snapshot file of its own yet either, matching
// apply_snapshot.go's existing sdn*SnapshotPath scope).
func opsTouchSDNConfig(ops []Op) bool {
	for _, op := range ops {
		switch op.Type {
		case OpSdnZoneCreate, OpSdnZoneUpdate, OpSdnZoneDelete,
			OpSdnVnetCreate, OpSdnVnetUpdate, OpSdnVnetDelete,
			OpSdnSubnetCreate, OpSdnSubnetUpdate, OpSdnSubnetDelete:
			return true
		}
	}
	return false
}

// sdnConfigFileDiff renders one synthetic SDN config file's before/after as
// canonical indented JSON (never PVE's native cfg syntax — apply_snapshot.go's
// sdn*SnapshotPath doc comment explains why) and unified-diffs the two.
func sdnConfigFileDiff(path string, before, after any) ifaces.FileDiff {
	beforeText := canonicalJSON(before)
	afterText := canonicalJSON(after)
	unified := ifaces.UnifiedDiff(path, path, beforeText, afterText)
	return ifaces.FileDiff{Path: path, Unified: unified, Changed: unified != ""}
}

// canonicalJSON renders v as indented JSON with a trailing newline (so
// UnifiedDiff's line-oriented output ends cleanly). Every value this file
// passes in is one of this package's own plain SDNZoneConfig/SDNVnetConfig/
// SDNSubnetConfig slices — always marshalable; a failure here would be a
// programming error, not a runtime condition a caller could recover from, so
// it degrades to an empty string rather than propagating an error through a
// diff-rendering call chain that has none to give.
func canonicalJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b) + "\n"
}

func projectSDNZoneConfigs(ops []Op, snap inventory.Snapshot) (before, after []SDNZoneConfig) {
	m := map[string]SDNZoneConfig{}
	for _, e := range snap.All() {
		z, ok := e.(*inventory.SdnZone)
		if !ok {
			continue
		}
		m[z.ID] = SDNZoneConfig{
			ID: z.ID, Type: z.Type, Bridge: z.Bridge, Controller: z.Controller,
			Nodes: z.Nodes, ExitNodes: z.ExitNodes, Peers: z.Peers, VrfVxlan: z.VrfVxlan, MTU: z.MTU,
		}
	}
	before = sortedSDNZoneConfigs(m)

	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnZoneCreateParams:
			m[op.Target.ID] = SDNZoneConfig{
				ID: op.Target.ID, Type: p.Type, Bridge: p.Bridge, Controller: p.Controller,
				Nodes: p.Nodes, ExitNodes: p.ExitNodes, Peers: p.Peers, VrfVxlan: p.VrfVxlan, MTU: p.MTU,
			}
		case *SdnZoneUpdateParams:
			z := m[op.Target.ID]
			if p.Bridge != nil {
				z.Bridge = *p.Bridge
			}
			if p.Controller != nil {
				z.Controller = *p.Controller
			}
			if p.Nodes != nil {
				z.Nodes = *p.Nodes
			}
			if p.ExitNodes != nil {
				z.ExitNodes = *p.ExitNodes
			}
			if p.Peers != nil {
				z.Peers = *p.Peers
			}
			if p.VrfVxlan != nil {
				z.VrfVxlan = *p.VrfVxlan
			}
			if p.MTU != nil {
				z.MTU = *p.MTU
			}
			z.ID = op.Target.ID
			m[op.Target.ID] = z
		case *SdnZoneDeleteParams:
			delete(m, op.Target.ID)
		}
	}
	after = sortedSDNZoneConfigs(m)
	return before, after
}

func sortedSDNZoneConfigs(m map[string]SDNZoneConfig) []SDNZoneConfig {
	out := make([]SDNZoneConfig, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func projectSDNVnetConfigs(ops []Op, snap inventory.Snapshot) (before, after []SDNVnetConfig) {
	m := map[string]SDNVnetConfig{}
	for _, e := range snap.All() {
		v, ok := e.(*inventory.SdnVnet)
		if !ok {
			continue
		}
		m[v.ID] = SDNVnetConfig{ID: v.ID, Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware}
	}
	before = sortedSDNVnetConfigs(m)

	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnVnetCreateParams:
			m[op.Target.ID] = SDNVnetConfig{ID: op.Target.ID, Zone: p.Zone, Alias: p.Alias, Tag: p.Tag, VlanAware: p.VlanAware}
		case *SdnVnetUpdateParams:
			v := m[op.Target.ID]
			if p.Alias != nil {
				v.Alias = *p.Alias
			}
			if p.Tag != nil {
				v.Tag = *p.Tag
			}
			if p.VlanAware != nil {
				v.VlanAware = *p.VlanAware
			}
			v.ID = op.Target.ID
			m[op.Target.ID] = v
		case *SdnVnetDeleteParams:
			delete(m, op.Target.ID)
		}
	}
	after = sortedSDNVnetConfigs(m)
	return before, after
}

func sortedSDNVnetConfigs(m map[string]SDNVnetConfig) []SDNVnetConfig {
	out := make([]SDNVnetConfig, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func projectSDNSubnetConfigs(ops []Op, snap inventory.Snapshot) (before, after []SDNSubnetConfig) {
	m := map[string]SDNSubnetConfig{}
	for _, e := range snap.All() {
		sub, ok := e.(*inventory.SdnSubnet)
		if !ok {
			continue
		}
		m[sub.ID] = SDNSubnetConfig{ID: sub.ID, Vnet: sub.Vnet, Gateway: sub.Gateway, DNSZonePrefix: sub.DNSZonePrefix, DHCPRanges: sub.DHCPRanges, SNAT: sub.SNAT}
	}
	before = sortedSDNSubnetConfigs(m)

	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnSubnetCreateParams:
			m[op.Target.ID] = SDNSubnetConfig{ID: op.Target.ID, Vnet: p.Vnet, Gateway: p.Gateway, DNSZonePrefix: p.DNSZonePrefix, DHCPRanges: p.DHCPRanges, SNAT: p.SNAT}
		case *SdnSubnetUpdateParams:
			sub := m[op.Target.ID]
			if p.Gateway != nil {
				sub.Gateway = *p.Gateway
			}
			if p.DNSZonePrefix != nil {
				sub.DNSZonePrefix = *p.DNSZonePrefix
			}
			if p.DHCPRanges != nil {
				sub.DHCPRanges = *p.DHCPRanges
			}
			if p.SNAT != nil {
				sub.SNAT = *p.SNAT
			}
			sub.ID = op.Target.ID
			m[op.Target.ID] = sub
		case *SdnSubnetDeleteParams:
			delete(m, op.Target.ID)
		}
	}
	after = sortedSDNSubnetConfigs(m)
	return before, after
}

func sortedSDNSubnetConfigs(m map[string]SDNSubnetConfig) []SDNSubnetConfig {
	out := make([]SDNSubnetConfig, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
