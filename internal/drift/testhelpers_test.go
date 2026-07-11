package drift_test

// Shared graph-building helpers for the check-family golden tests. Every
// test builds an *inventory.Graph directly (ApplyPoll with hand-built
// entities) rather than going through pvemock/collect — the check
// functions are pure functions of an inventory.Snapshot, so exercising
// them against a minimal, targeted graph is both faster and clearer about
// exactly which divergence is under test than round-tripping through the
// mock PVE server for every case. The messy-brownfield fixture test
// (messybrownfield_test.go) and the closed-loop test
// (internal/change/drift_closedloop_test.go) cover the real
// pvemock -> collect -> inventory.Graph -> drift pipeline end to end.

import (
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// newGraphWithNodes builds a graph seeded with one inventory.Node per name
// (drift's clusterNodes helper reads these), and nothing else.
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

// pveBridge applies one node's bridge as a pve-network contribution
// (declared config: DeclaredPortNames/MTUDeclared/VlanAware/Vids — the
// fields FromPVENetwork itself would set), which is enough for the
// checks under test (they fall back from runtime to declared fields when
// runtime is unreported — see helpers.go's effectiveMTU and link.go's
// Bridge.Ports resolution).
func pveBridge(g *inventory.Graph, node, name string, mtu int, vlanAware bool, vids []inventory.VidRange, declaredPorts []string) {
	br := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeLinux,
		MTUDeclared: mtu, VlanAware: vlanAware, VlanAwareSet: true, Vids: vids,
		DeclaredPortNames: declaredPorts,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
}

// pvePhysNic applies one node's physical NIC as a pve-network contribution
// (declared MTU only).
func pvePhysNic(g *inventory.Graph, node, name string, mtu int) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTUDeclared: mtu,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}

// netlinkPhysNic applies one node's physical NIC as a host-netlink
// contribution (runtime MTU/link state).
func netlinkPhysNic(g *inventory.Graph, node, name string, mtu int, linkUp bool) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTU: mtu, LinkUp: linkUp, LinkUpSet: true,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}

// netlinkBridge applies one node's bridge as a host-netlink contribution
// (runtime PortNames/MTU — the live view).
func netlinkBridge(g *inventory.Graph, node, name string, mtu int, livePorts []string) {
	br := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeLinux,
		MTU: mtu, PortNames: livePorts,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
}

// pveSDNZone applies a cluster-scoped SDN zone.
func pveSDNZone(g *inventory.Graph, id, bridge string, nodes []string) {
	z := &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: id},
		ID:  id, Type: "vlan", Bridge: bridge, Nodes: nodes,
	}
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{z})
}

// pvePendingPhysNic applies one node's physical NIC as a pve-network
// contribution carrying a Pending marker.
func pvePendingPhysNic(g *inventory.Graph, node, name, pending string) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTUDeclared: 1500, Pending: pending,
	}
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}
