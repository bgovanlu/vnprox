// Package topology projects the T-103 inventory graph into the renderable
// contract docs/features/topology.md §3 documents: nodes[] carrying
// id/kind/label/layer/nodeGroup/status/badges, and edges[] carrying
// from/to/kind/badges/status. The frontend owns layout (React Flow +
// elkjs, T-107); this package owns structure and status.
package topology

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// layerOf maps an inventory Kind to its rendering layer. Kinds with no entry
// (KindNode, KindFwRuleset) are never rendered directly as topology nodes —
// cluster membership and firewall rulesets are surfaced by other routes
// (docs/api.md's /auth/me and /firewall/* respectively), not the topology
// map.
func layerOf(k inventory.Kind) (Layer, bool) {
	switch k {
	case inventory.KindPhysNic, inventory.KindLldpNeighbor:
		return LayerPhysical, true
	case inventory.KindBond, inventory.KindOVSBond, inventory.KindBridge, inventory.KindOVSBridge, inventory.KindVlan:
		return LayerL2, true
	case inventory.KindSDNZone, inventory.KindSDNVnet, inventory.KindSDNSubnet:
		return LayerSDN, true
	case inventory.KindGuest, inventory.KindGuestNic:
		return LayerGuest, true
	default:
		return "", false
	}
}

// Project builds the full Topology for snap, applying f's server-side
// filters (docs/features/topology.md §3).
func Project(snap inventory.Snapshot, f Filter) Topology {
	all := snap.All()

	// candidateRefs is the set of entity Refs eligible for rendering after
	// kind/node/layer filtering — before VLAN filtering and guest collapse.
	candidate := make(map[inventory.Ref]bool, len(all))
	byRef := make(map[inventory.Ref]inventory.Entity, len(all))
	for _, e := range all {
		ref := e.GetRef()
		byRef[ref] = e
		layer, ok := layerOf(ref.Kind)
		if !ok {
			continue
		}
		if !f.hasLayer(layer) {
			continue
		}
		if f.Node != "" && !ref.ClusterScoped() && ref.Node != f.Node {
			continue
		}
		candidate[ref] = true
	}

	if f.VLAN > 0 {
		candidate = restrictToVLAN(snap, byRef, candidate, f.VLAN)
	}

	nodes, groups := buildNodes(snap, byRef, candidate)
	nodes, groupEdges := collapseGuests(nodes, groups, byRef)

	nodeSet := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n.ID] = true
	}
	statusByID := make(map[string]Status, len(nodes))
	for _, n := range nodes {
		statusByID[n.ID] = n.Status
	}

	edges := buildEdges(snap, byRef, nodeSet, statusByID)
	edges = append(edges, groupEdges...)

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})

	return Topology{
		Nodes:       nodes,
		Edges:       edges,
		Layers:      AllLayers,
		GeneratedAt: snap.GeneratedAt().Unix(),
	}
}

// restrictToVLAN narrows candidate to the entities that directly carry vid
// (see entityVID) plus, transitively, the other endpoint of any edge whose
// badges name that VID or that touches a direct carrier — so a VLAN filter
// still yields renderable edges (both endpoints present) rather than
// dangling ones, without pulling in the whole connectivity chain
// (docs/features/topology.md §3's server-side "?vlan=" contract; the client
// still gets full dim-not-remove behavior for interactive VLAN entry per §2,
// which is a UI concern layered on top of this narrower server-side set).
func restrictToVLAN(snap inventory.Snapshot, byRef map[inventory.Ref]inventory.Entity, candidate map[inventory.Ref]bool, vid int) map[inventory.Ref]bool {
	direct := map[inventory.Ref]bool{}
	for ref := range candidate {
		if entityCarriesVID(byRef[ref], vid) {
			direct[ref] = true
		}
	}

	out := map[inventory.Ref]bool{}
	for ref := range direct {
		out[ref] = true
	}
	vidBadge := "vid=" + strconv.Itoa(vid)
	for _, e := range snap.Edges() {
		if !candidate[e.From] || !candidate[e.To] {
			continue
		}
		if direct[e.From] || direct[e.To] || hasBadge(e.Badges, vidBadge) {
			out[e.From] = true
			out[e.To] = true
		}
	}
	return out
}

func hasBadge(badges []string, want string) bool {
	for _, b := range badges {
		if b == want {
			return true
		}
	}
	return false
}

// entityCarriesVID reports whether e itself declares/carries VLAN vid.
func entityCarriesVID(e inventory.Entity, vid int) bool {
	switch v := e.(type) {
	case *inventory.VlanIface:
		return v.Vid == vid
	case *inventory.GuestNic:
		return v.EffectiveVid == vid
	case *inventory.Bridge:
		return v.VlanAware && vidsContain(v.Vids, vid)
	case *inventory.SdnVnet:
		return v.Tag == vid
	case *inventory.LldpNeighbor:
		return v.VLAN == vid
	default:
		return false
	}
}

