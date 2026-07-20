// mgmtpath.go implements the physical-path half of T-702's management-path
// visibility deliverable (docs/features/topology.md §3, docs/api.md's
// GET /protected-interfaces/status): given a set of already role-classified
// protected refs (which entity carries a node's management IP and/or its
// corosync ring addresses — computed by internal/change, since only that
// package knows about protected.json/corosync.conf), resolve the physical
// path each one rides on — carrier -> parent bridge (for a VLAN
// sub-interface carrier) -> bridge ports -> bond slaves -> PhysNics — and
// whether that path has redundancy (>=2 link-up physical NICs).
//
// This is the "shared classification/path resolver" the task card asks for:
// internal/change calls ResolveMgmtPaths to answer GET
// /protected-interfaces/status, and internal/api calls it a second time (via
// ApplyMgmtBadges) to paint the same data onto GET /topology's node badges —
// one walk, two consumers, matching the existing finding-badge overlay's
// "Project stays pure, internal/api decorates" seam (see api/topology.go's
// paintFindings/paintDrift/paintBadge). Project() itself is untouched: this
// file adds new, separate exported entry points rather than folding mgmt
// classification into badgesOf, since Project has no way to know about
// protected.json/corosync.conf (host-local, cluster-config data outside the
// inventory snapshot).

package topology

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// MgmtRole names a purpose a protected ref serves for its node
// (docs/features/topology.md §3): RoleMgmt when the ref carries the node's
// PVE management IP, RoleCorosync when it carries one of the node's
// corosync ring addresses. A single ref can carry both roles at once (e.g.
// a homelab node running corosync over its only bridge) — see AC1's
// "+corosync where rings match".
type MgmtRole string

const (
	MgmtRoleMgmt     MgmtRole = "mgmt"
	MgmtRoleCorosync MgmtRole = "corosync"
)

// MgmtPathBadge is the topology.Node.Badges token ApplyMgmtBadges paints on
// every entity in a resolved MgmtPath's Path (docs/features/topology.md §3's
// badge vocabulary) — distinct from the MgmtRole values themselves, which
// are painted only on the carrier (see ApplyMgmtBadges).
const MgmtPathBadge = "mgmt-path"

// MgmtRoleRef is one protected ref plus the role(s) it serves, the input to
// ResolveMgmtPaths. Produced by internal/change's DetectProtectedRoles (the
// detection-suggested set) or its confirmed-protected.json counterpart —
// this package deliberately doesn't know how a ref's roles were decided, it
// only resolves what already-classified refs are physically carried by.
type MgmtRoleRef struct {
	Ref   inventory.Ref
	Roles []MgmtRole
}

// MgmtPath is one resolved protected ref: its role(s), the physical path
// carrying it (docs/features/topology.md §3: "carrier -> parent bridge for
// VLAN sub-interfaces -> bridge ports -> bond slaves -> PhysNics" —
// excluding the carrier itself, which is Ref), and whether that path has
// >=2 link-up physical NICs.
type MgmtPath struct {
	Ref       inventory.Ref
	Roles     []MgmtRole
	Path      []inventory.Ref
	Redundant bool
}

// ResolveMgmtPaths walks refs (keyed by node name) against snap, resolving
// each ref's physical path and redundancy. Refs whose node no longer exists
// in snap, or whose ref itself can't be found, still get a MgmtPath entry
// (empty Path, Redundant false) — a stale protected.json entry naming a
// removed interface shouldn't silently vanish from the status endpoint, it
// should visibly show "nothing behind this anymore".
func ResolveMgmtPaths(snap inventory.Snapshot, refs map[string][]MgmtRoleRef) map[string][]MgmtPath {
	if len(refs) == 0 {
		return nil
	}
	out := make(map[string][]MgmtPath, len(refs))
	for node, roleRefs := range refs {
		list := make([]MgmtPath, 0, len(roleRefs))
		for _, rr := range roleRefs {
			path, nics := ResolvePhysicalPath(snap, rr.Ref)
			list = append(list, MgmtPath{
				Ref:       rr.Ref,
				Roles:     append([]MgmtRole(nil), rr.Roles...),
				Path:      path,
				Redundant: countLinkUp(snap, nics) >= 2,
			})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Ref.String() < list[j].Ref.String() })
		out[node] = list
	}
	return out
}

