package ceph

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// OSD is one Ceph OSD PVE reports, converted from pve.CephOSD (this
// package's own copy, rather than a direct alias, so internal/api and the
// frontend-facing wire types this package's callers build don't leak
// internal/pve's import into their own dependency graph — the same
// "own copy at the package boundary" convention every other read-only
// package in this codebase (internal/k8s, internal/topology) follows for
// its upstream API client's wire types). Ref is this OSD's first-class
// identity (Kind inventory.KindCephOSD, "osd<id>" scoped to Node) —
// mirroring inventory.VirtualFunction's identical "has a Ref but is never
// itself graph/provenance-tracked" pattern (docs/data-model.md §1), set by
// OSDRef below rather than left for callers to construct by hand.
type OSD struct {
	Ref    inventory.Ref
	Device string
	Node   string
	ID     int
	Up     bool
	In     bool
}

// OSDRef builds the stable Ref identifying OSD id on node — the one place
// this "osd<id>" ID scheme is spelled out, so Discover and any future
// caller never disagree on the encoding.
func OSDRef(node string, id int) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindCephOSD, Node: node, ID: fmt.Sprintf("osd%d", id)}
}

// Status is Discover's output: PVE's own Ceph public/cluster network
// declaration plus every discovered OSD's node placement. Either network
// CIDR may be empty (Ceph not installed, or only one network declared —
// see pve.CephConfig's doc comment: never a guessed fallback).
type Status struct {
	PublicNetwork  string
	ClusterNetwork string
	OSDs           []OSD
}

// NodeAttribution is one OSD-hosting node's resolved physical path for
// Ceph's public and cluster networks (Project's per-node output).
// *Carrier is the zero Ref when that network's CIDR is undeclared or no
// interface on this node carries an address inside it — "unresolved",
// never a guessed path (the same honesty contract this codebase's other
// live-resolved correlations, e.g. ResolveVFAssignments, already follow).
type NodeAttribution struct {
	ClusterCarrier  inventory.Ref
	PublicCarrier   inventory.Ref
	ClusterRidingOn inventory.Ref
	PublicRidingOn  inventory.Ref
	Node            string
	ClusterNICs     []inventory.Ref
	ClusterPath     []inventory.Ref
	PublicNICs      []inventory.Ref
	PublicPath      []inventory.Ref
	PublicMTU       int
	ClusterMTU      int
	PublicMTUKnown  bool
	ClusterMTUKnown bool
}

// OSDAttribution is one OSD plus the bond/NIC ref its node's public/cluster
// traffic rides (NodeAttribution.Public/ClusterRidingOn for its Node) — the
// "which OSDs ride which bonds" projection T-1503's card names, denormalized
// per OSD so a map layer/inspector can go straight from an OSD to its bond
// without a second node lookup.
type OSDAttribution struct {
	PublicBond  inventory.Ref
	ClusterBond inventory.Ref
	OSD         OSD
}

// Overlay is Project's full output: the map-layer projection T-1503's AC1
// golden test asserts against, and the substrate CorosyncSharedLink/
// ClusterMTUMismatch/SingleNIC evaluate.
type Overlay struct {
	PublicNetwork  string
	ClusterNetwork string
	Nodes          []NodeAttribution
	OSDs           []OSDAttribution
}
