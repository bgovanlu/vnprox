// SPDX-License-Identifier: Apache-2.0

// switches.go implements docs/features/lldp-discovery.md §2's map upgrade:
// "switch chassis rendered as physical-layer nodes; identical chassis IDs
// seen from multiple nodes/NICs merge into one switch entity — this is what
// makes the map show the actual wiring, including which nodes share a
// switch." It is purely additive to project.go's existing per-neighbor
// lldp-neighbor node/lldp-adjacent edge rendering (kept unchanged, still the
// basis of the inspector's "per-NIC neighbor detail"): every poll also
// yields one synthesized KindSwitch node per distinct chassis ID plus one
// EdgeLLDPPort edge per contributing PhysNic, so the map itself shows
// consolidated switches instead of one node per (NIC, neighbor) pair.

package topology

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// KindSwitch is the topology.Node.Kind value for a synthesized switch
// entity. It is not an inventory.Kind — a switch has no single backing
// inventory entity, it is a chassis-ID grouping over one-or-more
// inventory.LldpNeighbor observations — so it only ever appears as a
// rendered topology.Node, never as a valid inventory Ref / GET
// /inventory/{ref} target.
const KindSwitch = "switch"

// EdgeLLDPPort is the rendered edge kind from a PhysNic to the KindSwitch
// node it was observed connected to, carrying the switch port identity as a
// badge. Distinct from inventory.EdgeLldpAdjacent (PhysNic -> raw
// LldpNeighbor, docs/data-model.md's contract, unchanged) since this edge's
// target is a synthetic switch id, not an inventory Ref.
const EdgeLLDPPort = "lldp-port"

// switchNodeID is the rendered id of the switch merging every LldpNeighbor
// observation whose ChassisID normalizes to chassisID. Deliberately not a
// valid inventory Ref (kind:node:id triplet) — same convention as
// collapseGuests' "guest-group:" synthetic ids (project.go / collapse.go) —
// so the frontend can special-case it as "not an inspectable entity"
// exactly the way it already must for guest-collapse pills.
func switchNodeID(chassisID string) string {
	return "switch:" + normalizeChassisID(chassisID)
}

// normalizeChassisID canonicalizes a chassis ID for grouping purposes
// (case-insensitive; lldpd/switch vendors are inconsistent about MAC
// address casing in particular). Distinct chassis IDs that merely share a
// chassis *name* are never merged (AC1's "same-name/different-ID edge
// case") — grouping is keyed on ChassisID alone.
func normalizeChassisID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// lldpGreyAfter is the age threshold past which a neighbor observation
// greys on the map (docs/features/lldp-discovery.md §3: "entries carry
// lastSeen; entries older than 2×TTL grey out"). ttlSeconds <= 0 (never
// reported) falls back to a conservative default so a neighbor with no TTL
// at all still eventually greys rather than staying permanently "ok".
func lldpGreyAfter(ttlSeconds int) time.Duration {
	if ttlSeconds <= 0 {
		ttlSeconds = 120
	}
	return 2 * time.Duration(ttlSeconds) * time.Second
}

// lldpDropAfter is the fixed age threshold past which a neighbor drops from
// the map entirely (spec §3: "older than 10min drop from the map (kept in
// the table with a 'stale' tag for troubleshooting unplugged links)") —
// independent of TTL, unlike the grey threshold.
const lldpDropAfter = 10 * time.Minute

// lldpAge returns how long ago n was last observed, and whether it has ever
// been observed at all (LastSeen == 0, e.g. a hand-built entity in a test
// that never set it).
func lldpAge(n *inventory.LldpNeighbor, now time.Time) (age time.Duration, known bool) {
	if n.LastSeen == 0 {
		return 0, false
	}
	return now.Sub(time.Unix(n.LastSeen, 0)), true
}

// lldpDropped reports whether n has crossed the drop-from-map threshold.
func lldpDropped(n *inventory.LldpNeighbor, now time.Time) bool {
	age, known := lldpAge(n, now)
	return known && age > lldpDropAfter
}

