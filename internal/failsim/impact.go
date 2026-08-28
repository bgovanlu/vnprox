// SPDX-License-Identifier: Apache-2.0

package failsim

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
	"github.com/bgovanlu/vnprox/internal/wireguard"
)

// Severity ranks an Impact from harmless to connectivity-severing. It is a
// coarse rollup of the structured fields below (never a substitute for them):
// a caller that needs to gate an unattended apply reads QuorumRisk /
// MgmtPathLoss directly (see ImpactPreflighter), not this label.
const (
	// SeverityNone: removing the target breaks nothing this simulator can
	// see, and every dimension was actually evaluated (empty NotEvaluated).
	SeverityNone = "none"
	// SeverityInfo: nothing known-broken, but at least one dimension could
	// not be assessed (NotEvaluated non-empty) — an honest "unknown", not a
	// clean bill of health.
	SeverityInfo = "info"
	// SeverityWarning: guests lose their uplink or SDN segments are stranded,
	// but corosync/Ceph/management stay intact.
	SeverityWarning = "warning"
	// SeverityCritical: quorum, a management path, or a Ceph network is put
	// at risk — the classes an unattended apply must never trigger blind.
	SeverityCritical = "critical"
)

// Input is Simulate's world: the live inventory snapshot plus the optional
// side-tables the connectivity graph does not itself carry. A nil/absent
// side-table degrades its dimension to NotEvaluated (never a false "no
// impact") — see the honesty contract in doc.go.
type Input struct {
	Corosync *host.CorosyncConfig
	Ceph     *ceph.Status
	Snapshot inventory.Snapshot
	Tunnels  []wireguard.Tunnel
}

// Impact is the computed blast radius of removing one entity. Every field is
// derived fresh from Input.Snapshot; nothing here is persisted. The struct is
// the machine-checkable answer to "what breaks if this dies?", and its
// NotEvaluated list is the load-bearing honesty channel: a caller must treat
// a dimension named there as unknown, never as safe.
type Impact struct {
	Target             inventory.Ref
	Severity           string
	DisconnectedGuests []inventory.Ref
	StrandedVlans      []inventory.Ref
	MgmtPathLoss       []string
	NotEvaluated       []string
	QuorumRisk         bool
	CephRisk           bool
}

// nonZero reports whether the Impact represents any known or unknown effect —
// used by the SPOF inventory to decide whether an element is worth listing.
// A pure "not evaluated" element (some dimension unknown, nothing known
// broken) is deliberately treated as zero here: the SPOF inventory lists
// elements with a *known* nonzero blast radius, and the honesty gap is
// surfaced per-element via NotEvaluated on demand, not as a phantom SPOF.
func (im Impact) nonZero() bool {
	return len(im.DisconnectedGuests) > 0 || len(im.StrandedVlans) > 0 ||
		len(im.MgmtPathLoss) > 0 || im.QuorumRisk || im.CephRisk
}

// Simulate computes the Impact of removing target from in.Snapshot. It is
// pure and side-effect-free: it builds a post-failure copy of the graph with
// target (and, for a node/switch target, everything that physically dies with
// it) removed, then recomputes connectivity, quorum, Ceph isolation and
// management-path loss over that copy.
func Simulate(in Input, target inventory.Ref) Impact {
	pre := in.Snapshot
	removed := removalClosure(pre, target)
	post := rebuildExcluding(pre, removed)

	im := Impact{Target: target}

	// --- guest connectivity + stranded SDN segments -----------------------
	vnetByID, zoneByID := sdnIndex(pre)
	disc, unresolvedGuests := disconnectedGuests(pre, post, vnetByID, zoneByID)
	im.DisconnectedGuests = disc
	im.StrandedVlans = strandedVlans(pre, post, vnetByID, zoneByID)
	if unresolvedGuests > 0 {
		im.NotEvaluated = append(im.NotEvaluated, DimGuestConnectivity)
	}

	// --- management-path loss (shared resolver, post-failure) -------------
	im.MgmtPathLoss = mgmtPathLoss(pre, post, in.Corosync)

	// --- corosync quorum --------------------------------------------------
	quorum, quorumOK := quorumRisk(pre, post, in.Corosync)
	if quorumOK {
		im.QuorumRisk = quorum
	} else {
		im.NotEvaluated = append(im.NotEvaluated, DimQuorum)
	}

	// --- Ceph public/cluster network isolation ----------------------------
	cephR, cephOK := cephRisk(pre, post, in.Ceph)
	if cephOK {
		im.CephRisk = cephR
	} else {
		im.NotEvaluated = append(im.NotEvaluated, DimCeph)
	}

	// --- WireGuard tunnels ------------------------------------------------
	// Tunnel-borne connectivity is only assessable when the tunnel model is
	// supplied; absent it, we cannot claim tunnels are unaffected.
	if len(in.Tunnels) == 0 {
		im.NotEvaluated = append(im.NotEvaluated, DimTunnels)
	}

	sortRefs(im.DisconnectedGuests)
	sortRefs(im.StrandedVlans)
	sort.Strings(im.MgmtPathLoss)
	sort.Strings(im.NotEvaluated)
	im.Severity = severityOf(im)
	return im
}

