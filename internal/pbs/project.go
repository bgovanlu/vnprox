package pbs

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// Project builds an Overlay from a live inventory snapshot plus Status
// (Discover's output): for every backing-up node, resolves which interface
// on that node egresses toward its PBS server (egressCarrier), then reuses
// internal/topology.ResolvePhysicalPath — the exact carrier -> parent-bridge
// -> bond-slaves -> PhysNics resolver T-702's management-path visibility
// established — to report that backup path's bottleneck link speed and the
// bond/NIC it rides (this task's card: "reusing internal/topology's existing
// NIC-path resolution"). Pure with respect to snap/status — no I/O, the
// shape AC1's golden projection test exercises directly.
//
// A node is a backing-up node for a host H when an enabled backup job targets
// one of H's storages and applies to that node (its node restriction, and
// the storage's own node restriction, both permit it). Nodes/hosts/jobs are
// emitted in deterministic sorted order.
func Project(snap inventory.Snapshot, status Status) Overlay {
	storageAddr := map[string]string{}
	storageNodes := map[string][]string{}
	for _, s := range status.Storages {
		storageAddr[s.ID] = s.Address
		storageNodes[s.ID] = s.Nodes
	}
	datastoresByAddr := map[string][]string{}
	for _, h := range status.Hosts {
		datastoresByAddr[h.Address] = h.Datastores
	}

	// perHostNode[address][node] accumulates the storages and jobs that make
	// `node` back up to the PBS host at `address`.
	type acc struct {
		storageIDs map[string]bool
		jobs       []Job
	}
	perHostNode := map[string]map[string]*acc{}
	for _, j := range status.Jobs {
		addr, ok := storageAddr[j.Storage]
		if !ok {
			continue // job targets a non-PBS or unknown storage
		}
		for _, node := range scopeNodes(j, status.Nodes) {
			if !nodeAllowed(node, storageNodes[j.Storage]) {
				continue
			}
			byNode := perHostNode[addr]
			if byNode == nil {
				byNode = map[string]*acc{}
				perHostNode[addr] = byNode
			}
			a := byNode[node]
			if a == nil {
				a = &acc{storageIDs: map[string]bool{}}
				byNode[node] = a
			}
			a.storageIDs[j.Storage] = true
			a.jobs = append(a.jobs, j)
		}
	}

	var paths []BackupPath
	for _, h := range status.Hosts {
		byNode := perHostNode[h.Address]
		nodes := make([]string, 0, len(byNode))
		for node := range byNode {
			nodes = append(nodes, node)
		}
		sort.Strings(nodes)

		for _, node := range nodes {
			a := byNode[node]
			bp := BackupPath{
				Host:       h.Ref,
				Node:       node,
				StorageIDs: sortedKeys(a.storageIDs),
				Jobs:       jobSummaries(a.jobs),
			}
			if carrier, ok := egressCarrier(snap, node, h.Address); ok {
				bp.Carrier = carrier
				bp.Path, bp.NICs = topology.ResolvePhysicalPath(snap, carrier)
				bp.RidingOn = ridingRef(bp.Path, bp.NICs)
				bp.LinkMbps, bp.LinkKnown = bottleneckSpeed(snap, bp.NICs)
			}
			bp.SizingHint = sizingHint(node, h, datastoresByAddr[h.Address], bp)
			paths = append(paths, bp)
		}
	}

	return Overlay{Hosts: status.Hosts, Paths: paths}
}

// scopeNodes expands a job to the node names it applies to: exactly its Node
// restriction when set, else every cluster node (an unrestricted job backs up
// guests wherever they run).
func scopeNodes(j Job, all []string) []string {
	if strings.TrimSpace(j.Node) != "" {
		return []string{j.Node}
	}
	return all
}

// nodeAllowed reports whether node is permitted by a storage's node
// restriction (empty restriction = every node).
func nodeAllowed(node string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == node {
			return true
		}
	}
	return false
}

