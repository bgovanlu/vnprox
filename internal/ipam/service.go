package ipam

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// pagedThreshold is the largest address count a subnet renders as a full
// grid (docs/features/ipam.md §2: "/24-and-smaller render as a color
// grid... larger subnets render as paged block summaries").
const pagedThreshold = 256

// PVEReader is the subset of *pve.Client this package needs: the SDN tree
// (to enumerate subnets and their owning zone/vnet), every configured IPAM
// plugin instance's allocation set (docs/features/ipam.md §1: "vnprox reads
// through PVE's plugin transparently"), and the QEMU guest-agent
// network-get-interfaces read (the guest-agent-reported-IP enrichment
// source). Mirrors internal/sdn.PVEReader's "small interface, real type
// satisfies it" seam.
type PVEReader interface {
	ListSDNZones(ctx context.Context) ([]pve.SDNZone, error)
	ListSDNVnets(ctx context.Context) ([]pve.SDNVnet, error)
	ListSDNSubnets(ctx context.Context, vnet string) ([]pve.SDNSubnet, error)
	ListIPAMs(ctx context.Context) ([]pve.IPAM, error)
	GetIPAMStatus(ctx context.Context, ipam string) ([]pve.IPAMEntry, error)
	GetGuestAgentInterfaces(ctx context.Context, node string, vmid int) ([]pve.AgentIface, error)
}

// InventorySource is the seam this package uses for cluster-wide guest and
// bridge data (docs/architecture.md's "small interface" convention already
// used by internal/change.InventorySource) — *inventory.Graph satisfies it
// via its existing Snapshot method.
type InventorySource interface {
	Snapshot() inventory.Snapshot
}

// NeighborSource is the documented interface point for ARP/neighbor-table
// enrichment via the peer API (docs/features/ipam.md §1: "ARP/neighbor
// tables via peer API"). It is not wired to a concrete collector in T-405 —
// see this package's completion report for why (no acceptance criterion
// exercises it, and guest-agent observations alone already exercise every
// confidence label and every conflict type; a node-local ARP-cache reader
// plus peer-API fan-out is a right-sized standalone follow-up). A nil
// NeighborSource contributes no observations — Service treats that
// identically to "no neighbors currently seen", not an error.
type NeighborSource interface {
	Neighbors(ctx context.Context) ([]Observation, error)
}

// LeaseSource is the documented interface point for DHCP-lease enrichment,
// explicitly deferred to T-406 (dnsmasq lease-file parsing) per this task's
// card. A nil LeaseSource contributes no observations.
type LeaseSource interface {
	Leases(ctx context.Context) ([]Observation, error)
}

// Config configures a Service. PVE and Inventory are required; Neighbors/
// Leases are optional enrichment sources (nil contributes nothing — see
// their doc comments above).
type Config struct {
	PVE       PVEReader
	Inventory InventorySource
	Neighbors NeighborSource
	Leases    LeaseSource
	Now       func() time.Time
}

// Service builds docs/api.md's GET /ipam/subnets and
// GET /ipam/subnets/{cidr}/allocations responses.
type Service struct {
	pve       PVEReader
	inv       InventorySource
	neighbors NeighborSource
	leases    LeaseSource
	now       func() time.Time
}

// NewService builds a Service from cfg.
func NewService(cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pve: cfg.PVE, inv: cfg.Inventory, neighbors: cfg.Neighbors, leases: cfg.Leases, now: now}
}

// sdnSubnetInfo is one SDN subnet's context (zone/vnet/gateway/DHCP),
// gathered from PVE's live SDN tree — mirrors internal/sdn.Service's own
// "read PVE directly and live on every request" approach (docs/api.md's
// GET /sdn doc comment) so IPAM's view is never stale relative to what a
// reserve/release apply would actually change (T-405 acceptance criterion
// 3: "apply -> grid updates").
type sdnSubnetInfo struct {
	cidr    string
	zone    string
	vnet    string
	gateway string
	dhcp    bool
}

