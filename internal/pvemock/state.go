package pvemock

import (
	"sync"
	"time"
)

// nodeState is the mutable runtime state for one node, seeded from a
// NodeSpec but diverging from it as requests stage/apply/rollback changes.
type nodeState struct {
	links          map[string]LinkInfo
	lldp           map[string]LLDPNeighbor
	stats          map[string]IfaceStats
	services       map[string]bool
	qemu           map[string]*GuestSpec
	lxc            map[string]*GuestSpec
	frr            *FRRSpec
	corosync       *CorosyncSpec
	dhcpLeases     string
	neighbors      []NeighborSpec
	conntrack      []ConntrackEntrySpec
	ipv6RA         []IPv6RASpec
	network        []NetIface
	networkPending []NetIface
	firewall       FirewallScope
	mock           MockOptions
	mu             sync.RWMutex
}

// sdnState is the mutable runtime SDN tree (cluster-scope, one instance).
//
// zones/vnets/subnets is the staged (default, pending-merged) view GET
// /cluster/sdn/{zones,vnets,vnets/{v}/subnets} serve by default — the one
// every handler in sdn.go already mutated before T-401. zonesRunning/
// vnetsRunning/subnetsRunning is the parallel last-applied view the same
// routes serve under "?running=1" (T-401, docs/features/sdn.md §1's
// staged-vs-running diff): derived once at load (see runningZone/
// runningVnet/runningSubnet) and re-synced on every successful
// handleSDNApply, exactly mirroring what a real apply does to PVE's own
// running config.
type sdnState struct {
	zones          map[string]SDNZoneSpec
	vnets          map[string]SDNVnetSpec
	subnets        map[string]SDNSubnetSpec
	zonesRunning   map[string]SDNZoneSpec
	vnetsRunning   map[string]SDNVnetSpec
	subnetsRunning map[string]SDNSubnetSpec
	// dnsZones (T-1204) holds the SDN DNS plugin config plus each zone's
	// authoritative record set. Unlike zones/vnets/subnets, DNS writes apply
	// immediately (real PVE writes records straight into PowerDNS per-record,
	// not into a staged cfg an apply later flushes), so there is no separate
	// running/pending pair here — the config store IS the truth, and the
	// live "resolve" read reflects it unless the zone is marked Unreachable.
	dnsZones map[string]SDNDnsZoneSpec
	// fabrics/fabricsRunning (T-3101) mirror the zones/zonesRunning
	// staged-vs-running pair exactly — a fabric stages and applies through
	// the same PUT /cluster/sdn as every other SDN object family
	// (internal/pve/sdn_fabric.go's package doc comment).
	fabrics        map[string]SDNFabricSpec
	fabricsRunning map[string]SDNFabricSpec
	// fabricNodes (T-3101) is GET /cluster/sdn/fabrics/node's own
	// collection — fixture-seeded and read-only at runtime (no handler
	// mutates it; real per-node fabric realization needs cluster hardware
	// this mock does not model, see needs-hardware-validation.md).
	fabricNodes []SDNFabricNodeSpec
	// prefixLists/routeMaps (T-3101) are read-only fixture-seeded lists —
	// no handler mutates either (sdn_fabric.go's guard: T-3101 scopes CRUD
	// out entirely).
	prefixLists []SDNPrefixListSpec
	routeMaps   []SDNRouteMapSpec
	mu          sync.RWMutex
}

// ipamState is the mutable runtime allocation set for every configured IPAM
// plugin instance (cluster-scope, one instance — T-405's `ipam.alloc.*`
// write path). Keyed by IPAM plugin id (SDNIpamSpec.ID), mirroring
// sdnState's per-object maps; entries themselves carry their own
// zone/vnet/subnet (IPAMEntrySpec), so — like real PVE — a caller never
// names the plugin explicitly when reserving/releasing an address on a
// vnet (see ipam.go's handleIPAMCreateIP/handleIPAMDeleteIP doc comments).
type ipamState struct {
	entries map[string][]IPAMEntrySpec
	mu      sync.RWMutex
}

// State is the full mutable runtime state of a mock PVE server, built from
// an immutable Fixture. Server handlers read/write State; Fixture itself is
// never mutated after load.
type State struct {
	clock              func() time.Time
	execs              map[int]execResult
	fixture            *Fixture
	nodes              map[string]*nodeState
	sessions           *sessionStore
	tasks              *taskManager
	nodeOnlineOverride map[string]bool
	clusterFW          FirewallScope
	ipam               ipamState
	sdn                sdnState
	execSeq            int
	nodesMu            sync.RWMutex
	clusterFWMu        sync.RWMutex
	churnMu            sync.RWMutex
	execMu             sync.Mutex
}

