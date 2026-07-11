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

// BlockSummary is one /24-sized block's utilization rollup within a larger
// (paged) subnet (docs/features/ipam.md §2: "larger subnets render as paged
// block summaries").
type BlockSummary struct {
	CIDR        string  `json:"cidr"`
	Total       int     `json:"total"`
	Allocated   int     `json:"allocated"`
	Observed    int     `json:"observed"`
	Conflicts   int     `json:"conflicts"`
	Utilization float64 `json:"utilization"`
}

// Conflict is one health finding from conflict detection
// (docs/features/ipam.md §2: "Each conflict is a health finding with
// suggested resolution").
type Conflict struct {
	Type       string   `json:"type"`
	Severity   string   `json:"severity"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion"`
	IPs        []string `json:"ips"`
}

// AllocationGrid is `GET /ipam/subnets/{cidr}/allocations`'s response
// (docs/api.md: "allocation grid data"). For a subnet with <=256 addresses
// (/24 and smaller), Cells carries the whole subnet and Paged is false. For
// a larger subnet, the default (no `?block=`) response is Paged=true with
// Blocks (one /24-sized summary per block, docs/features/ipam.md §2's
// "paged block summaries") and no Cells; passing `?block=<cidr>` (one of
// Blocks' own CIDRs) returns that one block's full Cells instead — see
// this task's report for the paging/perf approach.
type AllocationGrid struct {
	CIDR        string         `json:"cidr"`
	Block       string         `json:"block,omitempty"`
	Blocks      []BlockSummary `json:"blocks,omitempty"`
	Cells       []Cell         `json:"cells,omitempty"`
	Conflicts   []Conflict     `json:"conflicts"`
	Prefix      int            `json:"prefix"`
	Total       int            `json:"total"`
	GeneratedAt int64          `json:"generatedAt"`
	Paged       bool           `json:"paged"`
	ReadOnly    bool           `json:"readOnly,omitempty"`
}
