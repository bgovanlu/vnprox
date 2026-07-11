// Package ipam implements subnet/allocation views and the PVE IPAM plugin
// bridge (docs/features/ipam.md, docs/api.md's /ipam routes): reading PVE's
// configured IPAM plugin(s) transparently, merging in enrichment
// observations (guest agent-reported IPs today; ARP/neighbor and DHCP-lease
// sources are documented interface points not yet wired — see this
// package's completion report), and surfacing the merge as confidence
// labels plus conflict-detection health findings with suggested
// resolutions.
package ipam

import (
	"net"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// Allocation is one PVE-IPAM-sourced address record for a subnet — the
// package-local, subnet-scoped view of pve.IPAMEntry (Subnet/Zone/Vnet
// already known from context, so they're dropped here).
type Allocation struct {
	IP       string
	MAC      string
	Hostname string
	VMID     int
	Gateway  bool
}

// Observation is one enrichment-source sighting of an address: today,
// exclusively guest-agent-reported (docs/features/ipam.md §1). NeighborSource
// (ARP/peer-API) and LeaseSource (DHCP, deferred to T-406) are the
// documented interface points for the two enrichment sources this package
// does not yet populate — see service.go's doc comments on those types.
type Observation struct {
	IP       string
	MAC      string
	Hostname string
	GuestRef string
	Source   string // "guest-agent" | "neighbor" (reserved for a future NeighborSource wiring)
}

// knownGuests is the inventory-derived "does this VMID/MAC correspond to a
// real, currently-known guest" check the allocated_dark conflict needs.
type knownGuests struct {
	vmids map[int]bool
	macs  map[string]bool // normalized (lower-cased) MAC -> known
}

func normMAC(mac string) string { return strings.ToLower(strings.TrimSpace(mac)) }

func (k knownGuests) hasVMID(vmid int) bool {
	if vmid == 0 {
		return false
	}
	return k.vmids[vmid]
}

func (k knownGuests) hasMAC(mac string) bool {
	if mac == "" {
		return false
	}
	return k.macs[normMAC(mac)]
}

// mergeSubnet merges allocs and obs (both already filtered to one subnet)
// into a per-IP Cell map plus the conflict findings detected along the way.
// gatewayIP, when non-empty, forces that address to CellGateway regardless
// of what else is known about it (an allocation with Gateway=true already
// carries this, but a subnet's declared gateway with no explicit IPAM
// gateway record should still render as gateway, not free).
func mergeSubnet(allocs []Allocation, obs []Observation, known knownGuests, gatewayIP string) (map[string]Cell, []Conflict) {
	byIP := map[string]*mergeEntry{}
	entry := func(ip string) *mergeEntry {
		e, ok := byIP[ip]
		if !ok {
			e = &mergeEntry{}
			byIP[ip] = e
		}
		return e
	}

	for _, a := range allocs {
		e := entry(a.IP)
		e.alloc = append(e.alloc, a)
	}
	for _, o := range obs {
		e := entry(o.IP)
		e.obs = append(e.obs, o)
	}
	if gatewayIP != "" {
		entry(gatewayIP).forceGateway = true
	}

	cells := make(map[string]Cell, len(byIP))
	var conflicts []Conflict
	for ip, e := range byIP {
		cell, cf := e.resolve(ip, known)
		cells[ip] = cell
		conflicts = append(conflicts, cf...)
	}

	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Type != conflicts[j].Type {
			return conflicts[i].Type < conflicts[j].Type
		}
		return strings.Join(conflicts[i].IPs, ",") < strings.Join(conflicts[j].IPs, ",")
	})
	return cells, conflicts
}

type mergeEntry struct {
	alloc        []Allocation
	obs          []Observation
	forceGateway bool
}