func severityOf(im Impact) string {
	switch {
	case im.QuorumRisk || len(im.MgmtPathLoss) > 0 || im.CephRisk:
		return SeverityCritical
	case len(im.DisconnectedGuests) > 0 || len(im.StrandedVlans) > 0:
		return SeverityWarning
	case len(im.NotEvaluated) > 0:
		return SeverityInfo
	default:
		return SeverityNone
	}
}

// --- removal + rebuild -----------------------------------------------------

// removalClosure is the set of refs that physically die when target is
// removed. A plain interface (bond/bridge/vlan/physnic/tunnel) removes only
// itself. A node removes every entity scoped to it (its NICs, bonds, bridges,
// guests). A switch (identified from LLDP) removes every node PhysNic facing
// it — the "one switch takes down every link plugged into it" case that makes
// a shared switch a genuine single point of failure.
func removalClosure(snap inventory.Snapshot, target inventory.Ref) map[inventory.Ref]bool {
	removed := map[inventory.Ref]bool{target: true}
	switch target.Kind {
	case inventory.KindNode:
		for _, e := range snap.All() {
			if e.GetRef().Node == target.ID {
				removed[e.GetRef()] = true
			}
		}
	case inventory.KindSwitchPort:
		// A switch target's ID is the LLDP chassis id/name of the switch (see
		// SwitchTargets). Every local NIC whose LLDP neighbor advertises that
		// chassis loses link when the switch dies.
		for _, e := range snap.All() {
			n, ok := e.(*inventory.LldpNeighbor)
			if !ok {
				continue
			}
			if n.ChassisID == target.ID || n.ChassisName == target.ID {
				if !n.LocalNic.IsZero() {
					removed[n.LocalNic] = true
				}
			}
		}
	}
	return removed
}

// rebuildExcluding returns a fresh snapshot identical to snap but with every
// ref in removed absent — the literal "removing target from a snapshot copy
// of the inventory graph and recomputing" the card describes. Entities are
// re-ingested through a real inventory.Graph under their owning source(s), so
// the resolver recomputes bridge ports, bond slaves, VLAN parents and guest
// attachments exactly as a live poll would (an interface naming a
// now-removed carrier simply fails to re-link, which is the whole point).
func rebuildExcluding(snap inventory.Snapshot, removed map[inventory.Ref]bool) inventory.Snapshot {
	g := inventory.NewGraph()

	// Group survivors by the source routing a live poll would use.
	hostByNode := map[string][]inventory.Entity{}
	bySource := map[inventory.Source][]inventory.Entity{}
	for _, e := range snap.All() {
		ref := e.GetRef()
		if removed[ref] {
			continue
		}
		switch ref.Kind {
		case inventory.KindPhysNic, inventory.KindBond, inventory.KindBridge,
			inventory.KindVlan, inventory.KindOVSBridge, inventory.KindOVSBond:
			hostByNode[ref.Node] = append(hostByNode[ref.Node], e)
		case inventory.KindLldpNeighbor:
			bySource[inventory.SourceHostLLDP] = append(bySource[inventory.SourceHostLLDP], e)
		case inventory.KindNode:
			bySource[inventory.SourcePVECluster] = append(bySource[inventory.SourcePVECluster], e)
		case inventory.KindSDNZone, inventory.KindSDNVnet, inventory.KindSDNSubnet,
			inventory.KindSDNDnsZone, inventory.KindSDNDnsRecord:
			bySource[inventory.SourcePVESDN] = append(bySource[inventory.SourcePVESDN], e)
		case inventory.KindGuest, inventory.KindGuestNic:
			bySource[inventory.SourcePVEGuest] = append(bySource[inventory.SourcePVEGuest], e)
		case inventory.KindFwRuleset:
			bySource[inventory.SourcePVEFirewall] = append(bySource[inventory.SourcePVEFirewall], e)
		}
	}

	// Host L2 entities are multi-source: apply under both the runtime and
	// declared sources (like the collector does) so every field resolves.
	for node, ents := range hostByNode {
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, ents)
		g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: node}, ents)
	}
	for src, ents := range bySource {
		g.ApplyPoll(src, inventory.Scope{}, ents)
	}
	return g.Snapshot()
}

