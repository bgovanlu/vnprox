package change

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// sdnAffectedZones collects, in first-appearance order, every zone id this
// changeset's sdn.* ops touch (directly via a zone op, or transitively via
// a vnet/subnet op naming it) — the set StepSDNApply's post-apply health
// verification checks (docs/features/sdn.md §4: "verification that each
// node's status reports the zone healthy"). Resolution for vnet/subnet
// update/delete ops (whose params carry no zone/vnet field of their own —
// see params_sdn.go's doc comments) uses only what this same changeset's
// own ops establish (the vnet-id-embeds-zone convention, "zone1/vnet1")
// plus a vnet-to-zone map built from this changeset's own vnet.create ops;
// a subnet op naming a vnet this changeset does not also touch is a known,
// documented gap (that zone is simply not verified) rather than a live PVE
// lookup — see the T-402 report.
func sdnAffectedZones(ops []Op) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}

	vnetZone := map[string]string{}
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnZoneCreateParams:
			add(op.Target.ID)
		case *SdnZoneUpdateParams:
			add(op.Target.ID)
		case *SdnZoneDeleteParams:
			add(op.Target.ID)
		case *SdnVnetCreateParams:
			vnetZone[op.Target.ID] = p.Zone
			add(p.Zone)
		case *SdnVnetUpdateParams:
			if zone := zoneFromVnetID(op.Target.ID); zone != "" {
				add(zone)
			}
		case *SdnVnetDeleteParams:
			if zone := zoneFromVnetID(op.Target.ID); zone != "" {
				add(zone)
			}
		}
	}
	for _, op := range ops {
		p, ok := op.Params.(*SdnSubnetCreateParams)
		if !ok {
			continue
		}
		if zone, ok := vnetZone[p.Vnet]; ok {
			add(zone)
		} else if zone := zoneFromVnetID(p.Vnet); zone != "" {
			add(zone)
		}
	}

	sort.Strings(out)
	return out
}

// zoneFromVnetID parses the "zone/vnet" convention op.go's SdnVnetCreateParams
// doc comment documents (e.g. "zone1/vnet1" -> "zone1"), or "" if vnetID
// carries no "/".
func zoneFromVnetID(vnetID string) string {
	if i := strings.IndexByte(vnetID, '/'); i > 0 {
		return vnetID[:i]
	}
	return ""
}

// resolveSubnetVnet resolves the owning vnet for a subnet target (needed by
// PVEGateway.SDNStageOp for sdn.subnet.update/delete, whose params carry no
// vnet field): first from an earlier sdn.subnet.create op in this same
// changeset targeting the same cidr (the subnet was created and then
// touched again in one changeset), else from the live inventory snapshot's
// SdnSubnet entity (the common case — a pre-existing subnet). Returns "" if
// neither resolves (the gateway call then fails cleanly with a clear
// PVE-side "vnet not found" error rather than panicking).
func resolveSubnetVnet(ops []Op, uptoIdx int, snap inventory.Snapshot, cidr string) string {
	for i := 0; i < uptoIdx && i < len(ops); i++ {
		if p, ok := ops[i].Params.(*SdnSubnetCreateParams); ok && ops[i].Target.ID == cidr {
			return p.Vnet
		}
	}
	if e, ok := snap.Get(inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr}); ok {
		if s, ok := e.(*inventory.SdnSubnet); ok {
			return s.Vnet
		}
	}
	return ""
}

// sdnRestoreOp pairs one inverse Op sdnRestoreOps computed with the vnet
// hint PVEGateway.SDNStageOp needs to execute a subnet op (see that
// interface method's doc comment) — carried alongside rather than
// re-derived, since sdnRestoreOps already has the owning vnet in hand from
// whichever SDNConfig side (pre or current) it read the subnet from.
type sdnRestoreOp struct {
	op   Op
	vnet string
}