// lldpNeighborStatus derives the per-neighbor rendering status feeding a
// switch's aggregate status and its lldp-port edge status: unknown (grey)
// once the neighbor has crossed its 2×TTL grey threshold or was never
// timestamped, ok otherwise.
func lldpNeighborStatus(n *inventory.LldpNeighbor, now time.Time) Status {
	age, known := lldpAge(n, now)
	if !known {
		return StatusUnknown
	}
	if age > lldpGreyAfter(n.TTL) {
		return StatusUnknown
	}
	return StatusOK
}

// switchAgg accumulates every LldpNeighbor observation grouped under one
// normalized chassis ID.
type switchAgg struct {
	chassisID   string // first-seen verbatim ChassisID (rendering label fallback)
	chassisName string
	neighbors   []*inventory.LldpNeighbor
}

// buildSwitches groups candidate's LldpNeighbor entities by chassis ID into
// merged switch nodes and PhysNic->switch edges (spec §2), applying the
// staleness lifecycle (spec §3, clock-injected via now): neighbors past the
// drop threshold are excluded entirely; neighbors past the grey threshold
// still render but paint StatusUnknown. Edges are only emitted for
// PhysNics present in nodeSet (already-filtered/rendered), so a ?layers= or
// ?node= filter that excludes a NIC also excludes its switch link; a switch
// left with zero surviving edges is dropped rather than rendered orphaned.
func buildSwitches(candidate map[inventory.Ref]inventory.Entity, nodeSet map[string]bool, now time.Time) ([]Node, []Edge) {
	groups := map[string]*switchAgg{}
	var order []string
	for _, e := range candidate {
		n, ok := e.(*inventory.LldpNeighbor)
		if !ok || n.ChassisID == "" {
			continue
		}
		if lldpDropped(n, now) {
			continue
		}
		key := normalizeChassisID(n.ChassisID)
		g, ok := groups[key]
		if !ok {
			g = &switchAgg{chassisID: n.ChassisID, chassisName: n.ChassisName}
			groups[key] = g
			order = append(order, key)
		}
		if g.chassisName == "" {
			g.chassisName = n.ChassisName
		}
		g.neighbors = append(g.neighbors, n)
	}
	sort.Strings(order)

	var nodes []Node
	var edges []Edge
	for _, key := range order {
		g := groups[key]

		var nicEdges []Edge
		status := StatusUnknown
		first := true
		nodeNames := map[string]bool{}
		for _, n := range g.neighbors {
			if n.LocalNic.IsZero() {
				continue
			}
			nicID := n.LocalNic.String()
			if !nodeSet[nicID] {
				continue
			}
			st := lldpNeighborStatus(n, now)
			if first {
				status = st
				first = false
			} else {
				status = worstStatus(status, st)
			}
			nodeNames[n.Node] = true
			nicEdges = append(nicEdges, Edge{
				From:   nicID,
				To:     switchNodeID(g.chassisID),
				Kind:   EdgeLLDPPort,
				Status: st,
				Badges: lldpPortBadges(n),
			})
		}
		if len(nicEdges) == 0 {
			// Every observation's NIC was filtered out (or unresolved) —
			// nothing left to anchor this switch to on the current view.
			continue
		}

		names := make([]string, 0, len(nodeNames))
		for nm := range nodeNames {
			names = append(names, nm)
		}
		sort.Strings(names)

		label := g.chassisName
		if label == "" {
			label = g.chassisID
		}
		nodes = append(nodes, Node{
			ID:        switchNodeID(g.chassisID),
			Kind:      KindSwitch,
			Label:     label,
			Layer:     LayerPhysical,
			NodeGroup: "",
			Status:    status,
			Badges:    []string{"nodes=" + strings.Join(names, ","), "links=" + strconv.Itoa(len(nicEdges))},
		})
		edges = append(edges, nicEdges...)
	}
	return nodes, edges
}

