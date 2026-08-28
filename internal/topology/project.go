// SPDX-License-Identifier: Apache-2.0

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
	"time"

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
	nodes, physGroupEdges := collapsePhysical(nodes, snap.Edges())
	groupEdges = append(groupEdges, physGroupEdges...)

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

	// T-3504: after the edges exist (so the ones touching a folded bridge can
	// be dropped with them) and before buildSwitches, which must not see an
	// fwbr as a switch candidate. See firewall_bridge.go.
	nodes, edges = foldFirewallBridges(nodes, edges)
	nodeSet = make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n.ID] = true
	}

	// T-302: switch merging (spec §2) is purely additive over the existing
	// per-neighbor lldp-neighbor nodes/lldp-adjacent edges built above —
	// see switches.go's doc comment.
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	candidateEnts := make(map[inventory.Ref]inventory.Entity, len(candidate))
	for ref := range candidate {
		candidateEnts[ref] = byRef[ref]
	}
	switchNodes, switchEdges := buildSwitches(candidateEnts, nodeSet, now)
	nodes = append(nodes, switchNodes...)
	edges = append(edges, switchEdges...)

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

// dnsHostRecordTypes is the set of SDN DNS record types that name a host
// identity (T-1204) — the ones a guest's dnsName badge correlates against. A
// PTR/TXT record isn't a forward host name, so it never contributes here.
var dnsHostRecordTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true}

// dnsNamesByHost indexes every SDN DNS host record in snap by its lowercased
// hostname keys (both the bare Name label and the full FQDN) to that record's
// FQDN — the lookup dnsNameForGuest resolves a guest's own name against. When
// two records share a key (e.g. an A and an AAAA for the same host), the
// lexicographically-first FQDN wins for determinism (they resolve to the same
// FQDN anyway; only the map value's identity matters, and it's identical).
func dnsNamesByHost(snap inventory.Snapshot) map[string]string {
	out := map[string]string{}
	for _, e := range snap.All() {
		rec, ok := e.(*inventory.SdnDnsRecord)
		if !ok || !dnsHostRecordTypes[rec.Type] {
			continue
		}
		fq := dnsFQDN(rec.Name, rec.Zone)
		if fq == "" {
			continue
		}
		for _, key := range []string{strings.ToLower(rec.Name), strings.ToLower(fq)} {
			if key == "" {
				continue
			}
			if existing, seen := out[key]; !seen || fq < existing {
				out[key] = fq
			}
		}
	}
	return out
}

// dnsFQDN joins a record's name label and zone into a fully-qualified name,
// mirroring internal/sdn.fqdn (kept local here so internal/topology needn't
// import internal/sdn just for one string join).
func dnsFQDN(name, zone string) string {
	switch {
	case name == "" || name == "@":
		return zone
	case zone == "":
		return name
	default:
		return name + "." + zone
	}
}