// --- connectivity helpers --------------------------------------------------

// countLinkUp counts confirmed link-up PhysNics among nics in snap (a removed
// or link-down NIC never counts), mirroring topology.countLinkUp's tri-state
// treatment — an unreported link state is not "up".
func countLinkUp(snap inventory.Snapshot, nics []inventory.Ref) int {
	n := 0
	for _, ref := range nics {
		e, ok := snap.Get(ref)
		if !ok {
			continue
		}
		if p, ok := e.(*inventory.PhysNic); ok && p.LinkUpSet && p.LinkUp {
			n++
		}
	}
	return n
}

// liveUplinks returns how many link-up physical NICs carrier can still reach
// in snap, reusing the shared physical-path resolver — 0 means "no live
// uplink behind this carrier".
func liveUplinks(snap inventory.Snapshot, carrier inventory.Ref) int {
	_, nics := topology.ResolvePhysicalPath(snap, carrier)
	return countLinkUp(snap, nics)
}

// sdnIndex builds id→entity lookups for SDN vnets and zones.
func sdnIndex(snap inventory.Snapshot) (vnetByID map[string]*inventory.SdnVnet, zoneByID map[string]*inventory.SdnZone) {
	vnetByID = map[string]*inventory.SdnVnet{}
	zoneByID = map[string]*inventory.SdnZone{}
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.SdnVnet:
			vnetByID[v.ID] = v
		case *inventory.SdnZone:
			zoneByID[v.ID] = v
		}
	}
	return vnetByID, zoneByID
}

// guestCarriers resolves the uplink bridge(s) a guest's NICs ride, on the
// guest's own node. resolvable is false when a NIC names an attachment that
// does not resolve to a bridge or an SDN vnet-with-underlay-bridge — the
// honest "we cannot tell where this guest is attached" case that feeds
// DimGuestConnectivity rather than being silently dropped.
func guestCarriers(g *inventory.Guest, snap inventory.Snapshot, vnetByID map[string]*inventory.SdnVnet, zoneByID map[string]*inventory.SdnZone) (carriers []inventory.Ref, resolvable bool) {
	resolvable = true
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok || nic.Guest != g.Ref {
			continue
		}
		switch nic.BridgeOrVnet.Kind {
		case inventory.KindBridge, inventory.KindOVSBridge:
			carriers = append(carriers, nic.BridgeOrVnet)
		case inventory.KindSDNVnet:
			br, ok := vnetUnderlayBridge(nic.BridgeOrVnet.ID, g.Node, vnetByID, zoneByID)
			if !ok {
				resolvable = false
				continue
			}
			carriers = append(carriers, br)
		default:
			// A NIC with no resolved attachment at all (target bridge/vnet not
			// found): we cannot say whether the removal disconnects it.
			resolvable = false
		}
	}
	return carriers, resolvable
}

// vnetUnderlayBridge resolves an SDN vnet to the per-node underlay bridge that
// realizes it (the zone's bridge), so a vnet-attached guest's uplink can be
// evaluated the same way a plain-bridge guest's is. ok is false for a vnet
// whose zone declares no bridge (e.g. a pure overlay this model does not map)
// — surfaced as unresolved rather than assumed connected.
func vnetUnderlayBridge(vnetID, node string, vnetByID map[string]*inventory.SdnVnet, zoneByID map[string]*inventory.SdnZone) (inventory.Ref, bool) {
	vnet, ok := vnetByID[vnetID]
	if !ok {
		return inventory.Ref{}, false
	}
	zone, ok := zoneByID[vnet.Zone]
	if !ok || zone.Bridge == "" {
		return inventory.Ref{}, false
	}
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: zone.Bridge}, true
}

