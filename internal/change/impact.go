// impact.go implements T-2404's blast-radius preview: what an operator would
// NOTICE if this changeset were applied, computed server-side from the ops plus
// the live inventory graph.
//
// THE GAP THIS CLOSES. The diff says *what changes* and the plan says *in what
// order*; nothing said *who notices*. "Update bridge vmbr0 on pve1" and "delete
// bridge vmbr0 on pve1" render as two similar-looking rows in a diff, and one
// of them takes eleven guests off the network.
//
// TWO RULES, both load-bearing:
//
//  1. EVERY VERDICT CARRIES ITS REASON. There is no way to produce a
//     Disruption without saying what produced it, because the constructor takes
//     both. A preview that over-claims with no explanation is worse than none:
//     it trains people to ignore it, and an ignored warning is the same as an
//     absent one.
//
//  2. IT IS COMPUTED SERVER-SIDE AND IS NOT AN INTERLOCK. Like
//     TouchesMgmtPath (mgmttouch.go), this is display metadata. The enforcement
//     backstops remain validate_safety.go's safety class and the mgmt-path
//     ceremony. Impact deliberately over-approximates for the same reason
//     TouchesMgmtPath does — a false "outage" costs one moment's extra care, a
//     false "none" is a lie at exactly the wrong time.
//
// Guest attribution walks the inventory graph's guest NICs, so it reports what
// is attached RIGHT NOW rather than what the changeset's author believed was
// attached when they staged it.

package change

import (
	"context"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// DisruptionClass ranks what an operator would observe.
type DisruptionClass string

const (
	// DisruptionNone: nothing on the data path moves. Creating a new,
	// unattached bridge; an IPAM allocation; a comment change.
	DisruptionNone DisruptionClass = "none"
	// DisruptionBrief: a reload that re-creates an existing carrier — traffic
	// on it stops and resumes. An MTU or address change on a live interface.
	DisruptionBrief DisruptionClass = "brief"
	// DisruptionOutage: something loses its path and does not get it back by
	// applying this changeset. A delete of an entity that has guests on it.
	DisruptionOutage DisruptionClass = "outage"
)

// disruptionRank orders the classes so Impact can report the worst.
var disruptionRank = map[DisruptionClass]int{
	DisruptionNone:   0,
	DisruptionBrief:  1,
	DisruptionOutage: 2,
}

// worse returns whichever class an operator should be told about.
func worse(a, b DisruptionClass) DisruptionClass {
	if disruptionRank[b] > disruptionRank[a] {
		return b
	}
	return a
}

// GuestImpact names one guest whose connectivity this changeset affects, and
// through what.
type GuestImpact struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Node    string `json:"node"`
	NIC     string `json:"nic"`
	Carrier string `json:"carrier"`
	VMID    int    `json:"vmid"`
}

// OpImpact is one op's contribution, always with the reason for its verdict.
type OpImpact struct {
	OpID       string          `json:"opId,omitempty"`
	Op         string          `json:"op"`
	Target     string          `json:"target,omitempty"`
	Disruption DisruptionClass `json:"disruption"`
	Reason     string          `json:"reason"`
}

// Impact is GET /changesets/{id}/impact's body.
type Impact struct {
	Disruption      DisruptionClass `json:"disruption"`
	Nodes           []string        `json:"nodes"`
	Carriers        []string        `json:"carriers"`
	Guests          []GuestImpact   `json:"guests"`
	Ops             []OpImpact      `json:"ops"`
	TouchesMgmtPath bool            `json:"touchesMgmtPath"`
}

// deletingOps are the op types that REMOVE an entity. A delete is the only
// shape that can strand a guest: every other op leaves the carrier in place,
// however much it changes about it.
var deletingOps = map[OpType]bool{
	OpBridgeDelete: true,
	OpBondDelete:   true,
	OpVlanDelete:   true,
}

// nonDisruptiveOps never move a packet: they create something that did not
// exist, or record app-owned bookkeeping. Anything NOT listed here and not a
// delete is treated as `brief`, which is the over-approximating direction —
// see this file's rule 2.
var nonDisruptiveOps = map[OpType]bool{
	OpBridgeCreate: true,
}

// ComputeImpact derives the blast radius of ops against the live inventory
// snapshot and the resolved management paths.
//
// mgmtPaths may be nil (management-path resolution unavailable); the result
// then reports TouchesMgmtPath false, exactly as TouchesMgmtPath itself does,
// rather than guessing.
func ComputeImpact(ops []Op, snap inventory.Snapshot, mgmtPaths map[string][]topology.MgmtPath, tunnelCarriers map[string]WgTunnelCarrier, mgmtSwitchPorts map[string]bool) Impact {
	out := Impact{
		Nodes:      []string{},
		Carriers:   []string{},
		Guests:     []GuestImpact{},
		Ops:        []OpImpact{},
		Disruption: DisruptionNone,
	}
	if len(ops) == 0 {
		return out
	}

	nodeSet := map[string]bool{}
	// carrierSet is a SET of affected carrier refs — every value is true.
	// It deliberately does not double as a "was this a delete" map:
	// guestsOnCarriers tests membership by value, so storing false for a
	// non-deleting op would silently drop that carrier's guests from the
	// report. (It did, until TestImpact_UpdateOnAUsedBridgeIsBriefNotAnOutage
	// caught it — which is the whole reason that control test exists.)
	carrierSet := map[inventory.Ref]bool{}

	for _, op := range ops {
		target := op.Target
		if target.Node != "" {
			nodeSet[target.Node] = true
		}

		deleting := deletingOps[op.Type]
		if !target.IsZero() && isCarrierKind(target.Kind) {
			carrierSet[target] = true
		}

		class, reason := classifyOp(op, snap, deleting)
		out.Ops = append(out.Ops, OpImpact{
			OpID: op.ID, Op: string(op.Type), Target: refString(target),
			Disruption: class, Reason: reason,
		})
		out.Disruption = worse(out.Disruption, class)
	}

	for n := range nodeSet {
		out.Nodes = append(out.Nodes, n)
	}
	sort.Strings(out.Nodes)
	for c := range carrierSet {
		out.Carriers = append(out.Carriers, c.String())
	}
	sort.Strings(out.Carriers)

	out.Guests = guestsOnCarriers(snap, carrierSet)
	out.TouchesMgmtPath = TouchesMgmtPath(mgmtPaths, tunnelCarriers, mgmtSwitchPorts, ops)
	return out
}

