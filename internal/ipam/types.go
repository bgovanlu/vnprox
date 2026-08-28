// SPDX-License-Identifier: Apache-2.0

package ipam

// Subnet is one row of docs/api.md's `GET /ipam/subnets`: an SDN subnet (or,
// read-only, a detected non-SDN subnet derived from a bridge address —
// docs/features/ipam.md §2) with utilization counts.
type Subnet struct {
	CIDR    string `json:"cidr"`
	Zone    string `json:"zone,omitempty"`
	Vnet    string `json:"vnet,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	// Node is only set for a bridge-derived (non-SDN) subnet: which
	// cluster node the bridge address was observed on (docs/data-model.md's
	// Bridge entity is per-node, unlike a cluster-scoped SdnSubnet).
	Node string `json:"node,omitempty"`
	// Source is "sdn" (a real SDN subnet, reserve/release-able) or "bridge"
	// (a detected non-SDN subnet, read-only per docs/features/ipam.md §2).
	Source      string  `json:"source"`
	ReadOnly    bool    `json:"readOnly,omitempty"`
	DHCPEnabled bool    `json:"dhcpEnabled,omitempty"`
	Total       int     `json:"total"`
	Allocated   int     `json:"allocated"`
	Observed    int     `json:"observed"`
	Conflicts   int     `json:"conflicts"`
	Utilization float64 `json:"utilization"`
}

// SubnetsResponse is `GET /ipam/subnets`'s full response.
type SubnetsResponse struct {
	Items       []Subnet `json:"items"`
	GeneratedAt int64    `json:"generatedAt"`
}

// CellState is one allocation-grid cell's render state
// (docs/features/ipam.md §2: "free / allocated / observed-unallocated /
// reserved / gateway / conflict").
type CellState string

const (
	CellFree      CellState = "free"
	CellAllocated CellState = "allocated"
	CellReserved  CellState = "reserved"
	CellObserved  CellState = "observed"
	CellGateway   CellState = "gateway"
	CellConflict  CellState = "conflict"
)

// Confidence is the multi-source-merge confidence label
// (docs/features/ipam.md §1: "merged with confidence labels (allocated,
// observed, both, conflict)"), independent of (but related to) CellState —
// e.g. an allocated-but-dark cell renders CellConflict (it needs attention)
// while its Confidence stays "allocated" (it genuinely is PVE-IPAM-sourced
// only; nothing currently observed contradicts or corroborates it).
type Confidence string

const (
	ConfidenceAllocated Confidence = "allocated"
	ConfidenceObserved  Confidence = "observed"
	ConfidenceBoth      Confidence = "both"
	ConfidenceConflict  Confidence = "conflict"
)

// Cell is one address's full detail: the allocation-grid's per-cell popover
// content (docs/features/ipam.md §2: "Click any cell -> detail (who, what,
// since when, source)"). Since-when is not carried: vnprox's app store never
// shadows PVE's own allocation history (docs/architecture.md's "Proxmox is
// the source of truth" rule), and neither PVE's IPAM API nor the
// enrichment sources this task wires (guest agent) report a first-seen
// timestamp — see this task's report for the follow-up note (deriving it
// from vnprox's own changeset audit trail for admin-made reservations is a
// reasonable future addition, out of scope here).
type Cell struct {
	IP         string     `json:"ip"`
	State      CellState  `json:"state"`
	Confidence Confidence `json:"confidence,omitempty"`
	Hostname   string     `json:"hostname,omitempty"`
	MAC        string     `json:"mac,omitempty"`
	GuestRef   string     `json:"guestRef,omitempty"`
	Sources    []string   `json:"sources,omitempty"`
	VMID       int        `json:"vmid,omitempty"`
}

// FreeRange is a contiguous run of unallocated host addresses, collapsed
// into a single row of the address list (docs/features/ipam.md §2). The list
// carries occupied addresses one-per-row and every gap between them as one
// FreeRange, so the response is proportional to actual usage — it renders
// identically for a /30 or a /16, with no per-address materialization of
// empty space. Start and End are inclusive; Count is the number of addresses
// in the run (clamped to a display-safe maximum for very large IPv6 gaps).
type FreeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Count int    `json:"count"`
}

// Counts is the address-list summary strip's per-state tally
// (docs/features/ipam.md §2). The buckets are mutually exclusive and sum to
// the subnet's usable-host count (Free included), so a segmented utilization
// bar drawn from them is exact.
type Counts struct {
	Allocated int `json:"allocated"`
	Reserved  int `json:"reserved"`
	Observed  int `json:"observed"`
	Gateway   int `json:"gateway"`
	Conflict  int `json:"conflict"`
	Free      int `json:"free"`
}

// Conflict is one health finding from conflict detection
// (docs/features/ipam.md §2: "Each conflict is a health finding with
// suggested resolution").
//
// Clusters is populated only for the T-1203 cross-cluster conflict type
// (cross_cluster_duplicate_subnet): the pair of attached clusters that both
// allocate the same or an overlapping CIDR. It is the "cluster-pair field
// added" to the reused Conflict shape (task card T-1203) — omitted entirely
// for every intra-cluster conflict type, which is single-cluster by nature.
type Conflict struct {
	Type       string   `json:"type"`
	Severity   string   `json:"severity"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion"`
	IPs        []string `json:"ips"`
	Clusters   []string `json:"clusters,omitempty"`
}

// Conflict type values. The first three are the intra-subnet vocabulary the
// merge engine emits (docs/features/ipam.md §2); ConflictCrossClusterDuplicateSubnet
// is T-1203's cross-cluster addition, produced by CrossClusterConflicts.
const (
	ConflictDuplicateIP                 = "duplicate_ip"
	ConflictObservedUnallocated         = "observed_unallocated"
	ConflictAllocatedDark               = "allocated_dark"
	ConflictCrossClusterDuplicateSubnet = "cross_cluster_duplicate_subnet"
)

// AllocationList is `GET /ipam/subnets/{cidr}/allocations`'s response: the
// NetBox-style address list (docs/features/ipam.md §2). Entries holds every
// occupied address (allocated, reserved, observed, gateway, or in conflict),
// sorted ascending; FreeRanges holds the contiguous unallocated gaps between
// them. The representation is sparse — proportional to actual usage, never
// to the address space — so a /16 and a /30 render through the same view
// with no paging. Counts is the summary tally, and Gateway names the
// subnet's gateway address (already present in Entries as a CellGateway row)
// so the client can pin/label it without re-deriving it.
type AllocationList struct {
	CIDR        string      `json:"cidr"`
	Gateway     string      `json:"gateway,omitempty"`
	Entries     []Cell      `json:"entries"`
	FreeRanges  []FreeRange `json:"freeRanges"`
	Conflicts   []Conflict  `json:"conflicts"`
	Counts      Counts      `json:"counts"`
	Prefix      int         `json:"prefix"`
	Total       int         `json:"total"`
	GeneratedAt int64       `json:"generatedAt"`
	ReadOnly    bool        `json:"readOnly,omitempty"`
}
