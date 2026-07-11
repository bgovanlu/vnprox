package ipam

import (
	"context"
	"net"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Reservation is one DHCP-eligible subnet's MAC-bound static allocation
// (docs/features/sdn.md §5: "static reservations bound to guest MACs ...
// Reservations are IPAM allocations (ipam.alloc.create) so the IPAM grid
// and DHCP stay one dataset"). It is a filtered, derived view over the
// exact same PVE-IPAM allocation records Allocations/Subnets render as
// grid cells — DHCP builds it from the same allocationsByCIDR read those
// two methods use, never from a second stored copy. Mutating the
// underlying PVE-IPAM record (via ipam.alloc.create/delete) is therefore
// immediately visible from both this view and the grid, because there is
// only ever one record to begin with.
type Reservation struct {
	CIDR     string `json:"cidr"`
	Zone     string `json:"zone"`
	Vnet     string `json:"vnet"`
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	Hostname string `json:"hostname,omitempty"`
	GuestRef string `json:"guestRef,omitempty"`
	VMID     int    `json:"vmid,omitempty"`
}

// Lease is one live dnsmasq-observed DHCP lease (docs/features/sdn.md §5:
// "a live leases view (parsed per-node via peer API)"), correlated to a
// known guest by MAC when the lease's MAC matches a currently-known
// GuestNic (the same MAC-index approach the allocated_dark conflict check
// uses — see knownGuestsFromSnapshot).
type Lease struct {
	CIDR     string `json:"cidr"`
	Zone     string `json:"zone"`
	Vnet     string `json:"vnet"`
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	Hostname string `json:"hostname,omitempty"`
	GuestRef string `json:"guestRef,omitempty"`
}

// DHCPView is docs/api.md's `GET /sdn/dhcp` response: every DHCP-enabled
// SDN subnet's static reservations plus live leases, optionally scoped to
// one zone.
type DHCPView struct {
	Reservations []Reservation `json:"reservations"`
	Leases       []Lease       `json:"leases"`
	GeneratedAt  int64         `json:"generatedAt"`
}

// DHCP builds docs/api.md's `GET /sdn/dhcp` response for zone (every
// DHCP-enabled subnet cluster-wide when zone == ""). Reservations are
// derived directly from the same allocationsByCIDR PVE read
// Allocations/Subnets use — see Reservation's doc comment for why this
// guarantees "one dataset", not two independently-stored records. Leases
// come from the optional LeaseSource (T-406) — nil or an errored read
// simply yields no leases, the same soft-fail convention every other
// optional enrichment source in this package follows.
func (s *Service) DHCP(ctx context.Context, zone string) (DHCPView, error) {
	snap := s.inv.Snapshot()

	sdnInfo, err := s.sdnSubnets(ctx)
	if err != nil {
		return DHCPView{}, err
	}
	allocByCIDR, err := s.allocationsByCIDR(ctx)
	if err != nil {
		return DHCPView{}, err
	}

	dhcpSubnets := make(map[string]sdnSubnetInfo)
	for _, info := range sdnInfo {
		if !info.dhcp {
			continue
		}
		if zone != "" && info.zone != zone {
			continue
		}
		dhcpSubnets[info.cidr] = info
	}

	reservations := make([]Reservation, 0)
	for cidr, info := range dhcpSubnets {
		for _, a := range allocByCIDR[cidr] {
			if a.MAC == "" || a.Gateway {
				continue
			}
			reservations = append(reservations, Reservation{
				CIDR: cidr, Zone: info.zone, Vnet: info.vnet,
				IP: a.IP, MAC: a.MAC, Hostname: a.Hostname, VMID: a.VMID,
				GuestRef: guestRefForMAC(snap, a.MAC),
			})
		}
	}
	sort.Slice(reservations, func(i, j int) bool {
		if reservations[i].CIDR != reservations[j].CIDR {
			return reservations[i].CIDR < reservations[j].CIDR
		}
		return reservations[i].IP < reservations[j].IP
	})

	leases := make([]Lease, 0)
	if s.leases != nil {
		if obs, lerr := s.leases.Leases(ctx); lerr == nil {
			for _, o := range obs {
				cidr, info, ok := subnetForIP(dhcpSubnets, o.IP)
				if !ok {
					continue
				}
				leases = append(leases, Lease{
					CIDR: cidr, Zone: info.zone, Vnet: info.vnet,
					IP: o.IP, MAC: o.MAC, Hostname: o.Hostname,
					GuestRef: firstNonEmptyStr(o.GuestRef, guestRefForMAC(snap, o.MAC)),
				})
			}
		}
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].CIDR != leases[j].CIDR {
			return leases[i].CIDR < leases[j].CIDR
		}
		return leases[i].IP < leases[j].IP
	})

	return DHCPView{Reservations: reservations, Leases: leases, GeneratedAt: s.now().Unix()}, nil
}

// guestRefForMAC returns the Ref of the currently-known GuestNic whose MAC
// matches mac (case/whitespace-insensitive), or "" if none — the same
// lease/reservation-to-guest correlation-by-MAC the allocated_dark
// conflict check's knownGuests index already performs, exposed here as a
// direct lookup for DHCP's reservation/lease views.
func guestRefForMAC(snap inventory.Snapshot, mac string) string {
	if mac == "" {
		return ""
	}
	target := normMAC(mac)
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok || normMAC(nic.Mac) != target {
			continue
		}
		return nic.Guest.String()
	}
	return ""
}

// subnetForIP returns the DHCP-enabled subnet (from dhcpSubnets) whose
// CIDR contains ip, or ok=false if none does — a lease's raw IP is matched
// against subnet containment rather than a filename/zone tag, since
// host.DHCPLeases' reader has no reliable way to attribute a given lease
// file to one specific zone (see that package's doc comment).
func subnetForIP(dhcpSubnets map[string]sdnSubnetInfo, ip string) (string, sdnSubnetInfo, bool) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", sdnSubnetInfo{}, false
	}
	for cidr, info := range dhcpSubnets {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipnet.Contains(parsed) {
			return cidr, info, true
		}
	}
	return "", sdnSubnetInfo{}, false
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
