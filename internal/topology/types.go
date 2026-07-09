package topology

// Layer names one of docs/features/topology.md §1's four toggleable layers.
// The string values are docs/api.md's own `?layers=phys,l2,sdn,guest` query
// tokens — the same vocabulary is reused for the `layer` field on every
// projected Node, so the frontend has exactly one set of layer identifiers
// to reason about (the query filter and the rendered field never disagree).
type Layer string

const (
	LayerPhysical Layer = "phys"
	LayerL2       Layer = "l2"
	LayerSDN      Layer = "sdn"
	LayerGuest    Layer = "guest"
)

// AllLayers is the canonical, always-four-long list returned in every
// Topology.Layers response field (docs/features/topology.md §1: "Four
// toggleable layers"), regardless of ?layers= filtering or of which layers
// actually have any nodes this poll. The frontend's layer-toggle rail wants
// a stable list to render toggles for even when a layer is momentarily
// empty (e.g. no LLDP neighbors yet) — docs/api.md's `{nodes, edges, layers,
// generatedAt}` shape doesn't spell out which of these two readings
// "layers" means, so this is the documented interpretation; flagged in the
// T-106 completion report for T-107 to confirm against.
var AllLayers = []Layer{LayerPhysical, LayerL2, LayerSDN, LayerGuest}

// Status is the rendering status painting of a node or edge
// (docs/features/topology.md §2 "Status painting").
type Status string

const (
	StatusOK       Status = "ok"
	StatusDown     Status = "down"     // link down = red
	StatusDegraded Status = "degraded" // degraded bond (missing slave) = amber
	StatusUnknown  Status = "unknown"  // no data / stale
)

// severity orders Status for edge-status derivation (worst wins).
func (s Status) severity() int {
	switch s {
	case StatusDown:
		return 3
	case StatusDegraded:
		return 2
	case StatusUnknown:
		return 1
	default:
		return 0
	}
}

// worstStatus returns whichever of a, b paints more severely.
func worstStatus(a, b Status) Status {
	if a.severity() >= b.severity() {
		return a
	}
	return b
}

// Node is one rendered topology element, per docs/features/topology.md §3's
// rendering contract: `nodes[]` with id/kind/label/layer/nodeGroup/status/
// badges[]. ID is normally a Ref.String() (so the frontend can round-trip it
// through inventory.ParseRef to hit GET /inventory/{ref}); synthetic
// guest-collapse group nodes use the "guest-group:<node>:<targetRef>"
// convention documented on CollapsedCount below instead and are not valid
// inventory Refs — the frontend must special-case ids with that prefix as
// "expand this pill" rather than "open the inspector".
type Node struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Label          string   `json:"label"`
	Layer          Layer    `json:"layer"`
	NodeGroup      string   `json:"nodeGroup"`
	Status         Status   `json:"status"`
	Badges         []string `json:"badges"`
	CollapsedCount int      `json:"collapsedCount,omitempty"`
}

// Edge is one rendered relationship, per the same rendering contract:
// `edges[]` with from/to/kind/badges[]/status.
type Edge struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Kind   string   `json:"kind"`
	Status Status   `json:"status"`
	Badges []string `json:"badges"`
}

// Topology is the full GET /api/v1/topology response body (docs/api.md:
// "full projected topology: {nodes, edges, layers, generatedAt}").
// GeneratedAt is unix seconds UTC per docs/api.md's conventions section.
type Topology struct {
	Nodes       []Node  `json:"nodes"`
	Edges       []Edge  `json:"edges"`
	Layers      []Layer `json:"layers"`
	GeneratedAt int64   `json:"generatedAt"`
}

// Filter is the server-side ?layers=&node=&vlan= filter for GET /topology.
// A zero Filter (no Layers, no Node, VLAN == 0) means "everything".
type Filter struct {
	Node   string
	Layers []Layer
	VLAN   int
}

// hasLayer reports whether f restricts to specific layers and, if so,
// whether l is one of them. An empty Layers means every layer passes.
func (f Filter) hasLayer(l Layer) bool {
	if len(f.Layers) == 0 {
		return true
	}
	for _, want := range f.Layers {
		if want == l {
			return true
		}
	}
	return false
}

// DefaultCollapseThreshold is the guest-per-target count above which the
// projection collapses individual guest NICs attached to the same
// bridge/VNet on the same node into a single summarized pill node
// (docs/features/topology.md §1: "Collapsible per bridge (\"23 guests\"
// pill expands on click)"). Chosen to sit comfortably below the §4 scale
// target (300 guests / 8 nodes / 4 bridges-per-node ⇒ ~75 guests per
// bridge at max scale) while still leaving small clusters (the golden test
// fixtures included) fully expanded.
const DefaultCollapseThreshold = 8

// EntityDetail is the GET /inventory/{ref} response: the resolved entity's
// canonical fields plus provenance (docs/api.md: "including raw source").
// See this package's doc.go for what "raw source" means here — the graph
// does not retain original interfaces(5)/PVE JSON text past ingestion (see
// T-106's completion report), so "raw source" is the best available
// substitute: which source contributed each resolved field, and what any
// disagreeing sources reported.
type EntityDetail struct {
	Ref         string                 `json:"ref"`
	Kind        string                 `json:"kind"`
	Node        string                 `json:"node"`
	Label       string                 `json:"label"`
	Fields      map[string]any         `json:"fields"`
	Provenance  map[string]FieldSource `json:"provenance"`
	Related     []RelatedRef           `json:"related"`
	GeneratedAt int64                  `json:"generatedAt"`
}

// FieldSource is one resolved field's provenance: which source won, and any
// dissenting (source, value) pairs (mirrors inventory.FieldProv).
type FieldSource struct {
	Owner     string        `json:"owner"`
	Conflicts []SourceValue `json:"conflicts,omitempty"`
}

// SourceValue mirrors inventory.SourceValue for JSON exposure.
type SourceValue struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

// RelatedRef is one edge incident to the detailed entity.
type RelatedRef struct {
	Ref       string `json:"ref"`
	EdgeKind  string `json:"edgeKind"`
	Direction string `json:"direction"` // "from" | "to"
}

// SearchResult is one GET /inventory/search hit.
type SearchResult struct {
	Ref          string `json:"ref"`
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	Node         string `json:"node"`
	MatchedField string `json:"matchedField"`
	Score        int    `json:"score"`
}