// sdnSubnets fetches every configured SDN subnet's context, live from PVE.
func (s *Service) sdnSubnets(ctx context.Context) ([]sdnSubnetInfo, error) {
	zones, err := s.pve.ListSDNZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipam: listing SDN zones: %w", err)
	}
	vnets, err := s.pve.ListSDNVnets(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipam: listing SDN vnets: %w", err)
	}
	zoneByID := make(map[string]string, len(zones))
	for _, z := range zones {
		zoneByID[z.ID] = z.ID
	}

	var out []sdnSubnetInfo
	for _, v := range vnets {
		subnets, err := s.pve.ListSDNSubnets(ctx, v.ID)
		if err != nil {
			// One vnet's subnets failing to list shouldn't blank the whole
			// view — matches internal/sdn.Service.Tree's per-zone
			// status-lookup tolerance.
			continue
		}
		for _, sub := range subnets {
			out = append(out, sdnSubnetInfo{
				cidr: sub.CIDR, zone: v.Zone, vnet: v.ID, gateway: sub.Gateway,
				dhcp: sub.DHCPRangeStart != "" && sub.DHCPRangeEnd != "",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].cidr < out[j].cidr })
	return out, nil
}

// allocationsByCIDR fetches every configured IPAM plugin's full allocation
// set and buckets entries by their owning subnet CIDR.
func (s *Service) allocationsByCIDR(ctx context.Context) (map[string][]Allocation, error) {
	plugins, err := s.pve.ListIPAMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("ipam: listing IPAM plugin instances: %w", err)
	}
	out := map[string][]Allocation{}
	for _, p := range plugins {
		entries, err := s.pve.GetIPAMStatus(ctx, p.ID)
		if err != nil {
			// A misconfigured/unreachable external plugin (NetBox, phpIPAM)
			// shouldn't blank every other plugin's data.
			continue
		}
		for _, e := range entries {
			out[e.Subnet] = append(out[e.Subnet], pveEntryToAllocation(e))
		}
	}
	return out, nil
}

// agentObservations gathers guest-agent-reported IPs cluster-wide: every
// qemu guest's network-get-interfaces read (LXC has no guest-agent
// equivalent route — see pve.Client.GetGuestAgentInterfaces' doc comment).
// A guest whose agent is unreachable (not installed, not running, guest
// stopped — overwhelmingly the common case, not a fault) simply
// contributes no observations; the read error is not surfaced.
func (s *Service) agentObservations(ctx context.Context, snap inventory.Snapshot) []Observation {
	var out []Observation
	for _, e := range snap.All() {
		g, ok := e.(*inventory.Guest)
		if !ok || g.Type != "qemu" {
			continue
		}
		ifaces, err := s.pve.GetGuestAgentInterfaces(ctx, g.Node, g.VMID)
		if err != nil {
			continue
		}
		guestRef := g.String()
		for _, iface := range ifaces {
			for _, addr := range iface.IPAddresses {
				if addr.IPAddress == "" || isLoopback(addr.IPAddress) {
					continue
				}
				out = append(out, Observation{
					IP: addr.IPAddress, MAC: iface.HardwareAddr,
					Hostname: g.Name, GuestRef: guestRef, Source: "guest-agent",
				})
			}
		}
	}
	return out
}

func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// enrichmentObservations gathers every wired enrichment source's
// observations: guest-agent (always), plus the optional NeighborSource/
// LeaseSource when configured (both nil in T-405's own wiring — see their
// doc comments on Config).
func (s *Service) enrichmentObservations(ctx context.Context, snap inventory.Snapshot) []Observation {
	out := s.agentObservations(ctx, snap)
	if s.neighbors != nil {
		if obs, err := s.neighbors.Neighbors(ctx); err == nil {
			out = append(out, obs...)
		}
	}
	if s.leases != nil {
		if obs, err := s.leases.Leases(ctx); err == nil {
			out = append(out, obs...)
		}
	}
	return out
}

// knownGuestsFromSnapshot builds the allocated_dark conflict check's
// "does this VMID/MAC correspond to a real guest" index.
func knownGuestsFromSnapshot(snap inventory.Snapshot) knownGuests {
	k := knownGuests{vmids: map[int]bool{}, macs: map[string]bool{}}
	for _, e := range snap.All() {
		switch ent := e.(type) {
		case *inventory.Guest:
			k.vmids[ent.VMID] = true
		case *inventory.GuestNic:
			if ent.Mac != "" {
				k.macs[normMAC(ent.Mac)] = true
			}
		}
	}
	return k
}

func observationsForCIDR(cidr string, obs []Observation) []Observation {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	var out []Observation
	for _, o := range obs {
		ip := net.ParseIP(o.IP)
		if ip != nil && ipnet.Contains(ip) {
			out = append(out, o)
		}
	}
	return out
}
