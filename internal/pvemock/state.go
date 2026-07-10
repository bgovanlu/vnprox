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
	qemu           map[string]*GuestSpec
	lxc            map[string]*GuestSpec
	network        []NetIface
	networkPending []NetIface
	firewall       FirewallScope
	mock           MockOptions
	mu             sync.RWMutex
}

// sdnState is the mutable runtime SDN tree (cluster-scope, one instance).
type sdnState struct {
	zones   map[string]SDNZoneSpec
	vnets   map[string]SDNVnetSpec
	subnets map[string]SDNSubnetSpec
	mu      sync.RWMutex
}

// State is the full mutable runtime state of a mock PVE server, built from
// an immutable Fixture. Server handlers read/write State; Fixture itself is
// never mutated after load.
type State struct {
	sdn         sdnState
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
	for _, z := range f.SDN.Zones {
		s.sdn.zones[z.ID] = z
	}
	for _, v := range f.SDN.Vnets {
		s.sdn.vnets[v.ID] = v
	}
	for _, sub := range f.SDN.Subnets {
		s.sdn.subnets[sub.ID] = sub
	}

	for name, ns := range f.Nodes {
		rt := &nodeState{
			links:   cloneMap(ns.Links),
			lldp:    cloneMap(ns.LLDP),
			stats:   cloneMap(ns.Stats),
			qemu:    cloneGuestMap(ns.Qemu),
			lxc:     cloneGuestMap(ns.Lxc),
			mock:    f.Mock.merge(ns.Mock),
			network: append([]NetIface(nil), ns.Network...),
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