// sdnRestoreOps computes the inverse zone/vnet/subnet ops that transform
// current back into pre, in PVE's own dependency order: removals of
// whatever current has that pre doesn't (deepest entity first — subnet,
// vnet, zone, since a subnet must be gone before its vnet, a vnet before
// its zone), then recreations of whatever pre has that current doesn't
// (shallowest first — zone, vnet, subnet, the reverse), then field updates
// for entities present in both but changed. This is T-402's SDN rollback
// (docs/features/sdn.md §4 / the task card: "pre-snapshot of
// /etc/pve/sdn/*.cfg + re-apply"), the SDN-shaped counterpart of T-206's
// restoreOpsForNode diffing a node's pre-apply snapshot against its live
// file.
func sdnRestoreOps(pre, current SDNConfig) []sdnRestoreOp {
	preZones, curZones := zoneMapOf(pre.Zones), zoneMapOf(current.Zones)
	preVnets, curVnets := vnetMapOf(pre.Vnets), vnetMapOf(current.Vnets)
	preSubnets, curSubnets := subnetMapOf(pre.Subnets), subnetMapOf(current.Subnets)
	preFabrics, curFabrics := fabricMapOf(pre.Fabrics), fabricMapOf(current.Fabrics)

	var out []sdnRestoreOp

	// DNS (T-1204): inverse records before inverse subnets/vnets/zones in the
	// removal phase (a record must be gone before its DNS zone), and after
	// the recreations in phase 2 (a DNS zone must exist before its records) —
	// see sdnDnsRestoreOps' two return slices.
	dnsRemovals, dnsRecreations := sdnDnsRestoreOps(pre, current)
	out = append(out, dnsRemovals...)

	// Phase 1: remove additions, deepest first.
	for _, id := range sortedKeys(curSubnets) {
		if _, ok := preSubnets[id]; ok {
			continue
		}
		s := curSubnets[id]
		out = append(out, sdnRestoreOp{
			op:   Op{Type: OpSdnSubnetDelete, Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: id}, Params: &SdnSubnetDeleteParams{}},
			vnet: s.Vnet,
		})
	}
	for _, id := range sortedKeys(curVnets) {
		if _, ok := preVnets[id]; ok {
			continue
		}
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnVnetDelete, Target: inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}, Params: &SdnVnetDeleteParams{}}})
	}
	for _, id := range sortedKeys(curZones) {
		if _, ok := preZones[id]; ok {
			continue
		}
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnZoneDelete, Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: id}, Params: &SdnZoneDeleteParams{}}})
	}
	// A fabric is deleted last in this phase (after every zone that might
	// reference it via its own `--fabric` field is already gone) — the
	// mirror image of phase 2's fabric-created-first ordering below.
	for _, id := range sortedKeys(curFabrics) {
		if _, ok := preFabrics[id]; ok {
			continue
		}
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnFabricDelete, Target: inventory.Ref{Kind: inventory.KindSDNFabric, ID: id}, Params: &SdnFabricDeleteParams{}}})
	}

	// Phase 2: recreate removals, shallowest first. A fabric is recreated
	// before any zone that might reference it via `--fabric`.
	for _, id := range sortedKeys(preFabrics) {
		if _, ok := curFabrics[id]; ok {
			continue
		}
		f := preFabrics[id]
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnFabricCreate, Target: inventory.Ref{Kind: inventory.KindSDNFabric, ID: id}, Params: &SdnFabricCreateParams{
			Protocol: f.Protocol, IPPrefix: f.IPPrefix, IP6Prefix: f.IP6Prefix,
			CSNPInterval: f.CSNPInterval, HelloInterval: f.HelloInterval, RouteFilter: f.RouteFilter,
			Area: f.Area, Redistribute: f.Redistribute, PersistentKeepalive: f.PersistentKeepalive,
		}}})
	}
	for _, id := range sortedKeys(preZones) {
		if _, ok := curZones[id]; ok {
			continue
		}
		z := preZones[id]
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnZoneCreate, Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: id}, Params: &SdnZoneCreateParams{
			Type: z.Type, Bridge: z.Bridge, Controller: z.Controller, Nodes: z.Nodes, ExitNodes: z.ExitNodes, Peers: z.Peers, VrfVxlan: z.VrfVxlan, MTU: z.MTU,
		}}})
	}
	for _, id := range sortedKeys(preVnets) {
		if _, ok := curVnets[id]; ok {
			continue
		}
		v := preVnets[id]
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnVnetCreate, Target: inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}, Params: &SdnVnetCreateParams{
			Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware,
		}}})
	}
	for _, id := range sortedKeys(preSubnets) {
		if _, ok := curSubnets[id]; ok {
			continue
		}
		s := preSubnets[id]
		out = append(out, sdnRestoreOp{
			op: Op{Type: OpSdnSubnetCreate, Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: id}, Params: &SdnSubnetCreateParams{
				Vnet: s.Vnet, CIDR: id, Gateway: s.Gateway, DNSZonePrefix: s.DNSZonePrefix, DHCPRanges: s.DHCPRanges, SNAT: s.SNAT,
			}},
			vnet: s.Vnet,
		})
	}

	// Phase 3: restore changed fields on entities present in both.
	for _, id := range sortedKeys(preZones) {
		cz, ok := curZones[id]
		if !ok || reflect.DeepEqual(preZones[id], cz) {
			continue
		}
		z := preZones[id]
		bridge, controller, nodes, exitNodes, peers, vrf, mtu := z.Bridge, z.Controller, z.Nodes, z.ExitNodes, z.Peers, z.VrfVxlan, z.MTU
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnZoneUpdate, Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: id}, Params: &SdnZoneUpdateParams{
			Bridge: &bridge, Controller: &controller, Nodes: &nodes, ExitNodes: &exitNodes, Peers: &peers, VrfVxlan: &vrf, MTU: &mtu,
		}}})
	}
	for _, id := range sortedKeys(preVnets) {
		cv, ok := curVnets[id]
		if !ok || reflect.DeepEqual(preVnets[id], cv) {
			continue
		}
		v := preVnets[id]
		alias, tag, vlanAware := v.Alias, v.Tag, v.VlanAware
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnVnetUpdate, Target: inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}, Params: &SdnVnetUpdateParams{
			Alias: &alias, Tag: &tag, VlanAware: &vlanAware,
		}}})
	}
	for _, id := range sortedKeys(preSubnets) {
		cs, ok := curSubnets[id]
		if !ok || reflect.DeepEqual(preSubnets[id], cs) {
			continue
		}
		s := preSubnets[id]
		gw, dnsPrefix, ranges, snat := s.Gateway, s.DNSZonePrefix, s.DHCPRanges, s.SNAT
		out = append(out, sdnRestoreOp{
			op: Op{Type: OpSdnSubnetUpdate, Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: id}, Params: &SdnSubnetUpdateParams{
				Gateway: &gw, DNSZonePrefix: &dnsPrefix, DHCPRanges: &ranges, SNAT: &snat,
			}},
			vnet: s.Vnet,
		})
	}

	// Phase 3 (fabrics): restore changed fields on fabrics present in both.
	for _, id := range sortedKeys(preFabrics) {
		cf, ok := curFabrics[id]
		if !ok || reflect.DeepEqual(preFabrics[id], cf) {
			continue
		}
		f := preFabrics[id]
		ipPrefix, ip6Prefix := f.IPPrefix, f.IP6Prefix
		csnp, hello := f.CSNPInterval, f.HelloInterval
		routeFilter, area := f.RouteFilter, f.Area
		redistribute := f.Redistribute
		keepalive := f.PersistentKeepalive
		out = append(out, sdnRestoreOp{op: Op{Type: OpSdnFabricUpdate, Target: inventory.Ref{Kind: inventory.KindSDNFabric, ID: id}, Params: &SdnFabricUpdateParams{
			IPPrefix: &ipPrefix, IP6Prefix: &ip6Prefix, CSNPInterval: &csnp, HelloInterval: &hello,
			RouteFilter: &routeFilter, Area: &area, Redistribute: &redistribute, PersistentKeepalive: &keepalive,
		}}})
	}

	out = append(out, dnsRecreations...)

	return out
}

