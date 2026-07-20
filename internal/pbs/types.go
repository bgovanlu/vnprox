package pbs

import "github.com/bgovanlu/vnprox/internal/inventory"

// HostRef builds the stable Ref identifying a PBS host by its server
// address — the one place this ID scheme is spelled out, so Discover, the
// topology decoration, and any future caller never disagree on the
// encoding. A PBS host is cluster-scoped (its storage.cfg entry is shared
// cluster config), so Node is empty (docs/data-model.md §1).
func HostRef(address string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindPBSHost, ID: address}
}

// Host is one Proxmox Backup Server vnprox discovered from PVE's own
// storage.cfg (grouped by server address, so two storage.cfg entries
// pointing at the same PBS server with different datastores collapse into
// one Host carrying both). Ref is its first-class identity (Kind
// KindPBSHost, cluster-scoped, ID = Address). Every field is sourced from
// PVE's own storage config — never a PBS API read.
type Host struct {
	Ref         inventory.Ref
	Address     string
	Fingerprint string
	Datastores  []string
	StorageIDs  []string
	Port        int
}

// Storage is this package's own copy of the PBS-type storage.cfg fields
// Project needs (kept separate from pve.Storage so internal/pve's import
// doesn't leak into this package's callers — the same package-boundary
// convention internal/topology follows for its upstream wire types). Nodes
// is the storage's node restriction (empty = available on all nodes).
type Storage struct {
	ID          string
	Address     string
	Datastore   string
	Fingerprint string
	Nodes       []string
	Port        int
}

// Job is this package's own copy of one enabled vzdump backup job's fields
// (Discover drops disabled jobs). Node is the job's node restriction (empty
// = every node's guests); All true means "back up every guest" (VMIDs then
// empty).
type Job struct {
	ID       string
	Storage  string
	Node     string
	Schedule string
	VMIDs    []string
	All      bool
}

// Status is Discover's output: the discovered PBS hosts, the PBS-type
// storage entries backing them (for node-restriction resolution), the
// enabled backup jobs, and the cluster node list (used to expand a job with
// no node restriction to "every node"). Empty Hosts models a cluster with no
// PBS storage configured at all — the common, unremarkable case, never an
// error.
type Status struct {
	Hosts    []Host
	Storages []Storage
	Jobs     []Job
	Nodes    []string
}

// JobSummary is one backup job's map/inspector-facing summary on a resolved
// BackupPath (denormalized so a consumer need not re-join jobs to paths).
type JobSummary struct {
	ID       string
	Storage  string
	Schedule string
	Guests   int  // count of explicitly selected guests (0 when All)
	All      bool // "every guest" job
}

// BackupPath is one backing-up node's resolved network path to a PBS host:
// which egress interface on that node carries the backup traffic toward the
// PBS server (Carrier), the physical path behind it (Path/NICs, from
// internal/topology.ResolvePhysicalPath), the single bond/NIC it rides
// (RidingOn), the path's bottleneck link speed, and a deterministic
// plain-English sizing hint. Carrier/RidingOn are the zero Ref when the
// egress could not be resolved from inventory (a PBS server reachable only
// via a route inventory doesn't model) — "unresolved", never a guessed
// path.
type BackupPath struct {
	Host       inventory.Ref
	Carrier    inventory.Ref
	RidingOn   inventory.Ref
	Node       string
	SizingHint string
	Path       []inventory.Ref
	NICs       []inventory.Ref
	StorageIDs []string
	Jobs       []JobSummary
	LinkMbps   int
	LinkKnown  bool
}

// Overlay is Project's full output: the discovered PBS hosts plus every
// resolved node->host backup path. The topology decoration
// (internal/api.paintPBS) renders Hosts as pbs-host nodes and Paths as
// backup-path edges; GET /pbs serves the whole Overlay for the inspector's
// datastore-network sizing hint.
type Overlay struct {
	Hosts []Host
	Paths []BackupPath
}