// SwitchPortScope is one switch port's T-1205 scoping facts, resolved from
// LLDP: whether it faces a known PVE node's PhysNic (writable at all), whether
// it carries a node's resolved management path (management-path interlock), and
// the management VLAN that must not be stripped from such a port. It is
// internal/topology's own small shape (not internal/change.SwitchPortState —
// change imports topology, so topology must not import change); cmd/vnproxd's
// switch-scope source maps one to the other.
type SwitchPortScope struct {
	PVEFacing bool
	MgmtPath  bool
	MgmtVLAN  int
}

// ResolveSwitchPortScope extends the management-path resolver one hop onto the
// physical switch (T-1205): for every LLDP neighbor observed on a cluster
// PhysNic whose management address identifies a registered switch
// (switchByMgmtIP: switch mgmt IP -> app-store switch id), it yields the scoped
// switch-port Ref ("switch-port::<switchID>/<portID>") and its facts —
// PVEFacing (always true here, since the local end is a known cluster PhysNic),
// MgmtPath (the local PhysNic is part of some node's resolved management path),
// and MgmtVLAN (the PVID the switch advertises on that port, the management VLAN
// a push must not strip). mgmtPaths is ResolveMgmtPaths' output; a nil map means
// "no management path known", so no port is flagged MgmtPath.
//
// This is a pure function so the daemon's switch-scope source and its tests can
// share one implementation rather than re-deriving the LLDP↔switch linkage.
func ResolveSwitchPortScope(snap inventory.Snapshot, mgmtPaths map[string][]MgmtPath, switchByMgmtIP map[string]string) map[string]SwitchPortScope {
	if len(switchByMgmtIP) == 0 {
		return nil
	}
	// Set of every PhysNic ref that is part of some node's management path.
	mgmtNics := map[inventory.Ref]bool{}
	for _, list := range mgmtPaths {
		for _, p := range list {
			mgmtNics[p.Ref] = true
			for _, ref := range p.Path {
				mgmtNics[ref] = true
			}
		}
	}

	out := map[string]SwitchPortScope{}
	for _, e := range snap.All() {
		n, ok := e.(*inventory.LldpNeighbor)
		if !ok || n.LocalNic.IsZero() || n.PortID == "" {
			continue
		}
		switchID := lookupSwitchID(n, switchByMgmtIP)
		if switchID == "" {
			continue
		}
		portRef := inventory.Ref{Kind: inventory.KindSwitchPort, ID: switchID + "/" + n.PortID}
		out[portRef.String()] = SwitchPortScope{
			PVEFacing: true,
			MgmtPath:  mgmtNics[n.LocalNic],
			MgmtVLAN:  n.VLAN,
		}
	}
	return out
}

// lookupSwitchID resolves a neighbor's registered switch id by matching any of
// its advertised management addresses against switchByMgmtIP.
func lookupSwitchID(n *inventory.LldpNeighbor, switchByMgmtIP map[string]string) string {
	if n.MgmtIP != "" {
		if id, ok := switchByMgmtIP[n.MgmtIP]; ok {
			return id
		}
	}
	for _, ip := range n.MgmtIPs {
		if id, ok := switchByMgmtIP[ip]; ok {
			return id
		}
	}
	return ""
}

// lldpPortBadges renders one lldp-port edge's badges: the switch port
// identity plus, when known, its native/tagged VLAN summary.
func lldpPortBadges(n *inventory.LldpNeighbor) []string {
	badges := []string{}
	if n.PortID != "" {
		badges = append(badges, "port="+n.PortID)
	}
	if n.VLAN != 0 {
		badges = append(badges, "pvid="+strconv.Itoa(n.VLAN))
	}
	if len(n.TaggedVLANs) > 0 {
		parts := make([]string, len(n.TaggedVLANs))
		for i, v := range n.TaggedVLANs {
			parts[i] = strconv.Itoa(v)
		}
		badges = append(badges, "tagged="+strings.Join(parts, ","))
	}
	return badges
}