// disconnectedGuests returns every guest that had a live uplink pre-failure
// and none post-failure. unresolved counts guests whose attachment could not
// be resolved (they are excluded from the set but flagged via
// DimGuestConnectivity — never silently treated as unaffected).
func disconnectedGuests(pre, post inventory.Snapshot, vnetByID map[string]*inventory.SdnVnet, zoneByID map[string]*inventory.SdnZone) (disc []inventory.Ref, unresolved int) {
	for _, e := range pre.All() {
		g, ok := e.(*inventory.Guest)
		if !ok {
			continue
		}
		carriers, resolvable := guestCarriers(g, pre, vnetByID, zoneByID)
		if !resolvable {
			unresolved++
			continue
		}
		if !anyLiveUplink(pre, carriers) {
			continue // no external uplink to begin with — removal can't newly cut it
		}
		if !anyLiveUplink(post, carriers) {
			disc = append(disc, g.Ref)
		}
	}
	return disc, unresolved
}

func anyLiveUplink(snap inventory.Snapshot, carriers []inventory.Ref) bool {
	for _, c := range carriers {
		if liveUplinks(snap, c) > 0 {
			return true
		}
	}
	return false
}

// strandedVlans returns the SDN vnets that lose connectivity everywhere they
// were realized — a vnet with a live underlay uplink on some node pre-failure
// and none on any node post-failure. Per-node partial loss surfaces through
// disconnectedGuests instead, so nothing is silently dropped. Vnets whose
// zone declares no underlay bridge are not modeled here (documented boundary,
// honesty.go).
func strandedVlans(pre, post inventory.Snapshot, vnetByID map[string]*inventory.SdnVnet, zoneByID map[string]*inventory.SdnZone) []inventory.Ref {
	allNodes := nodeNames(pre)
	var out []inventory.Ref
	for _, vnet := range vnetByID {
		zone, ok := zoneByID[vnet.Zone]
		if !ok || zone.Bridge == "" {
			continue
		}
		nodes := zone.Nodes
		if len(nodes) == 0 {
			nodes = allNodes
		}
		preAny, postAny := false, false
		for _, node := range nodes {
			carrier := inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: zone.Bridge}
			if liveUplinks(pre, carrier) > 0 {
				preAny = true
			}
			if liveUplinks(post, carrier) > 0 {
				postAny = true
			}
		}
		if preAny && !postAny {
			out = append(out, vnet.Ref)
		}
	}
	return out
}

func nodeNames(snap inventory.Snapshot) []string {
	var out []string
	for _, e := range snap.All() {
		if n, ok := e.(*inventory.Node); ok {
			out = append(out, n.Name)
		}
	}
	return out
}

func nodeRef(name string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindNode, Node: name, ID: name}
}

// --- management-path loss --------------------------------------------------

// mgmtPathLoss returns the nodes whose management path (the carrier of the
// node's PVE management IP) retained a live physical uplink pre-failure but
// has none post-failure. It reuses the shared classifier + resolver
// (change.DetectProtectedRoles → topology.ResolveMgmtPaths) so its notion of
// "management connectivity" is identical to the T-703 interlock's and to
// GET /protected-interfaces/status — never a parallel one.
func mgmtPathLoss(pre, post inventory.Snapshot, cor *host.CorosyncConfig) []string {
	roleRefs := change.DetectProtectedRoles(pre, cor)
	if len(roleRefs) == 0 {
		return nil
	}
	prePaths := topology.ResolveMgmtPaths(pre, roleRefs)
	postPaths := topology.ResolveMgmtPaths(post, roleRefs)
	var lost []string
	for node, list := range prePaths {
		preUp := sumMgmtUp(pre, list)
		postUp := sumMgmtUp(post, postPaths[node])
		if preUp > 0 && postUp == 0 {
			lost = append(lost, node)
		}
	}
	return lost
}

