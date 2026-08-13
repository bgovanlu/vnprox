// testhelpers_test.go mirrors internal/blueprint's own testhelpers_test.go
// convention exactly (same reasoning: Instantiate/diffEntity are pure
// functions of an inventory.Snapshot, so a small hand-built graph is a
// faster, clearer way to pin down "this seed's bare/conforming/divergent
// case produces exactly this changeset" than round-tripping through
// pvemock/collect for every one) — duplicated here rather than imported
// because internal/blueprint's helpers are unexported test-only symbols in
// a different package. seed_pvemock_test.go is this package's counterpart to
// blueprint_test.go's own "more of the real stack" cross-reference.
package seed_test

import (
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func newGraphWithNodes(names ...string) *inventory.Graph {
	g := inventory.NewGraph()
	var nodes []inventory.Entity
	for _, n := range names {
		nodes = append(nodes, &inventory.Node{
			Ref: inventory.Ref{Kind: inventory.KindNode, Node: n, ID: n}, Name: n, Status: "online",
		})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, nodes)
	return g
}

func entitiesOfKind(g *inventory.Graph, node string, kind inventory.Kind) []inventory.Entity {
	var out []inventory.Entity
	for _, e := range g.Snapshot().All() {
		ref := e.GetRef()
		if ref.Kind == kind && ref.Node == node {
			out = append(out, e)
		}
	}
	return out
}

type bridgeOpts struct {
	gateway   string
	comments  string
	ports     []string
	vids      []int
	addresses []string
	mtu       int
	vlanAware bool
	stp       bool
}

func applyBridge(g *inventory.Graph, node, name string, o bridgeOpts) {
	var vids []inventory.VidRange
	for _, v := range o.vids {
		vids = append(vids, inventory.VidRange{Low: v, High: v})
	}
	br := &inventory.Bridge{
		Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Name: name, Virt: inventory.BridgeLinux,
		DeclaredPortNames: o.ports, VlanAware: o.vlanAware, VlanAwareSet: true, Vids: vids,
		Addresses: o.addresses, MTUDeclared: o.mtu, Gateway: o.gateway, Comments: o.comments, STP: o.stp,
	}
	entities := replaceByRef(entitiesOfKind(g, node, inventory.KindBridge), br)
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, entities)
}

type sdnZoneOpts struct {
	typ        string
	bridge     string
	controller string
	nodes      []string
	vrfVxlan   int
	mtu        int
}

func applySdnZone(g *inventory.Graph, id string, o sdnZoneOpts) {
	z := &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: id}, ID: id, Type: o.typ,
		Bridge: o.bridge, Controller: o.controller, Nodes: o.nodes, VrfVxlan: o.vrfVxlan, MTU: o.mtu,
	}
	entities := replaceByRef(entitiesOfKind(g, "", inventory.KindSDNZone), z)
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNZone}}, entities)
}

func applySdnVnet(g *inventory.Graph, zone, name string, tag int, vlanAware bool) {
	v := &inventory.SdnVnet{
		Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zone + "/" + name}, ID: name, Zone: zone, Tag: tag, VlanAware: vlanAware,
	}
	entities := replaceByRef(entitiesOfKind(g, "", inventory.KindSDNVnet), v)
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNVnet}}, entities)
}

func applySdnSubnet(g *inventory.Graph, vnet, cidr, gateway string, snat bool) {
	s := &inventory.SdnSubnet{
		Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr}, ID: cidr, Vnet: vnet, Gateway: gateway, SNAT: snat,
	}
	entities := replaceByRef(entitiesOfKind(g, "", inventory.KindSDNSubnet), s)
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNSubnet}}, entities)
}

func replaceByRef(existing []inventory.Entity, e inventory.Entity) []inventory.Entity {
	out := make([]inventory.Entity, 0, len(existing)+1)
	for _, ex := range existing {
		if ex.GetRef() != e.GetRef() {
			out = append(out, ex)
		}
	}
	return append(out, e)
}

func opTypes(ops []change.Op) []change.OpType {
	out := make([]change.OpType, len(ops))
	for i, o := range ops {
		out[i] = o.Type
	}
	return out
}

func equalOpTypes(got, want []change.OpType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
