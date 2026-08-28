// SPDX-License-Identifier: Apache-2.0

// churn.go is the mutation surface T-2504's soak gate drives the mock
// through: guests created and destroyed, cluster members flapping offline
// and back, and an SDN VNet whose zone comes and goes (which makes the
// `orphan_vnet` health check appear and clear).
//
// It exists because the existing mock-control surface (mockcontrol.go) only
// injects *failures* — a network reload that fails, an SDN zone that will
// not realize. A soak needs the opposite: successful, continuous, realistic
// change, so the daemon's collectors, inventory graph, topology projection,
// findings engine, and WebSocket fan-out all keep doing real work for the
// length of the run. A daemon polling an unchanging fixture is an idle
// daemon, and an idle daemon does not leak in the ways this gate exists to
// catch.
//
// Every method here mutates runtime State (the same state the handlers
// read), never the loaded Fixture — Fixture stays immutable exactly as
// State's own doc comment promises. Node online-ness is the one property
// the handlers previously read straight off the Fixture, so it is modelled
// as an override map consulted by those handlers rather than by editing the
// fixture underneath them.
//
// These are safe for concurrent use, like the rest of State.

package pvemock

// SetNodeOnline overrides the fixture's declared `online` flag for one
// cluster member, as GET /cluster/status and GET /cluster/resources report
// it. Reports false if node names no member of the fixture's cluster.
//
// Real PVE flaps a member's online flag when corosync loses and regains it;
// this is the smallest faithful model of that, and it is the one cluster
// property a daemon re-reads on every single PVE poll.
func (s *State) SetNodeOnline(node string, online bool) bool {
	if !s.isClusterMember(node) {
		return false
	}
	s.churnMu.Lock()
	defer s.churnMu.Unlock()
	if s.nodeOnlineOverride == nil {
		s.nodeOnlineOverride = make(map[string]bool)
	}
	s.nodeOnlineOverride[node] = online
	return true
}

// ClearNodeOnlineOverride drops a SetNodeOnline override, returning the
// node to whatever the fixture declared.
func (s *State) ClearNodeOnlineOverride(node string) {
	s.churnMu.Lock()
	defer s.churnMu.Unlock()
	delete(s.nodeOnlineOverride, node)
}

func (s *State) isClusterMember(node string) bool {
	for _, n := range s.fixture.Cluster.Nodes {
		if n.Name == node {
			return true
		}
	}
	return false
}

// nodeOnline resolves a cluster member's effective online flag: the
// SetNodeOnline override if one is set, else the fixture's own value.
func (s *State) nodeOnline(node string, fixtureValue bool) bool {
	s.churnMu.RLock()
	defer s.churnMu.RUnlock()
	if v, ok := s.nodeOnlineOverride[node]; ok {
		return v
	}
	return fixtureValue
}

// GuestKind names which of a node's two guest tables an operation targets.
type GuestKind string

// The two guest kinds PVE models, matching the fixture's `qemu:`/`lxc:`
// node keys and the /nodes/{node}/{qemu,lxc} route segment.
const (
	GuestQemu GuestKind = "qemu"
	GuestLXC  GuestKind = "lxc"
)

// SetGuest creates (or replaces) a guest on node, as if someone had just
// cloned a VM. It becomes visible to GET /cluster/resources, the per-node
// guest list, and the guest config endpoint on the next poll. Reports false
// if node is unknown or kind is not one of the two GuestKind constants.
func (s *State) SetGuest(node string, kind GuestKind, vmid string, spec GuestSpec) bool {
	ns, ok := s.node(node)
	if !ok || vmid == "" {
		return false
	}
	copied := spec
	ns.mu.Lock()
	defer ns.mu.Unlock()
	switch kind {
	case GuestQemu:
		if ns.qemu == nil {
			ns.qemu = make(map[string]*GuestSpec)
		}
		ns.qemu[vmid] = &copied
	case GuestLXC:
		if ns.lxc == nil {
			ns.lxc = make(map[string]*GuestSpec)
		}
		ns.lxc[vmid] = &copied
	default:
		return false
	}
	return true
}

// RemoveGuest destroys a guest on node, whichever table it lives in.
// Reports whether anything was actually removed.
func (s *State) RemoveGuest(node, vmid string) bool {
	ns, ok := s.node(node)
	if !ok {
		return false
	}
	ns.mu.Lock()
	defer ns.mu.Unlock()
	removed := false
	if _, ok := ns.qemu[vmid]; ok {
		delete(ns.qemu, vmid)
		removed = true
	}
	if _, ok := ns.lxc[vmid]; ok {
		delete(ns.lxc, vmid)
		removed = true
	}
	return removed
}

// SetSDNVnet creates or replaces one SDN VNet in both the staged and the
// running views (a VNet that exists but has not been applied would exercise
// the pending-diff path instead, which is not what a soak wants to hold
// steady). A VNet whose Zone names no existing zone is the smallest way to
// make the `orphan_vnet` health check fire; removing it again clears the
// finding, which is the "findings appearing and clearing" half of the
// card's churn.
func (s *State) SetSDNVnet(spec SDNVnetSpec) {
	s.sdn.mu.Lock()
	defer s.sdn.mu.Unlock()
	s.sdn.vnets[spec.ID] = spec
	if r, ok := runningVnet(spec); ok {
		s.sdn.vnetsRunning[spec.ID] = r
	} else {
		delete(s.sdn.vnetsRunning, spec.ID)
	}
}

// RemoveSDNVnet deletes an SDN VNet from both views, reporting whether it
// was there.
func (s *State) RemoveSDNVnet(id string) bool {
	s.sdn.mu.Lock()
	defer s.sdn.mu.Unlock()
	_, existed := s.sdn.vnets[id]
	delete(s.sdn.vnets, id)
	delete(s.sdn.vnetsRunning, id)
	return existed
}