// egressCarrier resolves the Bridge/VlanIface on node whose traffic egresses
// toward the PBS server at address: first any carrier whose declared subnet
// directly contains the server IP (same-segment reachability), else — when
// the server is off-subnet (routed) or named by hostname — the node's
// default-gateway-bearing bridge (the routed egress). Deterministically first
// by Ref.String() within each tier. ok is false (zero Ref) when neither
// resolves — "unresolved", never a guess.
func egressCarrier(snap inventory.Snapshot, node, address string) (inventory.Ref, bool) {
	if ref, ok := carrierContainingIP(snap, node, address); ok {
		return ref, true
	}
	return gatewayCarrier(snap, node)
}

// carrierContainingIP finds the carrier on node whose declared address subnet
// contains the IP form of address (a bare host, or the host part of a
// "host:port"/CIDR). Returns false when address isn't an IP or nothing
// matches.
func carrierContainingIP(snap inventory.Snapshot, node, address string) (inventory.Ref, bool) {
	ip := parseServerIP(address)
	if ip == nil {
		return inventory.Ref{}, false
	}
	var matches []inventory.Ref
	for _, e := range snap.All() {
		ref := e.GetRef()
		if ref.Node != node {
			continue
		}
		addrs, ok := addressesOf(e)
		if !ok {
			continue
		}
		for _, a := range addrs {
			_, network, err := net.ParseCIDR(a)
			if err != nil {
				continue
			}
			if network.Contains(ip) {
				matches = append(matches, ref)
				break
			}
		}
	}
	return firstRef(matches)
}

// gatewayCarrier finds the node's default-route egress: the Bridge declaring
// a non-empty Gateway (the routed path a backup to an off-subnet or
// hostname-addressed PBS server takes). Deterministically first by Ref.
func gatewayCarrier(snap inventory.Snapshot, node string) (inventory.Ref, bool) {
	var matches []inventory.Ref
	for _, e := range snap.All() {
		ref := e.GetRef()
		if ref.Node != node {
			continue
		}
		if b, ok := e.(*inventory.Bridge); ok && strings.TrimSpace(b.Gateway) != "" {
			matches = append(matches, ref)
		}
	}
	return firstRef(matches)
}

// parseServerIP extracts an IP from a PBS storage server value, which may be
// a bare IP, an "ip:port"/"[ipv6]:port" pair, or a hostname (nil then).
func parseServerIP(address string) net.IP {
	address = strings.TrimSpace(address)
	if ip := net.ParseIP(address); ip != nil {
		return ip
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		return net.ParseIP(host)
	}
	return nil
}

// addressesOf returns e's declared CIDR addresses and whether e is a kind
// that declares an Addresses field at all (Bridge/VlanIface — the same two
// kinds internal/topology's own address handling recognizes).
func addressesOf(e inventory.Entity) ([]string, bool) {
	switch v := e.(type) {
	case *inventory.Bridge:
		return v.Addresses, true
	case *inventory.VlanIface:
		return v.Addresses, true
	default:
		return nil, false
	}
}

// bottleneckSpeed returns the slowest reported link speed (Mbps) across the
// terminal PhysNics of a path — the effective ceiling of a backup transfer.
// known is false when no terminal NIC reports a speed at all (an unpopulated
// or unresolved path), so the sizing hint says "unknown" rather than "0".
func bottleneckSpeed(snap inventory.Snapshot, nics []inventory.Ref) (int, bool) {
	min := 0
	for _, ref := range nics {
		e, ok := snap.Get(ref)
		if !ok {
			continue
		}
		p, ok := e.(*inventory.PhysNic)
		if !ok || p.SpeedMbps <= 0 {
			continue
		}
		if min == 0 || p.SpeedMbps < min {
			min = p.SpeedMbps
		}
	}
	return min, min > 0
}