// ResolvePhysicalPath resolves carrier's physical path: a VLAN sub-interface
// walks to its parent link (then continues from there); a bridge walks to
// each of its resolved Ports (bond or bare PhysNic); a bond walks to each of
// its declared/runtime slaves. path is every entity ref traversed (the
// "mgmt-path" badge targets, ResolveMgmtPaths' own Path field); nics is the
// subset that are terminal PhysNics (redundancy is counted over exactly
// these). Exported (T-1503, T-1206) so other packages needing "which
// bond/PhysNic does this carrier's traffic ultimately ride" — internal/ceph's
// OSD↔bond attribution, and internal/pbs's backup-path network sizing hint
// (which resolves a node's egress interface toward its PBS server and reports
// that path's bottleneck link speed) — share this one resolver rather than
// re-implementing the bond/VLAN-transitive walk a second time; ResolveMgmtPaths
// below is its original caller.
func ResolvePhysicalPath(snap inventory.Snapshot, carrier inventory.Ref) (path []inventory.Ref, nics []inventory.Ref) {
	visited := map[inventory.Ref]bool{carrier: true}

	var walk func(ref inventory.Ref)
	walk = func(ref inventory.Ref) {
		e, ok := snap.Get(ref)
		if !ok {
			return
		}
		switch v := e.(type) {
		case *inventory.VlanIface:
			if v.Parent.IsZero() || visited[v.Parent] {
				return
			}
			visited[v.Parent] = true
			path = append(path, v.Parent)
			walk(v.Parent)
		case *inventory.Bridge:
			for _, p := range v.Ports {
				if visited[p] {
					continue
				}
				visited[p] = true
				path = append(path, p)
				walk(p)
			}
		case *inventory.Bond:
			slaves := v.Slaves
			if len(slaves) == 0 {
				slaves = v.DeclaredSlaves
			}
			for _, name := range slaves {
				sref := inventory.Ref{Kind: inventory.KindPhysNic, Node: ref.Node, ID: name}
				if visited[sref] {
					continue
				}
				visited[sref] = true
				path = append(path, sref)
				nics = append(nics, sref)
			}
		case *inventory.PhysNic:
			nics = append(nics, ref)
		}
	}
	walk(carrier)
	return path, nics
}

// countLinkUp counts how many of nics are confirmed link-up (PhysNic.LinkUp
// with LinkUpSet true — an unreported link state never counts as "up",
// mirroring project.go's statusOf tri-state treatment of the same field).
func countLinkUp(snap inventory.Snapshot, nics []inventory.Ref) int {
	n := 0
	for _, ref := range nics {
		e, ok := snap.Get(ref)
		if !ok {
			continue
		}
		p, ok := e.(*inventory.PhysNic)
		if ok && p.LinkUpSet && p.LinkUp {
			n++
		}
	}
	return n
}

// ApplyMgmtBadges decorates t in place with docs/features/topology.md §3's
// mgmt/corosync/mgmt-path badge vocabulary from paths (ResolveMgmtPaths'
// output): every resolved ref's own node gets its Roles painted as badges
// (MgmtRoleMgmt/MgmtRoleCorosync — "carrier nodes"), and every entity in its
// Path gets MgmtPathBadge ("path members") — additive to whatever badges
// project.go's badgesOf already assigned. A ref/path-member id absent from
// t.Nodes (filtered out by the request's layer/node/vlan query, or the
// snapshot the caller resolved paths against having since moved on) is
// silently skipped, the same tolerant behavior paintBadge (api/topology.go)
// already has for drift/finding badges.
func ApplyMgmtBadges(t *Topology, paths map[string][]MgmtPath) {
	if len(paths) == 0 {
		return
	}
	byID := make(map[string]int, len(t.Nodes))
	for i, n := range t.Nodes {
		byID[n.ID] = i
	}
	addBadge := func(id, badge string) {
		i, ok := byID[id]
		if !ok {
			return
		}
		if !hasBadge(t.Nodes[i].Badges, badge) {
			t.Nodes[i].Badges = append(t.Nodes[i].Badges, badge)
		}
	}
	for _, list := range paths {
		for _, p := range list {
			carrierID := p.Ref.String()
			for _, role := range p.Roles {
				addBadge(carrierID, string(role))
			}
			for _, ref := range p.Path {
				addBadge(ref.String(), MgmtPathBadge)
			}
		}
	}
}