// isCarrierKind reports whether a Ref names something a guest NIC can be
// attached to. SDN VNets count: a guest NIC's BridgeOrVnet resolves to one.
func isCarrierKind(k inventory.Kind) bool {
	switch k {
	case inventory.KindBridge, inventory.KindBond, inventory.KindVlan, inventory.KindSDNVnet:
		return true
	default:
		return false
	}
}

func refString(r inventory.Ref) string {
	if r.IsZero() {
		return ""
	}
	return r.String()
}

// classifyOp returns one op's disruption class AND the reason for it. The two
// are returned together, never separately, so a verdict without an explanation
// is not expressible.
func classifyOp(op Op, snap inventory.Snapshot, deleting bool) (DisruptionClass, string) {
	if deleting {
		if n := len(guestsOnCarriers(snap, map[inventory.Ref]bool{op.Target: true})); n > 0 {
			return DisruptionOutage, pluralGuests(n) + " attached to " + op.Target.String() + " lose their path"
		}
		return DisruptionOutage, op.Target.String() + " is removed"
	}
	if nonDisruptiveOps[op.Type] {
		return DisruptionNone, "creates a new entity; nothing existing is touched"
	}
	if !op.Target.IsZero() && isCarrierKind(op.Target.Kind) {
		if n := len(guestsOnCarriers(snap, map[inventory.Ref]bool{op.Target: true})); n > 0 {
			return DisruptionBrief, "reload re-creates " + op.Target.String() + "; " + pluralGuests(n) + " briefly lose traffic"
		}
		return DisruptionBrief, "reload re-creates " + op.Target.String() + ", which carries no guests"
	}
	return DisruptionBrief, "applied during the node's interfaces reload"
}

func pluralGuests(n int) string {
	if n == 1 {
		return "1 guest"
	}
	return itoa(n) + " guests"
}

// itoa avoids importing strconv for one call site in a hot-ish path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// guestsOnCarriers returns every guest with a NIC attached to one of the given
// carriers, deduplicated by (guest, nic) and deterministically ordered.
//
// It reads the LIVE graph, so a guest attached after the changeset was staged
// is counted and one detached since is not — the preview describes applying
// now, which is when it is shown.
func guestsOnCarriers(snap inventory.Snapshot, carriers map[inventory.Ref]bool) []GuestImpact {
	if len(carriers) == 0 {
		return []GuestImpact{}
	}
	guestByRef := map[inventory.Ref]*inventory.Guest{}
	var nics []*inventory.GuestNic
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Guest:
			guestByRef[v.Ref] = v
		case *inventory.GuestNic:
			nics = append(nics, v)
		}
	}

	out := []GuestImpact{}
	for _, nic := range nics {
		if !carriers[nic.BridgeOrVnet] {
			continue
		}
		gi := GuestImpact{
			Ref:     nic.Guest.String(),
			Node:    nic.Guest.Node,
			NIC:     nic.Key,
			Carrier: nic.BridgeOrVnet.String(),
		}
		if g, ok := guestByRef[nic.Guest]; ok {
			gi.Name = g.Name
			gi.VMID = g.VMID
			if g.Node != "" {
				gi.Node = g.Node
			}
		}
		out = append(out, gi)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref != out[j].Ref {
			return out[i].Ref < out[j].Ref
		}
		return out[i].NIC < out[j].NIC
	})
	return out
}

// Impact computes the blast-radius preview for a changeset by id (T-2404,
// GET /changesets/{id}/impact).
//
// Management-path resolution is supplied by the caller (the API layer already
// resolves it per request for the ceremony gate) rather than being re-derived
// here, so the impact panel and the mandatory-acknowledgement block can never
// disagree about whether a changeset touches the management path.
func (s *Service) Impact(ctx context.Context, id string, mgmtPaths map[string][]topology.MgmtPath, tunnelCarriers map[string]WgTunnelCarrier, mgmtSwitchPorts map[string]bool) (Impact, error) {
	cs, err := s.Get(ctx, id)
	if err != nil {
		return Impact{}, err
	}
	return ComputeImpact(cs.Ops, s.inventorySnapshot(), mgmtPaths, tunnelCarriers, mgmtSwitchPorts), nil
}