// ridingRef reports the single ref a carrier's traffic "rides" for
// badge/inspector display: the first Bond in path, else the sole terminal
// PhysNic when path resolves directly to exactly one bare NIC. Zero Ref when
// there is no single answer — never guessed. (Same rule T-702's own path
// display uses.)
func ridingRef(path, nics []inventory.Ref) inventory.Ref {
	for _, r := range path {
		if r.Kind == inventory.KindBond {
			return r
		}
	}
	if len(nics) == 1 {
		return nics[0]
	}
	return inventory.Ref{}
}

// jobSummaries denormalizes a's jobs into deterministic, sorted summaries.
func jobSummaries(jobs []Job) []JobSummary {
	out := make([]JobSummary, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, JobSummary{
			ID:       j.ID,
			Storage:  j.Storage,
			Schedule: j.Schedule,
			Guests:   len(j.VMIDs),
			All:      j.All,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// sizingHint renders the deterministic, plain-English datastore-network
// sizing hint for one backup path (docs/features/topology.md §2, T-1206's
// inspector deliverable): which datastore(s) on which PBS server the node
// backs up to, over which resolved egress at what link speed, and a coarse
// summary of the jobs driving it. Explicitly flagged as a heuristic estimate
// — it combines PVE's own backup-job and link config, never measured backup
// volume (needs-hardware-validation for real-world accuracy).
func sizingHint(node string, h Host, datastores []string, bp BackupPath) string {
	ds := "its datastore"
	if len(datastores) == 1 {
		ds = "datastore " + datastores[0]
	} else if len(datastores) > 1 {
		ds = "datastores " + strings.Join(datastores, ", ")
	}

	var via string
	switch {
	case !bp.RidingOn.IsZero():
		via = " via " + bp.RidingOn.ID + " (" + humanSpeed(bp.LinkMbps, bp.LinkKnown) + ")"
	case !bp.Carrier.IsZero():
		via = " via " + bp.Carrier.ID + " (" + humanSpeed(bp.LinkMbps, bp.LinkKnown) + ")"
	default:
		via = " over an egress path vnprox could not resolve from inventory"
	}

	return fmt.Sprintf(
		"%s backs up to %s on PBS %s%s. %s. Heuristic estimate from PVE backup-job and link config — validate the backup window against real dataset size and link utilization.",
		node, ds, h.Address, via, jobPhrase(bp.Jobs),
	)
}

// jobPhrase summarizes the backup jobs driving a path: how many, on what
// (deduplicated, sorted) schedules, covering how many guests.
func jobPhrase(jobs []JobSummary) string {
	if len(jobs) == 0 {
		return "No enabled backup job currently targets it"
	}
	schedSet := map[string]bool{}
	explicit := 0
	all := false
	for _, j := range jobs {
		s := strings.TrimSpace(j.Schedule)
		if s == "" {
			s = "manual"
		}
		schedSet[s] = true
		if j.All {
			all = true
		} else {
			explicit += j.Guests
		}
	}
	scheds := sortedKeys(schedSet)

	guests := "all guests"
	if !all {
		guests = fmt.Sprintf("%d guest(s)", explicit)
	}
	return fmt.Sprintf("%d backup job(s) (%s) covering %s", len(jobs), strings.Join(scheds, ", "), guests)
}

// humanSpeed formats a link speed for the sizing hint: "10 Gbit/s" for a
// whole-Gbit link, else "<n> Mbit/s"; "an unknown link speed" when unresolved.
func humanSpeed(mbps int, known bool) string {
	if !known || mbps <= 0 {
		return "an unknown link speed"
	}
	if mbps >= 1000 && mbps%1000 == 0 {
		return fmt.Sprintf("%d Gbit/s", mbps/1000)
	}
	return fmt.Sprintf("%d Mbit/s", mbps)
}

// firstRef sorts matches by Ref.String() and returns the first, or (zero,
// false) when empty — the deterministic "pick one" every resolver here uses.
func firstRef(matches []inventory.Ref) (inventory.Ref, bool) {
	if len(matches) == 0 {
		return inventory.Ref{}, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].String() < matches[j].String() })
	return matches[0], true
}

// sortedKeys returns m's keys sorted — deterministic slice output from a set.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