// dnsNameForGuest returns the DNS FQDN correlated to a Guest or GuestNic node
// (via the guest's own hostname), or "" if none. Correlation is by hostname
// (Guest.Name) — the map's key set covers both a record's bare label and its
// FQDN, so a guest named either "web1" or "web1.example.com" matches. IP-based
// correlation would additionally need guest-agent-reported guest IPs, which
// the inventory snapshot does not carry (see the T-1204 report / needs-
// hardware-validation.md).
func dnsNameForGuest(snap inventory.Snapshot, e inventory.Entity, dnsByHost map[string]string) string {
	if len(dnsByHost) == 0 {
		return ""
	}
	var name string
	switch v := e.(type) {
	case *inventory.Guest:
		name = v.Name
	case *inventory.GuestNic:
		if g, ok := snap.Get(v.Guest); ok {
			if guest, ok := g.(*inventory.Guest); ok {
				name = guest.Name
			}
		}
	default:
		return ""
	}
	if name == "" {
		return ""
	}
	return dnsByHost[strings.ToLower(name)]
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

	// T-1204: index SDN DNS records by guest hostname so a Guest/GuestNic
	// node can carry its matching record's FQDN as a dnsName badge. Records
	// live in the same snapshot (SdnDnsRecord entities), so this stays a pure
	// function of the snapshot.
	dnsByHost := dnsNamesByHost(snap)

	for ref := range candidate {
		e := byRef[ref]
		layer, _ := layerOf(ref.Kind)

		if nic, ok := e.(*inventory.GuestNic); ok && !nic.BridgeOrVnet.IsZero() {
			key := ref.Node + "|" + nic.BridgeOrVnet.String()
			groups[key] = append(groups[key], ref)
		}

		n := Node{
			ID:        ref.String(),
			Kind:      string(ref.Kind),
			Label:     labelOf(snap, e),
			Layer:     layer,
			NodeGroup: nodeGroupOf(ref),
			Status:    statusOf(snap, e),
			Badges:    badgesOf(snap, e),
		}
		if dns := dnsNameForGuest(snap, e, dnsByHost); dns != "" {
			n.DnsName = dns
			n.Badges = append(n.Badges, "dns:"+dns)
		}
		// T-3503: the negotiated speed and media/port type, for the
		// faceplate's port bodies. These two guards are deliberately
		// independent, not combined into one `if ok {` block: SpeedMbps is
		// guarded on >0 so Linux's "-1 = no carrier" never reaches the wire
		// as a speed (see Node.SpeedMbps), but MediaPort must NOT share
		// that guard — the kernel reports port type even with no carrier
		// (see Node.MediaPort), so a down copper link still needs to draw
		// as copper, not fall back to no port body at all.
		if nic, ok := e.(*inventory.PhysNic); ok {
			if nic.SpeedMbps > 0 {
				n.SpeedMbps = nic.SpeedMbps
			}
			n.MediaPort = nic.MediaPort
			// T-3907: same "carry it whenever the driver answered,
			// independent of SpeedMbps' own guard" rule as MediaPort — see
			// Node.Duplex's doc comment.
			n.Duplex = nic.Duplex
		}
		nodes = append(nodes, n)
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
// a peer node the collector doesn't poll directly — see doc.go) run a host
// poll against simply has no contribution for them, and the merge leaves
// such a field unresolved rather than defaulting it to a zero value that
// would otherwise misrender as a confirmed "down"/"degraded". For linkUp
// the resolved entity carries that tri-state directly (PhysNic.LinkUpSet is
// true iff some source actually reported the field — inventory's optional
// booleans); slaves has no companion flag, so bondStatus checks the field's
// provenance entry instead. Either way, "unreported" renders as unknown
// (grey), never as down/degraded.
func statusOf(snap inventory.Snapshot, e inventory.Entity) Status {
	prov, _ := snap.Provenance(e.GetRef())
	switch v := e.(type) {
	case *inventory.PhysNic:
		if !v.LinkUpSet {
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

// sdnZoneStatus paints a zone from its per-node realization status
// (docs/api.md's GET /nodes/{node}/sdn/zones "ok"|"pending"|"error"
// vocabulary — T-3701 replaced an invented per-zone
// GET /cluster/sdn/zones/{zone}/status that PVE 9.2.4 returns 501 for —
// mirrored in T-401's GET /sdn tree so the cockpit tree and this map
// painting agree): red (StatusDown) if any node reports "error" (the zone
// is broken there — e.g. its bridge is missing, AC4), amber
// (StatusDegraded) if any node reports "pending" with no outright error,
// unknown (StatusUnknown) if PVE hasn't reported any per-node status at
// all, OR if any reporting node itself carries the vnprox-synthesized
// "unknown" status (pve.ReconcileSDNZoneStatus's doc comment — a declared
// member node PVE had nothing to say about, confirmed live on a real
// two-node cluster, planning/reports/evidence/
// pve-9.2.4-cluster-vnprox-dev.txt) with no outright error/pending
// elsewhere, else ok. Priority when a zone has more than one of these:
// error > pending > unknown > ok — a confirmed problem always wins over
// "we don't know", and "we don't know" always wins over a silent ok, so a
// gap in PVE's own reporting can never paint the same as a healthy zone.
func sdnZoneStatus(z *inventory.SdnZone) Status {
	if len(z.NodeStatus) == 0 {
		return StatusUnknown
	}
	worst := StatusOK
	for _, st := range z.NodeStatus {
		switch {
		case st == "" || strings.EqualFold(st, "ok"):
			continue
		case strings.EqualFold(st, "error"):
			return StatusDown
		case strings.EqualFold(st, "unknown"):
			if worst == StatusOK {
				worst = StatusUnknown
			}
		default: // "pending", or any other non-ok/non-error/non-unknown status PVE reports
			worst = StatusDegraded
		}
	}
	return worst
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
		// "ovs" (T-407) is the map's distinguishing badge for every OVS
		// entity kind (mirrored below for Bridge/VlanIface-as-Int-Port) —
		// additive to the corner kind label (EntityNode already renders
		// "ovs-bond"/"ovs-bridge" there), giving OVS entities a second,
		// filterable/legend-able visual marker the way every other badge
		// works.
		if v.Kind == inventory.KindOVSBond {
			badges = append(badges, "ovs")
		}
		if v.Mode != "" {
			badges = append(badges, "mode="+v.Mode)
		}
		prov, _ := snap.Provenance(v.GetRef())
		if bondStatus(v, prov) == StatusDegraded {
			badges = append(badges, "missing-slave")
		}
	case *inventory.Bridge:
		if v.Kind == inventory.KindOVSBridge {
			badges = append(badges, "ovs")
		}
		if v.VlanAware && len(v.Vids) > 0 {
			ranges := make([]string, len(v.Vids))
			for i, r := range v.Vids {
				ranges[i] = r.String()
			}
			sort.Strings(ranges)
			badges = append(badges, "vlans="+strings.Join(ranges, ","))
		}
		// stp-root (T-3901): only when STP is administratively on — every
		// bridge trivially reports RootID==BridgeID (IsRoot) when STP is
		// off, since there's no protocol running to elect a real root (see
		// planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt).
		// Gating on StpState != 0 keeps that from painting every ordinary,
		// STP-disabled PVE bridge as "root".
		if v.STPState != nil && v.STPState.StpState != 0 && v.STPState.IsRoot {
			badges = append(badges, "stp-root")
		}
	case *inventory.VlanIface:
		// An OVS Int Port (VlanIface.Virt == "ovs" — it carries no
		// dedicated inventory.Kind of its own, see that field's doc
		// comment) badges its tag/trunks the same way a tagged VLAN
		// sub-interface / VLAN-aware bridge already do ("tag="/"vlans="),
		// so the existing VLAN-filter logic (web/src/topology/
		// projection.ts's badgeCarriesVlan) highlights it for free.
		if v.Virt == "ovs" {
			badges = append(badges, "ovs")
			if v.Vid != 0 {
				badges = append(badges, "tag="+strconv.Itoa(v.Vid))
			}
			if len(v.Trunks) > 0 {
				ranges := make([]string, len(v.Trunks))
				for i, r := range v.Trunks {
					ranges[i] = r.String()
				}
				sort.Strings(ranges)
				badges = append(badges, "vlans="+strings.Join(ranges, ","))
			}
		} else {
			badges = append(badges, "vid="+strconv.Itoa(v.Vid))
		}
	case *inventory.SdnZone:
		badges = append(badges, "type="+v.Type)
		if v.Pending != "" {
			badges = append(badges, "pending="+v.Pending)
		}
	case *inventory.SdnVnet:
		if v.Tag != 0 {
			badges = append(badges, "tag="+strconv.Itoa(v.Tag))
		}
		if v.Pending != "" {
			badges = append(badges, "pending="+v.Pending)
		}
	case *inventory.SdnSubnet:
		if v.Pending != "" {
			badges = append(badges, "pending="+v.Pending)
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
		// mac= (T-406): the frontend's guest-MAC picker (web/src/guests/
		// guestNics.ts) reads this straight out of the existing topology
		// badges array — the same additive convention every other
		// entity-specific badge here already follows (e.g. Guest's
		// "vmid=") — rather than adding a second, dedicated MAC-listing
		// API route just for this one picker.
		if v.Mac != "" {
			badges = append(badges, "mac="+v.Mac)
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
		if e.Kind == inventory.EdgePortOf {
			badges = append(badges, stpPortBadges(byRef[e.To], e.From)...)
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

// stpPortBadges returns "stp-state=<state>"/"stp-role=<role>" for the
// bridge port named by portRef inside bridgeEnt (T-3901), mirroring
// slaveRoleBadges' identical bond-edge precedent for EdgePortOf (bridge
// membership) edges instead of EdgeEnslavedBy (bond membership) ones.
//
// Nil (no badges at all) when the owning bridge has no live STP state, or
// has STP administratively off: root_id/bridge_id/port state are still
// populated by the kernel even with stp_state=0 (every bridge trivially
// reports itself root then — see
// planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt), so a
// "root"/"blocking" role badge would be meaningless noise on the common
// case of an ordinary, STP-disabled PVE bridge. Mirrors badgesOf's
// identical StpState != 0 gate for the bridge node's own "stp-root" badge.
func stpPortBadges(bridgeEnt inventory.Entity, portRef inventory.Ref) []string {
	br, ok := bridgeEnt.(*inventory.Bridge)
	if !ok || br.STPState == nil || br.STPState.StpState == 0 {
		return nil
	}
	for _, p := range br.STPState.Ports {
		if p.Port != portRef.ID {
			continue
		}
		var badges []string
		if p.State != "" {
			badges = append(badges, "stp-state="+p.State)
		}
		if p.Role != "" {
			badges = append(badges, "stp-role="+p.Role)
		}
		return badges
	}
	return nil
}