// NewState builds runtime State from a validated Fixture.
func NewState(f *Fixture) *State {
	s := &State{
		fixture:   f,
		nodes:     make(map[string]*nodeState, len(f.Nodes)),
		clusterFW: f.Firewall.Cluster,
		clock:     time.Now,
		execs:     make(map[int]execResult),
	}
	s.sessions = newSessionStore(s.clock)
	if f.Mock.TicketTTLMS > 0 {
		s.sessions.setTTL(time.Duration(f.Mock.TicketTTLMS) * time.Millisecond)
	}
	s.sdn.zones = make(map[string]SDNZoneSpec, len(f.SDN.Zones))
	s.sdn.vnets = make(map[string]SDNVnetSpec, len(f.SDN.Vnets))
	s.sdn.subnets = make(map[string]SDNSubnetSpec, len(f.SDN.Subnets))
	s.sdn.zonesRunning = make(map[string]SDNZoneSpec, len(f.SDN.Zones))
	s.sdn.vnetsRunning = make(map[string]SDNVnetSpec, len(f.SDN.Vnets))
	s.sdn.subnetsRunning = make(map[string]SDNSubnetSpec, len(f.SDN.Subnets))
	for _, z := range f.SDN.Zones {
		s.sdn.zones[z.ID] = z
		if r, ok := runningZone(z); ok {
			s.sdn.zonesRunning[z.ID] = r
		}
	}
	for _, v := range f.SDN.Vnets {
		s.sdn.vnets[v.ID] = v
		if r, ok := runningVnet(v); ok {
			s.sdn.vnetsRunning[v.ID] = r
		}
	}
	for _, sub := range f.SDN.Subnets {
		s.sdn.subnets[sub.ID] = sub
		if r, ok := runningSubnet(sub); ok {
			s.sdn.subnetsRunning[sub.ID] = r
		}
	}

	s.sdn.dnsZones = make(map[string]SDNDnsZoneSpec, len(f.SDN.DNSZones))
	for _, z := range f.SDN.DNSZones {
		z.Records = append([]SDNDnsRecordSpec(nil), z.Records...)
		s.sdn.dnsZones[z.ID] = z
	}

	// T-3101: fabrics/fabricsRunning, seeded exactly like zones/zonesRunning
	// above (runningFabric mirrors runningZone).
	s.sdn.fabrics = make(map[string]SDNFabricSpec, len(f.SDN.Fabrics))
	s.sdn.fabricsRunning = make(map[string]SDNFabricSpec, len(f.SDN.Fabrics))
	for _, fab := range f.SDN.Fabrics {
		s.sdn.fabrics[fab.ID] = fab
		if r, ok := runningFabric(fab); ok {
			s.sdn.fabricsRunning[fab.ID] = r
		}
	}
	s.sdn.fabricNodes = append([]SDNFabricNodeSpec(nil), f.SDN.FabricNodes...)
	s.sdn.prefixLists = append([]SDNPrefixListSpec(nil), f.SDN.PrefixLists...)
	s.sdn.routeMaps = append([]SDNRouteMapSpec(nil), f.SDN.RouteMaps...)

	s.ipam.entries = make(map[string][]IPAMEntrySpec, len(f.SDN.Ipams))
	for _, ip := range f.SDN.Ipams {
		s.ipam.entries[ip.ID] = append([]IPAMEntrySpec(nil), ip.Entries...)
	}

	for name, ns := range f.Nodes {
		rt := &nodeState{
			links:      cloneMap(ns.Links),
			lldp:       cloneMap(ns.LLDP),
			stats:      cloneMap(ns.Stats),
			services:   cloneMap(ns.Services),
			qemu:       cloneGuestMap(ns.Qemu),
			lxc:        cloneGuestMap(ns.Lxc),
			mock:       f.Mock.merge(ns.Mock),
			network:    append([]NetIface(nil), ns.Network...),
			frr:        ns.FRR,
			corosync:   ns.Corosync,
			dhcpLeases: ns.DHCPLeases,
			neighbors:  append([]NeighborSpec(nil), ns.Neighbors...),
			conntrack:  append([]ConntrackEntrySpec(nil), ns.Conntrack...),
			ipv6RA:     append([]IPv6RASpec(nil), ns.IPv6RA...),
		}
		if ns.Firewall != nil {
			rt.firewall = *ns.Firewall
		}
		if ns.NetworkPending != nil {
			rt.networkPending = append([]NetIface(nil), ns.NetworkPending...)
		}
		s.nodes[name] = rt
	}

	s.tasks = newTaskManager(s.clock)
	return s
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneGuestMap(m map[string]*GuestSpec) map[string]*GuestSpec {
	out := make(map[string]*GuestSpec, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		cp := *v
		cp.Config = cloneMap(v.Config)
		out[k] = &cp
	}
	return out
}

func (s *State) node(name string) (*nodeState, bool) {
	s.nodesMu.RLock()
	defer s.nodesMu.RUnlock()
	n, ok := s.nodes[name]
	return n, ok
}

// execResult is one scripted guest-agent exec run's synthesized
// exec-status answer (T-802). exited false models "never completes" —
// handleGuestAgentExecStatus always reports it that way, so a fixture's
// "timeout" outcome exercises the real caller's own bounded poll deadline
// rather than the mock inventing a fake elapsed-time model.
type execResult struct {
	outData  string
	errData  string
	exitCode int
	exited   bool
}

// nextExecPID assigns the next scripted exec run's pid (T-802). Global
// (not per-node/guest) — real PVE pids are host-process-scoped, but nothing
// in this mock or its callers relies on that; a single monotonically
// increasing counter keeps the bookkeeping trivial.
func (s *State) nextExecPID() int {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.execSeq++
	return s.execSeq
}

func (s *State) storeExecResult(pid int, res execResult) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.execs[pid] = res
}

func (s *State) execResult(pid int) (execResult, bool) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	r, ok := s.execs[pid]
	return r, ok
}
