// SPDX-License-Identifier: Apache-2.0

package findings_test

// Shared graph-building helpers for the health-check golden tests — the
// same "build a minimal *inventory.Graph directly via ApplyPoll" pattern
// internal/drift's own testhelpers_test.go uses, for the same reason: the
// check functions only need an inventory.Snapshot, so a hand-built graph
// targeting exactly the case under test is faster and clearer than
// round-tripping through pvemock/collect for every case.

import (
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func newGraphWithNodes(names ...string) *inventory.Graph {
	g := inventory.NewGraph()
	var nodes []inventory.Entity
	for _, n := range names {
		nodes = append(nodes, &inventory.Node{
			Ref:  inventory.Ref{Kind: inventory.KindNode, Node: n, ID: n},
			Name: n, Status: "online",
		})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, nodes)
	return g
}

// netlinkBond applies one node's bond, including per-slave MII/active
// status (host-netlink runtime data, the substrate checkBondSlaveDown
// reads).
func netlinkBond(g *inventory.Graph, node, name string, slaves []inventory.BondSlaveState) {
	names := make([]string, len(slaves))
	for i, s := range slaves {
		names[i] = s.Name
	}
	b := &inventory.Bond{
		Ref:         inventory.Ref{Kind: inventory.KindBond, Node: node, ID: name},
		Name:        name,
		Slaves:      names,
		SlaveDetail: slaves,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBond}}, []inventory.Entity{b})
}

// netlinkPhysNicUp applies one node's physical NIC's runtime carrier state.
// A Scope{Node, Kinds:[KindPhysNic]} poll retires every other PhysNic this
// same source previously contributed for that node (ApplyPoll's scope-
// reconciliation contract — see graph.go's Scope doc comment), so a test
// that needs more than one live NIC on the same node must batch them into
// one netlinkPhysNics call below, not call this helper once per NIC.
func netlinkPhysNicUp(g *inventory.Graph, node, name string, up bool) {
	netlinkPhysNics(g, node, map[string]bool{name: up})
}

// netlinkPhysNics applies every (name -> linkUp) pair in states as one
// single-source, single-scope poll, so they coexist rather than each
// retiring the last (see netlinkPhysNicUp's doc comment).
func netlinkPhysNics(g *inventory.Graph, node string, states map[string]bool) {
	var entities []inventory.Entity
	for name, up := range states {
		entities = append(entities, &inventory.PhysNic{
			Ref:       inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
			Name:      name,
			LinkUp:    up,
			LinkUpSet: true,
		})
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, entities)
}

// netlinkBridgeWithPorts applies one node's bridge with declared+live port
// membership so inventory's linking pass resolves Bridge.Ports (used by the
// bridge-carrier and STP-burst checks).
func netlinkBridgeWithPorts(g *inventory.Graph, node, name string, ports []string) {
	br := &inventory.Bridge{
		Ref:               inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name:              name,
		Virt:              inventory.BridgeLinux,
		PortNames:         ports,
		DeclaredPortNames: ports,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
}

// pvePendingPhysNic applies one node's physical NIC with a Pending marker
// (pve-network staged-edit contribution).
func pvePendingPhysNic(g *inventory.Graph, node, name, pending string) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTUDeclared: 1500, Pending: pending,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}

// clearPending re-applies the same NIC without a Pending marker.
func clearPending(g *inventory.Graph, node, name string) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTUDeclared: 1500,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}
