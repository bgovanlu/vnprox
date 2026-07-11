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
	mu             sync.RWMutex
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
	sdn         sdnState
	ipam        ipamState
	fixture     *Fixture
	nodes       map[string]*nodeState
	sessions    *sessionStore
	tasks       *taskManager
	clock       func() time.Time
	clusterFW   FirewallScope
	nodesMu     sync.RWMutex
	clusterFWMu sync.RWMutex
}

// NewState builds runtime State from a validated Fixture.
func NewState(f *Fixture) *State {
	s := &State{
		fixture:   f,
		nodes:     make(map[string]*nodeState, len(f.Nodes)),
		clusterFW: f.Firewall.Cluster,
		clock:     time.Now,
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

	s.ipam.entries = make(map[string][]IPAMEntrySpec, len(f.SDN.Ipams))
	for _, ip := range f.SDN.Ipams {
		s.ipam.entries[ip.ID] = append([]IPAMEntrySpec(nil), ip.Entries...)
	}

	for name, ns := range f.Nodes {
		rt := &nodeState{
			links:    cloneMap(ns.Links),
			lldp:     cloneMap(ns.LLDP),
			stats:    cloneMap(ns.Stats),
			services: cloneMap(ns.Services),
			qemu:     cloneGuestMap(ns.Qemu),
			lxc:      cloneGuestMap(ns.Lxc),
			mock:     f.Mock.merge(ns.Mock),
			network:  append([]NetIface(nil), ns.Network...),
			frr:      ns.FRR,
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