func fabricMapOf(fs []SDNFabricConfig) map[string]SDNFabricConfig {
	m := make(map[string]SDNFabricConfig, len(fs))
	for _, f := range fs {
		m[f.ID] = f
	}
	return m
}

// sdnDnsRestoreOps computes the inverse DNS ops (T-1204) that transform
// current back into pre, split into removals (issued before the zone/vnet/
// subnet removals so a record is gone before its zone) and recreations
// (issued after the zone/vnet/subnet recreations so a zone exists before its
// records). Field-only changes to a surviving zone/record are emitted with
// the recreations. Mirrors the zone/vnet/subnet dependency-ordering the rest
// of sdnRestoreOps uses.
func sdnDnsRestoreOps(pre, current SDNConfig) (removals, recreations []sdnRestoreOp) {
	preZones, curZones := dnsZoneMapOf(pre.DnsZones), dnsZoneMapOf(current.DnsZones)
	preRecs, curRecs := dnsRecordMapOf(pre.DnsRecords), dnsRecordMapOf(current.DnsRecords)

	// Removals: records current added, then zones current added (deepest
	// first).
	for _, id := range sortedKeys(curRecs) {
		if _, ok := preRecs[id]; ok {
			continue
		}
		removals = append(removals, sdnRestoreOp{op: Op{Type: OpSdnDnsRecordDelete, Target: inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: id}, Params: &SdnDnsRecordDeleteParams{}}})
	}
	for _, id := range sortedKeys(curZones) {
		if _, ok := preZones[id]; ok {
			continue
		}
		removals = append(removals, sdnRestoreOp{op: Op{Type: OpSdnDnsZoneDelete, Target: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: id}, Params: &SdnDnsZoneDeleteParams{}}})
	}

	// Recreations: zones pre had that current removed (shallowest first),
	// then records, then field updates on survivors.
	for _, id := range sortedKeys(preZones) {
		if _, ok := curZones[id]; ok {
			continue
		}
		z := preZones[id]
		recreations = append(recreations, sdnRestoreOp{op: Op{Type: OpSdnDnsZoneCreate, Target: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: id}, Params: &SdnDnsZoneCreateParams{DNS: z.DNS, TTL: z.TTL}}})
	}
	for _, id := range sortedKeys(preRecs) {
		if _, ok := curRecs[id]; ok {
			continue
		}
		r := preRecs[id]
		recreations = append(recreations, sdnRestoreOp{op: Op{Type: OpSdnDnsRecordCreate, Target: inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: id}, Params: &SdnDnsRecordCreateParams{Zone: r.Zone, Name: r.Name, Type: r.Type, Value: r.Value, TTL: r.TTL}}})
	}
	for _, id := range sortedKeys(preZones) {
		cz, ok := curZones[id]
		if !ok || reflect.DeepEqual(preZones[id], cz) {
			continue
		}
		z := preZones[id]
		dns, ttl := z.DNS, z.TTL
		recreations = append(recreations, sdnRestoreOp{op: Op{Type: OpSdnDnsZoneUpdate, Target: inventory.Ref{Kind: inventory.KindSDNDnsZone, ID: id}, Params: &SdnDnsZoneUpdateParams{DNS: &dns, TTL: &ttl}}})
	}
	for _, id := range sortedKeys(preRecs) {
		cr, ok := curRecs[id]
		if !ok || reflect.DeepEqual(preRecs[id], cr) {
			continue
		}
		r := preRecs[id]
		value, ttl := r.Value, r.TTL
		recreations = append(recreations, sdnRestoreOp{op: Op{Type: OpSdnDnsRecordUpdate, Target: inventory.Ref{Kind: inventory.KindSDNDnsRecord, ID: id}, Params: &SdnDnsRecordUpdateParams{Value: &value, TTL: &ttl}}})
	}
	return removals, recreations
}

