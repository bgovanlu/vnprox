package blueprint_test

// Shared inventory-graph builders for this package's tests. Mirrors
// internal/drift's testhelpers_test.go convention: build a minimal
// *inventory.Graph directly via ApplyPoll with hand-built entities, since
// Instantiate/diffEntity are pure functions of an inventory.Snapshot —
// exercising them against a small, targeted graph is faster and clearer
// about exactly which case is under test than round-tripping through
// pvemock/collect for every one. blueprint_capture_test.go's round-trip
// test and service_test.go's second-daemon export/import test cover more
// of the real stack end to end.
//
// Every apply* helper below re-polls the *entire* current set of
// same-kind entities in the affected scope, not just the one being
// added/replaced: inventory.Graph.ApplyPoll reconciles a scope by
// removing anything in-scope the poll omits (graph.go's Scope doc
// comment), so two independent single-entity ApplyPoll calls for, say,
// two bridges on the same node would have the second call retire the
// first. Building fixtures with several same-kind entities therefore goes
// through one poll per kind+scope, current-plus-new.

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

// entitiesOfKind returns every current entity of kind in scope-node node
// from g's snapshot, as a mutable []inventory.Entity ready to append to.
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

type bondOpts struct {
	mode           string
	lacpRate       string
	xmitHashPolicy string
	slaves         []string
	mtu            int
}

func applyBond(g *inventory.Graph, node, name string, o bondOpts) {
	bd := &inventory.Bond{
		Ref:  inventory.Ref{Kind: inventory.KindBond, Node: node, ID: name},
		Name: name, Mode: o.mode, DeclaredSlaves: o.slaves, LACPRate: o.lacpRate, XmitHashPolicy: o.xmitHashPolicy, MTUDeclared: o.mtu,
	}
	entities := replaceByRef(entitiesOfKind(g, node, inventory.KindBond), bd)
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBond}}, entities)
}

func applyVlan(g *inventory.Graph, node, name, parent string, vid int, addresses []string, mtu int) {
	vl := &inventory.VlanIface{
		Ref: inventory.Ref{Kind: inventory.KindVlan, Node: node, ID: name}, Name: name,
		Parent: inventory.Ref{Kind: inventory.KindBond, Node: node, ID: parent}, ParentName: parent,
		Vid: vid, Addresses: addresses, MTUDeclared: mtu,
	}
	entities := replaceByRef(entitiesOfKind(g, node, inventory.KindVlan), vl)
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindVlan}}, entities)
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

type sdnControllerOpts struct {
	typ string
	asn int
}

func applySdnController(g *inventory.Graph, id string, o sdnControllerOpts) {
	c := &inventory.SdnController{
		Ref: inventory.Ref{Kind: inventory.KindSDNController, ID: id}, ID: id, Type: o.typ, ASN: o.asn,
	}
	entities := replaceByRef(entitiesOfKind(g, "", inventory.KindSDNController), c)
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{Kinds: []inventory.Kind{inventory.KindSDNController}}, entities)
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

// replaceByRef returns existing with any entity sharing e's Ref removed,
// plus e appended — the "upsert into a poll batch" building block every
// apply* helper above uses.
func replaceByRef(existing []inventory.Entity, e inventory.Entity) []inventory.Entity {
	out := make([]inventory.Entity, 0, len(existing)+1)
	for _, ex := range existing {
		if ex.GetRef() != e.GetRef() {
			out = append(out, ex)
		}
	}
	return append(out, e)
}

// opTypes returns the OpType of every op in ops, in order — the shape
// most golden assertions in this package's tests compare against, since
// exact param pointer values are covered separately where it matters.
func opTypes(ops []change.Op) []change.OpType {
	out := make([]change.OpType, len(ops))
	for i, o := range ops {
		out[i] = o.Type
	}
	return out
}