// resolve turns one address's raw alloc/obs facts into its rendered Cell
// plus any conflict findings it produces (0, 1, or 2 — an address can be
// both a duplicate-IP and, independently, unallocated is impossible since
// duplicate-IP implies >=1 observation and no allocation is required for
// it, but an alloc/obs MAC mismatch is folded into duplicate_ip too, see
// below).
func (e *mergeEntry) resolve(ip string, known knownGuests) (Cell, []Conflict) {
	cell := Cell{IP: ip}

	var alloc *Allocation
	if len(e.alloc) > 0 {
		alloc = &e.alloc[0]
	}
	isGateway := e.forceGateway || (alloc != nil && alloc.Gateway)

	sources := map[string]bool{}
	if alloc != nil {
		sources["pve-ipam"] = true
		cell.Hostname, cell.MAC, cell.VMID = alloc.Hostname, alloc.MAC, alloc.VMID
	}
	for _, o := range e.obs {
		src := o.Source
		if src == "" {
			src = "guest-agent"
		}
		sources[src] = true
	}
	cell.Sources = sortedKeys(sources)

	if isGateway {
		cell.State = CellGateway
		if alloc != nil {
			cell.Confidence = ConfidenceAllocated
		}
		return cell, nil
	}

	distinctMACs := map[string]bool{}
	for _, o := range e.obs {
		if o.MAC != "" {
			distinctMACs[normMAC(o.MAC)] = true
		}
	}
	if len(e.obs) > 0 && cell.Hostname == "" {
		// Prefer the observation's own hostname/MAC/guest ref when no
		// allocation supplied one (the "observed" / pure-squatter case).
		cell.Hostname = e.obs[0].Hostname
		cell.GuestRef = e.obs[0].GuestRef
		if cell.MAC == "" {
			cell.MAC = e.obs[0].MAC
		}
	}

	switch {
	case len(distinctMACs) > 1:
		// duplicate_ip: two (or more) different guests independently
		// report the same address.
		cell.State = CellConflict
		cell.Confidence = ConfidenceConflict
		return cell, []Conflict{duplicateIPConflict(ip, e.obs)}

	case alloc != nil && len(e.obs) > 0:
		obsMAC := normMAC(e.obs[0].MAC)
		allocMAC := normMAC(alloc.MAC)
		if obsMAC == "" || allocMAC == "" || obsMAC == allocMAC {
			cell.State = CellAllocated
			cell.Confidence = ConfidenceBoth
			return cell, nil
		}
		// The allocation record and what's actually observed on the wire
		// disagree about who owns this address — also a duplicate_ip-shaped
		// conflict (the record and reality name different MACs).
		cell.State = CellConflict
		cell.Confidence = ConfidenceConflict
		return cell, []Conflict{allocObsMismatchConflict(ip, *alloc, e.obs[0])}

	case alloc != nil:
		cell.Confidence = ConfidenceAllocated
		if alloc.VMID > 0 {
			cell.State = CellAllocated
			if !known.hasVMID(alloc.VMID) && !known.hasMAC(alloc.MAC) {
				cell.State = CellConflict
				return cell, []Conflict{allocatedDarkConflict(ip, *alloc)}
			}
			return cell, nil
		}
		// No VMID: a manual reservation, not tied to any guest.
		cell.State = CellReserved
		return cell, nil

	case len(e.obs) > 0:
		cell.State = CellObserved
		cell.Confidence = ConfidenceObserved
		return cell, []Conflict{observedUnallocatedConflict(ip, e.obs[0])}

	default:
		cell.State = CellFree
		return cell, nil
	}
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func duplicateIPConflict(ip string, obs []Observation) Conflict {
	who := make([]string, 0, len(obs))
	seen := map[string]bool{}
	for _, o := range obs {
		label := o.Hostname
		if label == "" {
			label = o.GuestRef
		}
		if label == "" {
			label = o.MAC
		}
		if label != "" && !seen[label] {
			seen[label] = true
			who = append(who, label)
		}
	}
	return Conflict{
		Type:       "duplicate_ip",
		Severity:   "error",
		IPs:        []string{ip},
		Message:    "multiple guests report " + ip + ": " + strings.Join(who, ", "),
		Suggestion: "release the address from all but one guest (reassign the others to their own address), then reserve " + ip + " for the guest that should keep it",
	}
}

func allocObsMismatchConflict(ip string, alloc Allocation, obs Observation) Conflict {
	allocWho := alloc.Hostname
	if allocWho == "" {
		allocWho = alloc.MAC
	}
	obsWho := obs.Hostname
	if obsWho == "" {
		obsWho = obs.MAC
	}
	return Conflict{
		Type:       "duplicate_ip",
		Severity:   "error",
		IPs:        []string{ip},
		Message:    "IPAM reserves " + ip + " for " + allocWho + " but " + obsWho + " is actually using it",
		Suggestion: "release the stale reservation for " + allocWho + " and reserve " + ip + " for " + obsWho + " instead, or reassign " + obsWho + " off " + ip,
	}
}

func allocatedDarkConflict(ip string, alloc Allocation) Conflict {
	who := alloc.Hostname
	if who == "" {
		who = alloc.MAC
	}
	return Conflict{
		Type:       "allocated_dark",
		Severity:   "warning",
		IPs:        []string{ip},
		Message:    ip + " is reserved for " + who + ", which no longer corresponds to a known guest",
		Suggestion: "release " + ip + " if " + who + " was decommissioned, or verify its guest still exists",
	}
}

func observedUnallocatedConflict(ip string, obs Observation) Conflict {
	who := obs.Hostname
	if who == "" {
		who = obs.MAC
	}
	return Conflict{
		Type:       "observed_unallocated",
		Severity:   "warning",
		IPs:        []string{ip},
		Message:    ip + " is in active use by " + who + " but has no IPAM reservation",
		Suggestion: "reserve " + ip + " for " + who + " to bring IPAM in sync with reality",
	}
}

// subnetAddrCount returns the number of addresses a CIDR spans (2^(bits -
// ones)), and the prefix length, or ok=false if cidr does not parse.
func subnetAddrCount(cidr string) (count int, prefix int, ok bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, 0, false
	}
	ones, bits := ipnet.Mask.Size()
	if bits-ones >= 24 {
		// Cap: a /8 already has 16M addresses — count is only used for the
		// subnet-list utilization denominator and the <=/24 direct-render
		// threshold, both of which only care "is this small or huge", so a
		// capped count avoids materializing an absurd int for /8-and-larger.
		return 1 << 24, prefix, true
	}
	return 1 << (bits - ones), ones, true
}

// pveEntryToAllocation adapts one pve.IPAMEntry (already known to belong to
// the subnet being merged) into this package's Allocation.
func pveEntryToAllocation(e pve.IPAMEntry) Allocation {
	return Allocation{IP: e.IP, MAC: e.MAC, Hostname: e.Hostname, VMID: e.VMID, Gateway: e.Gateway}
}
