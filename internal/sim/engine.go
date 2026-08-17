package sim

import (
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Engine holds indexes derived once from an inventory Snapshot so repeated
// Simulate calls (the UI, the 10k-sim benchmark) do not re-scan the whole
// entity set each time. It is immutable after construction and safe to call
// Simulate on concurrently (it only reads).
type Engine struct {
	inv      inventory.Snapshot
	fw       fw.Snapshot
	guestIPs map[inventory.Ref][]GuestIP
	// shapedRefs is Input.ShapedRefs re-keyed by Ref.String() — Hop.Ref is
	// already that same string encoding, so addHop can test membership
	// directly without re-parsing every hop's ref back into an
	// inventory.Ref.
	shapedRefs map[string]bool

	guestNics    map[inventory.Ref]*inventory.GuestNic
	guests       map[inventory.Ref]*inventory.Guest
	bridgesByRef map[inventory.Ref]*inventory.Bridge
	// bridgeByNodeName resolves a plain bridge by (node, name) — needed for
	// cross-node "same-named bridge" reachability, where the src and dst
	// bridges are distinct per-node entities that the fabric bridges
	// together.
	bridgeByNodeName map[string]map[string]*inventory.Bridge
	vnetByID         map[string]*inventory.SdnVnet
	// vnetByRef indexes the same vnets as vnetByID, but by their full Ref
	// (Kind+ID+Node) rather than the bare SdnVnet.ID string. A guest NIC's
	// resolved BridgeOrVnet (internal/inventory/link.go's resolveGuestNic)
	// is a Ref whose ID is the "<zone>/<vnet>" composite form
	// (ingest.go's SdnVnet.Ref.ID convention), not the bare vnet name
	// SdnVnet.ID/vnetByID carry — resolveNicAttachment must look a NIC's
	// attachment up by that full Ref, the same way bridgesByRef already
	// does for bridge attachments, not by re-deriving a bare name from it.
	vnetByRef     map[inventory.Ref]*inventory.SdnVnet
	zoneByID      map[string]*inventory.SdnZone
	subnetsByVnet map[string][]*inventory.SdnSubnet
	subnets       []*inventory.SdnSubnet
}

// NewEngine builds an Engine from in. The inventory Snapshot is required;
// GuestIPs is optional.
func NewEngine(in Input) *Engine {
	shapedRefs := make(map[string]bool, len(in.ShapedRefs))
	for ref, shaped := range in.ShapedRefs {
		if shaped {
			shapedRefs[ref.String()] = true
		}
	}
	e := &Engine{
		inv:              in.Inventory,
		fw:               fw.BuildSnapshot(in.Inventory.All()),
		guestIPs:         in.GuestIPs,
		shapedRefs:       shapedRefs,
		guestNics:        map[inventory.Ref]*inventory.GuestNic{},
		guests:           map[inventory.Ref]*inventory.Guest{},
		bridgesByRef:     map[inventory.Ref]*inventory.Bridge{},
		bridgeByNodeName: map[string]map[string]*inventory.Bridge{},
		vnetByID:         map[string]*inventory.SdnVnet{},
		vnetByRef:        map[inventory.Ref]*inventory.SdnVnet{},
		zoneByID:         map[string]*inventory.SdnZone{},
		subnetsByVnet:    map[string][]*inventory.SdnSubnet{},
	}
	for _, ent := range in.Inventory.All() {
		switch v := ent.(type) {
		case *inventory.GuestNic:
			e.guestNics[v.GetRef()] = v
		case *inventory.Guest:
			e.guests[v.GetRef()] = v
		case *inventory.Bridge:
			ref := v.GetRef()
			e.bridgesByRef[ref] = v
			m := e.bridgeByNodeName[ref.Node]
			if m == nil {
				m = map[string]*inventory.Bridge{}
				e.bridgeByNodeName[ref.Node] = m
			}
			m[v.Name] = v
		case *inventory.SdnVnet:
			e.vnetByID[v.ID] = v
			e.vnetByRef[v.GetRef()] = v
		case *inventory.SdnZone:
			e.zoneByID[v.ID] = v
		case *inventory.SdnSubnet:
			e.subnets = append(e.subnets, v)
			e.subnetsByVnet[v.Vnet] = append(e.subnetsByVnet[v.Vnet], v)
		}
	}
	return e
}

// Simulate runs one simulation over a fresh Engine — the convenience form
// matching docs/features/firewall.md §6's Simulate(graph, src, dst, proto,
// port) signature. For repeated calls over one snapshot, build an Engine
// once and reuse it.
func Simulate(in Input, req Request) Result {
	return NewEngine(in).Simulate(req)
}

// Simulate answers one Request. It always returns a Result carrying a
// non-empty Caveats list (AC3) and a Verdict that is honest about the
// engine's evaluation limits (AC5).
func (e *Engine) Simulate(req Request) Result {
	// T-2506's deliberate-slowdown fixture. Empty and inlined away in every
	// build that does not set the `perfslow` tag — see perfslow_off.go.
	perfSlowWork()

	res := Result{Proto: req.Proto, Port: req.Port, Hops: []Hop{}}
	family := req.Family.orDefault()

	src := e.resolveEndpoint(req.Src, family, &res)
	dst := e.resolveEndpoint(req.Dst, family, &res)
	res.Src = src.public
	res.Dst = dst.public

	// An endpoint that named an unknown/unsupported entity kind, or a guest
	// NIC whose attachment does not resolve, is fatal to a confident
	// verdict: resolveEndpoint has already attached the blocker caveat.
	if src.fatal || dst.fatal {
		res.Verdict = VerdictIndeterminate
		finalize(&res)
		return res
	}

	// Reachability (L2 then L3). Populates hops and, on a break, Missing.
	reach := e.reachability(src, dst, &res)
	switch reach.state {
	case pathUnreachable:
		res.Verdict = VerdictUnreachable
		res.Missing = reach.missing
		finalize(&res)
		return res
	case pathIndeterminate:
		res.Verdict = VerdictIndeterminate
		finalize(&res)
		return res
	}

	// Firewall enforcement at every point the path crosses, in PVE order.
	fwOutcome := e.evaluateFirewall(src, dst, req, &res)
	switch fwOutcome.state {
	case fwDeny:
		res.Verdict = VerdictDeny
		res.BlockingRule = fwOutcome.blocking
	case fwIndeterminate:
		res.Verdict = VerdictIndeterminate
	default:
		res.Verdict = VerdictAllow
	}
	finalize(&res)
	return res
}

// finalize appends the standing honesty caveats and guarantees the Result's
// slices are non-nil for stable JSON ([] not null).
func finalize(res *Result) {
	for _, c := range standingCaveats() {
		res.addCaveat(c)
	}
	if res.Hops == nil {
		res.Hops = []Hop{}
	}
	if res.Caveats == nil {
		res.Caveats = []Caveat{}
	}
}