func vidsContain(ranges []inventory.VidRange, vid int) bool {
	for _, r := range ranges {
		if vid >= r.Low && vid <= r.High {
			return true
		}
	}
	return false
}

// nodeGroupOf returns the rendering column an entity belongs to:
// docs/features/topology.md §1's per-cluster-node band for node-scoped
// entities, or the empty string sentinel for the cluster-scoped SDN band
// that spans all nodes.
func nodeGroupOf(ref inventory.Ref) string {
	if ref.ClusterScoped() {
		return ""
	}
	return ref.Node
}

// buildNodes projects every candidate entity to a rendered Node. groups
// collects, per (node, target-ref) attachment point, the GuestNic refs
// destined for guest-collapse handling in collapseGuests.
func buildNodes(snap inventory.Snapshot, byRef map[inventory.Ref]inventory.Entity, candidate map[inventory.Ref]bool) ([]Node, map[string][]inventory.Ref) {
	nodes := make([]Node, 0, len(candidate))
	groups := map[string][]inventory.Ref{}

	for ref := range candidate {
		e := byRef[ref]
		layer, _ := layerOf(ref.Kind)

		if nic, ok := e.(*inventory.GuestNic); ok && !nic.BridgeOrVnet.IsZero() {
			key := ref.Node + "|" + nic.BridgeOrVnet.String()
			groups[key] = append(groups[key], ref)
		}

		nodes = append(nodes, Node{
			ID:        ref.String(),
			Kind:      string(ref.Kind),
			Label:     labelOf(snap, e),
			Layer:     layer,
			NodeGroup: nodeGroupOf(ref),
			Status:    statusOf(snap, e),
			Badges:    badgesOf(snap, e),
		})
	}
	return nodes, groups
}

// labelOf renders the human-facing label for e.
func labelOf(snap inventory.Snapshot, e inventory.Entity) string {
	switch v := e.(type) {
	case *inventory.PhysNic:
		return v.Name
	case *inventory.Bond:
		return v.Name
	case *inventory.Bridge:
		return v.Name
	case *inventory.VlanIface:
		return v.Name
	case *inventory.SdnZone:
		return v.ID
	case *inventory.SdnVnet:
		if v.Alias != "" {
			return fmt.Sprintf("%s (%s)", v.ID, v.Alias)
		}
		return v.ID
	case *inventory.SdnSubnet:
		return v.ID
	case *inventory.Guest:
		return v.Name
	case *inventory.GuestNic:
		label := v.Key
		if g, ok := snap.Get(v.Guest); ok {
			if guest, ok := g.(*inventory.Guest); ok && guest.Name != "" {
				label = guest.Name + "/" + v.Key
			}
		}
		return label
	case *inventory.LldpNeighbor:
		if v.ChassisName != "" {
			return v.ChassisName
		}
		return v.ChassisID
	default:
		return e.GetRef().ID
	}
}

// statusOf derives the rendering status for e (docs/features/topology.md
// §2 "Status painting").
//
// PhysNic.LinkUp and Bond.Slaves are host-netlink-only fields (see
// merge.go's ownershipRules): a node this daemon hasn't (yet, or ever, for
// a peer node the collector doesn't poll directly — see doc.go and this
// package's T-106 completion report) run a host poll against simply has no
// contribution for them, and pick() leaves such a field out of Provenance
// entirely rather than defaulting it to a zero value that would otherwise
// misrender as a confirmed "down"/"degraded". This checks that provenance
// before trusting the zero value.
func statusOf(snap inventory.Snapshot, e inventory.Entity) Status {
	prov, _ := snap.Provenance(e.GetRef())
	switch v := e.(type) {
	case *inventory.PhysNic:
		if _, known := prov.Fields["linkUp"]; !known {
			return StatusUnknown
		}
		if !v.LinkUp {
			return StatusDown
		}
		return StatusOK
	case *inventory.Bond:
		return bondStatus(v, prov)
	case *inventory.Bridge:
		if len(v.Ports) == 0 && len(v.PortNames) == 0 && len(v.DeclaredPortNames) == 0 {
			return StatusUnknown
		}
		return StatusOK
	case *inventory.VlanIface:
		return StatusOK
	case *inventory.SdnZone:
		return sdnZoneStatus(v)
	case *inventory.SdnVnet, *inventory.SdnSubnet:
		return StatusOK
	case *inventory.Guest:
		if v.Status == "running" {
			return StatusOK
		}
		return StatusUnknown
	case *inventory.GuestNic:
		if v.LinkDown {
			return StatusDown
		}
		return StatusOK
	case *inventory.LldpNeighbor:
		return StatusOK
	default:
		return StatusUnknown
	}
}