func dnsZoneMapOf(zs []SDNDnsZoneConfig) map[string]SDNDnsZoneConfig {
	m := make(map[string]SDNDnsZoneConfig, len(zs))
	for _, z := range zs {
		m[z.ID] = z
	}
	return m
}

func dnsRecordMapOf(rs []SDNDnsRecordConfig) map[string]SDNDnsRecordConfig {
	m := make(map[string]SDNDnsRecordConfig, len(rs))
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}

func zoneMapOf(zs []SDNZoneConfig) map[string]SDNZoneConfig {
	m := make(map[string]SDNZoneConfig, len(zs))
	for _, z := range zs {
		m[z.ID] = z
	}
	return m
}

func vnetMapOf(vs []SDNVnetConfig) map[string]SDNVnetConfig {
	m := make(map[string]SDNVnetConfig, len(vs))
	for _, v := range vs {
		m[v.ID] = v
	}
	return m
}

func subnetMapOf(ss []SDNSubnetConfig) map[string]SDNSubnetConfig {
	m := make(map[string]SDNSubnetConfig, len(ss))
	for _, s := range ss {
		m[s.ID] = s
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// restoreSDN reverts a changeset's SDN portion back to pre (its pre-apply
// snapshot's SDNConfig): reads the current staged config, diffs it against
// pre (sdnRestoreOps), issues the inverse ops, and re-applies so PVE's
// running config converges to pre too — T-402's card: "rollback semantics
// for SDN: pre-snapshot of /etc/pve/sdn/*.cfg + re-apply, reusing T-205's
// rollback machinery". The re-apply always runs (even if there were zero
// inverse ops to issue) so any leftover Pending marker this changeset's own
// (failed) attempt left behind is cleared too.
//
// Best-effort: every inverse op is attempted even if an earlier one
// failed (mirroring restoreAll's per-node best-effort contract for node
// files), and the single returned RollbackLog entry's Status/Error
// summarizes the outcome for the apply log. pveGW == nil (no live PVE
// session — the auto-rollback-timer path, which runs with no user session
// at all) is reported as a failed-but-non-fatal entry rather than a panic:
// SDN config, unlike a node's own interfaces file, has no daemon-level
// write path that doesn't need a PVE ticket (docs/architecture.md §6: "no
// privilege escalation through vnprox") — see the T-402 report's flagged
// gap.
func (s *Service) restoreSDN(ctx context.Context, pveGW PVEGateway, pre SDNConfig) RollbackLog {
	rb := RollbackLog{
		Summary: "Restore /etc/pve/sdn/*.cfg from pre-apply snapshot and re-apply",
		At:      s.now().Unix(),
		Status:  StepOK,
	}
	if pveGW == nil {
		rb.Status = StepFailed
		rb.Error = "no PVE gateway available to restore SDN config (no live user session for this rollback)"
		return rb
	}

	current, err := pveGW.SDNConfig(ctx)
	if err != nil {
		rb.Status = StepFailed
		rb.Error = fmt.Sprintf("reading current sdn config: %v", err)
		return rb
	}

	inverse := sdnRestoreOps(pre, current)
	var failures []string
	for _, ro := range inverse {
		if err := pveGW.SDNStageOp(ctx, ro.op, ro.vnet); err != nil {
			failures = append(failures, fmt.Sprintf("%s %s: %v", ro.op.Type, ro.op.Target.ID, err))
		}
	}

	// Only verify zones pre still has: a zone current has but pre doesn't
	// (created by the apply attempt being rolled back) was just deleted by
	// the inverse ops above and no longer exists to query status for —
	// verifying it would 404, not report unhealthy.
	if _, err := pveGW.ApplySDN(ctx, zoneIDsOf(pre.Zones)); err != nil {
		failures = append(failures, fmt.Sprintf("re-applying reverted sdn config: %v", err))
	}

	if len(failures) > 0 {
		rb.Status = StepFailed
		rb.Error = strings.Join(failures, "; ")
	}
	return rb
}

func zoneIDsOf(zs []SDNZoneConfig) []string {
	out := make([]string, len(zs))
	for i, z := range zs {
		out[i] = z.ID
	}
	return out
}