// sumMgmtUp counts link-up physical NICs across the management-role carriers
// in paths, from the exact ResolveMgmtPaths output (its Path members plus a
// carrier that is itself a PhysNic). Corosync-only carriers are excluded —
// their loss is the quorum dimension's concern, not the management path's.
func sumMgmtUp(snap inventory.Snapshot, paths []topology.MgmtPath) int {
	total := 0
	for _, p := range paths {
		if !hasRole(p.Roles, topology.MgmtRoleMgmt) {
			continue
		}
		total += countLinkUp(snap, physNicsOf(snap, p))
	}
	return total
}

// physNicsOf returns the PhysNic refs behind a resolved MgmtPath: its Path
// members that are PhysNics, plus the carrier itself when the carrier is a
// bare PhysNic (ResolvePhysicalPath puts a PhysNic carrier in nics, not Path,
// so MgmtPath.Path would be empty for it).
func physNicsOf(snap inventory.Snapshot, p topology.MgmtPath) []inventory.Ref {
	var out []inventory.Ref
	for _, ref := range p.Path {
		if ref.Kind == inventory.KindPhysNic {
			out = append(out, ref)
		}
	}
	if p.Ref.Kind == inventory.KindPhysNic {
		out = append(out, p.Ref)
	}
	return out
}

func hasRole(roles []topology.MgmtRole, want topology.MgmtRole) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// --- quorum ----------------------------------------------------------------

// quorumRisk reports whether removing the target drops the count of
// reachable, quorum-voting corosync nodes below floor(N/2)+1. ok is false —
// the dimension is not evaluated — when there is no corosync config, fewer
// than two configured nodes (no meaningful quorum), or any voting node whose
// ring addresses do not resolve to an interface (its post-failure reachability
// cannot be determined, so a confident boolean would be dishonest).
func quorumRisk(pre, post inventory.Snapshot, cor *host.CorosyncConfig) (risk, ok bool) {
	if cor == nil || len(cor.Nodes) < 2 {
		return false, false
	}
	roleRefs := change.DetectProtectedRoles(pre, cor)
	corosyncCarriers := map[string][]inventory.Ref{}
	for node, list := range roleRefs {
		for _, rr := range list {
			if hasRole(rr.Roles, topology.MgmtRoleCorosync) {
				corosyncCarriers[node] = append(corosyncCarriers[node], rr.Ref)
			}
		}
	}
	// Every voting node must have at least one resolvable corosync carrier,
	// else we cannot assess its reachability post-failure.
	for _, cn := range cor.Nodes {
		if len(corosyncCarriers[cn.Name]) == 0 {
			return false, false
		}
	}
	voters := 0
	for _, cn := range cor.Nodes {
		if _, exists := post.Get(nodeRef(cn.Name)); !exists {
			continue // the node itself died
		}
		reachable := false
		for _, c := range corosyncCarriers[cn.Name] {
			if liveUplinks(post, c) > 0 {
				reachable = true
				break
			}
		}
		if reachable {
			voters++
		}
	}
	needed := len(cor.Nodes)/2 + 1
	return voters < needed, true
}

// --- ceph ------------------------------------------------------------------

// cephRisk reports whether removing the target isolates any OSD-hosting node
// from Ceph's public or cluster network — a node that had a resolved carrier
// on that network pre-failure and none post-failure. It reuses T-1503's read
// model (ceph.Project) over both snapshots. ok is false — not evaluated —
// when no Ceph status is supplied or Ceph declares no networks.
func cephRisk(pre, post inventory.Snapshot, status *ceph.Status) (risk, ok bool) {
	if status == nil || (status.PublicNetwork == "" && status.ClusterNetwork == "") {
		return false, false
	}
	preAttr := attributionByNode(ceph.Project(pre, *status))
	postAttr := attributionByNode(ceph.Project(post, *status))
	for node, pa := range preAttr {
		post, hadPost := postAttr[node]
		if !pa.PublicCarrier.IsZero() && (!hadPost || post.PublicCarrier.IsZero()) {
			return true, true
		}
		if !pa.ClusterCarrier.IsZero() && (!hadPost || post.ClusterCarrier.IsZero()) {
			return true, true
		}
	}
	return false, true
}

func attributionByNode(o ceph.Overlay) map[string]ceph.NodeAttribution {
	out := make(map[string]ceph.NodeAttribution, len(o.Nodes))
	for _, na := range o.Nodes {
		out[na.Node] = na
	}
	return out
}

// --- sorting ---------------------------------------------------------------

func sortRefs(rs []inventory.Ref) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}