// bondStatus flags a degraded bond: a declared slave missing from the
// runtime membership, or any slave whose MII/active state says it isn't
// carrying traffic (docs/features/topology.md §2: "degraded bond (missing
// slave) = amber"). Returns unknown rather than guessing when no source
// has ever reported live "slaves" membership at all (see statusOf's doc
// comment) — an absent runtime slave list means "we don't know yet", not
// "zero slaves are up".
func bondStatus(b *inventory.Bond, prov inventory.Provenance) Status {
	if _, known := prov.Fields["slaves"]; !known {
		return StatusUnknown
	}
	if len(b.DeclaredSlaves) > 0 && len(b.Slaves) < len(b.DeclaredSlaves) {
		return StatusDegraded
	}
	for _, sd := range b.SlaveDetail {
		if !sd.Active {
			return StatusDegraded
		}
	}
	return StatusOK
}

// sdnZoneStatus flags a zone degraded if any node's realization status is
// reported and isn't a clean "ok", unknown if PVE hasn't reported any
// per-node status yet.
func sdnZoneStatus(z *inventory.SdnZone) Status {
	if len(z.NodeStatus) == 0 {
		return StatusUnknown
	}
	for _, st := range z.NodeStatus {
		if st != "" && !strings.EqualFold(st, "ok") {
			return StatusDegraded
		}
	}
	return StatusOK
}

// badgesOf renders the badges shown on a node's chip
// (docs/features/topology.md: "badges for VLAN ranges/enslavement").
func badgesOf(snap inventory.Snapshot, e inventory.Entity) []string {
	// Always non-nil: docs/features/topology.md §3 documents `badges[]` as
	// an array field, and a nil Go slice marshals to JSON `null` rather
	// than `[]` — every rendered node/edge should give the frontend a
	// consistently-typed array to range over.
	badges := []string{}
	switch v := e.(type) {
	case *inventory.Bond:
		if v.Mode != "" {
			badges = append(badges, "mode="+v.Mode)
		}
		prov, _ := snap.Provenance(v.GetRef())
		if bondStatus(v, prov) == StatusDegraded {
			badges = append(badges, "missing-slave")
		}
	case *inventory.Bridge:
		if v.VlanAware && len(v.Vids) > 0 {
			ranges := make([]string, len(v.Vids))
			for i, r := range v.Vids {
				ranges[i] = r.String()
			}
			sort.Strings(ranges)
			badges = append(badges, "vlans="+strings.Join(ranges, ","))
		}
	case *inventory.VlanIface:
		badges = append(badges, "vid="+strconv.Itoa(v.Vid))
	case *inventory.SdnZone:
		badges = append(badges, "type="+v.Type)
	case *inventory.SdnVnet:
		if v.Tag != 0 {
			badges = append(badges, "tag="+strconv.Itoa(v.Tag))
		}
	case *inventory.Guest:
		badges = append(badges, "vmid="+strconv.Itoa(v.VMID))
	case *inventory.GuestNic:
		if v.EffectiveVid != 0 {
			badges = append(badges, "vid="+strconv.Itoa(v.EffectiveVid))
		}
		if v.LinkDown {
			badges = append(badges, "link-down")
		}
	case *inventory.LldpNeighbor:
		if v.PortID != "" {
			badges = append(badges, "port="+v.PortID)
		}
	}
	return badges
}

// buildEdges projects every inventory edge whose both endpoints survived
// filtering into a renderable Edge, deriving status from the endpoints and
// enriching enslaved-by edges with the slave's active/backup role.
func buildEdges(snap inventory.Snapshot, byRef map[inventory.Ref]inventory.Entity, nodeSet map[string]bool, statusByID map[string]Status) []Edge {
	var out []Edge
	for _, e := range snap.Edges() {
		fromID, toID := e.From.String(), e.To.String()
		if !nodeSet[fromID] || !nodeSet[toID] {
			continue
		}
		badges := append([]string{}, e.Badges...)
		if e.Kind == inventory.EdgeEnslavedBy {
			badges = append(badges, slaveRoleBadges(byRef[e.To], e.From)...)
		}
		out = append(out, Edge{
			From:   fromID,
			To:     toID,
			Kind:   string(e.Kind),
			Badges: badges,
			Status: worstStatus(statusByID[fromID], statusByID[toID]),
		})
	}
	return out
}

// slaveRoleBadges returns "active"/"backup" (and "mii-down" if applicable)
// for the slave named by slaveRef inside bondEnt.
func slaveRoleBadges(bondEnt inventory.Entity, slaveRef inventory.Ref) []string {
	bond, ok := bondEnt.(*inventory.Bond)
	if !ok {
		return nil
	}
	for _, sd := range bond.SlaveDetail {
		if sd.Name != slaveRef.ID {
			continue
		}
		var badges []string
		if sd.Active {
			badges = append(badges, "active")
		} else {
			badges = append(badges, "backup")
		}
		if sd.MIIStatus != "" && !strings.EqualFold(sd.MIIStatus, "up") {
			badges = append(badges, "mii-down")
		}
		return badges
	}
	return nil
}
