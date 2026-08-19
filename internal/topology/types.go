package topology

import "time"

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
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	Layer     Layer    `json:"layer"`
	NodeGroup string   `json:"nodeGroup"`
	Status    Status   `json:"status"`
	DnsName   string   `json:"dnsName,omitempty"`
	Badges    []string `json:"badges"`
	// Members lists the Ref strings of every entity this synthetic
	// collapse node absorbed (T-1907's physical-layer "phys-group:<node>"
	// per-node summary — see collapsePhysical). Unlike a "guest-group:
	// <node>:<targetRef>" pill (whose single shared attachment target
	// already gives the frontend a place to ask "who's collapsed here" via
	// that target's own GET /inventory/{ref} `related` list — see
	// web/src/topology/expand.ts's doc comment), a per-node physical-layer
	// summary has no single such target: a node's NICs fan out to several
	// different bonds/bridges, or none. So the member refs are carried
	// directly on the node instead, and the frontend's expand path
	// (expandPhysicalGroup) fetches each one's own GET /inventory/{ref}
	// detail to reconstruct it — still zero backend round trips beyond
	// what Detail() already answers, same as the guest-group path. Omitted
	// (nil) on every other node kind, including guest-group pills, which
	// keep their existing target-lookup expansion unchanged (AC3).
	Members []string `json:"members,omitempty"`
	// Findings is T-3501's source-and-severity-bearing form, additive to
	// Badges: one entry per open finding (any unified-stream producer —
	// drift, lldp, ipam, health) naming this node's ID in its Refs. Badges
	// keeps carrying the compact "finding:<source>:<severity>" token (one
	// per distinct source, worst severity) plus the legacy bare "drift"
	// token for wire back-compat (docs/api.md) — this field is where the
	// finding's own Check/Detail text lives, so the frontend can surface
	// "why" on hover/selection without a second round trip. Never set by
	// Project itself (nil on the pure projection); populated by the same
	// handler-level decoration Badges already gets
	// (paintFindings/paintDrift in internal/api/topology.go).
	//
	// Ahead of CollapsedCount, not after it: fieldalignment packs the
	// pointer-bearing fields before the int, the same reason Members sits
	// where it does.
	Findings       []FindingBadge `json:"findings,omitempty"`
	CollapsedCount int            `json:"collapsedCount,omitempty"`
}

// Edge is one rendered relationship, per the same rendering contract:
// `edges[]` with from/to/kind/badges[]/status.
type Edge struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Kind   string   `json:"kind"`
	Status Status   `json:"status"`
	Badges []string `json:"badges"`
	// Findings mirrors Node.Findings (see its doc comment) — present for
	// symmetry with EntityEdge.tsx's existing badge-vocabulary check, even
	// though no producer currently names an edge id in a finding's Refs
	// (findings name entities, and an edge has no Ref of its own).
	Findings []FindingBadge `json:"findings,omitempty"`
}

// FindingBadge is one open finding naming an entity, carried on Node.Findings
// / Edge.Findings (T-3501). Source is one of internal/findings.Source's
// values ("drift" for the fallback-only paintDrift path, which predates the
// unified stream and has no other source to report). Severity is
// "error"|"warning"|"info" (internal/findings.Severity's vocabulary — the
// same one internal/drift.Finding.Severity already used). Check and Detail
// are the finding's own machine check-id and human-readable explanation
// (GET /findings' own shape) — this is the "why" the frontend renders on
// hover/selection instead of sending the operator to a separate panel.
type FindingBadge struct {
	Source   string `json:"source"`
	Severity string `json:"severity"`
	Check    string `json:"check"`
	Detail   string `json:"detail"`
}

// UnrefFinding is a FindingBadge for a finding whose Refs are empty — T-3501
// AC5's "findings with no entity refs must not paint nothing anywhere"
// requirement (health/service_down for a bare service name like dnsmasq/frr
// has nothing to name: a service isn't an inventory entity). Nodes carries
// the finding's own Nodes list (which cluster node(s) it concerns) since
// there is no entity ref to attach it to.
type UnrefFinding struct {
	FindingBadge
	Nodes []string `json:"nodes"`
}

// Topology is the full GET /api/v1/topology response body (docs/api.md:
// "full projected topology: {nodes, edges, layers, generatedAt}").
// GeneratedAt is unix seconds UTC per docs/api.md's conventions section.
//
// Staleness is not produced by Project (which is a pure function of an
// inventory snapshot) — internal/api's /topology handler decorates the
// projection with it from live collector status, so the frontend can render
// docs/features/topology.md §5's greyed band + staleness banner. It is
// omitted when no collector status is available (tests, collector failed to
// initialize).
type Topology struct {
	Staleness *Staleness `json:"staleness,omitempty"`
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
	Layers    []Layer    `json:"layers"`
	// UnrefFindings (T-3501) carries every open finding whose Refs are
	// empty — see UnrefFinding's doc comment. Never produced by Project;
	// populated by the same handler-level decoration Nodes[].Findings gets.
	// Omitted (nil) when there are none, matching Staleness's convention.
	UnrefFindings []UnrefFinding `json:"unrefFindings,omitempty"`
	GeneratedAt   int64          `json:"generatedAt"`
}

// Staleness summarizes how fresh the inventory data behind a Topology
// response is, per collector source (docs/features/topology.md §5: "Peer
// node unreachable → its band renders greyed from last-known data with a
// staleness banner and timestamp"). Stale is true iff any source is stale,
// so the frontend has a single flag to key the banner on.
type Staleness struct {
	Sources []SourceStaleness `json:"sources"`
	Stale   bool              `json:"stale"`
}

// SourceStaleness is one collector poll loop's freshness. Name is the loop
// name ("pve", "host", "lldp"). Node scopes the staleness: the host/lldp
// loops only ever poll the daemon's local node, so their staleness greys
// that node's band only; the pve loop covers the whole cluster (empty Node)
// — when it is stale, every node's data is last-known. LastSuccess is unix
// seconds UTC of the last successful poll (0 / omitted if none yet) — the
// "timestamp" §5's banner shows.
type SourceStaleness struct {
	Name        string `json:"name"`
	Node        string `json:"node,omitempty"`
	LastError   string `json:"lastError,omitempty"`
	LastSuccess int64  `json:"lastSuccess,omitempty"`
	Stale       bool   `json:"stale"`
}

// Filter is the server-side ?layers=&node=&vlan= filter for GET /topology.
// A zero Filter (no Layers, no Node, VLAN == 0, zero Now) means "everything,
// evaluated at the real current time".
//
// Now is the clock Project uses for the LLDP staleness lifecycle
// (docs/features/lldp-discovery.md §3 — grey at 2×TTL, drop from the map at
// 10 minutes; see switches.go). The zero value means "time.Now()"; callers
// (internal/api's handler) never set it, only tests do, so this field is a
// pure test seam with no behavioral change for production traffic.
type Filter struct {
	Now    time.Time
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

// DefaultPhysicalCollapseThreshold is the physical-NIC-per-node count above
// which the projection collapses that node's individual PhysNic entities
// into one "N NICs" per-node summary pill (docs/features/topology.md §4: "physical
// layer collapses to per-node summary" — T-1907, closing the gap T-607's
// docs audit flagged and docs/performance.md §4 tracked). Mirrors
// DefaultCollapseThreshold's own reasoning, applied to the physical layer
// instead of the guest layer:
//
//   - At the documented §4 scale target (8 nodes x 6 NICs/node), every
//     node's physical layer sits at 6 — comfortably under this threshold —
//     so collapsing never engages at the scale the product is verified
//     against; T-607/docs/performance.md §4 already established the
//     physical layer is only ~50-65 elements cluster-wide there, nowhere
//     near the ~2,000-element render cap the guest layer alone exists to
//     protect.
//   - 8 is the same magnitude DefaultCollapseThreshold already established
//     as "small enough to read as individual chips, large enough that a
//     summary earns its keep" for the analogous guest-per-bridge case;
//     reusing it keeps one mental model ("more than 8 of the same kind of
//     thing in one place collapses") rather than inventing an unrelated
//     second number.
//
// **Provisional, revisited once (T-3203, 2026-08-18) with a real but small
// data point.** The two real nodes T-3201 stood up (`planning/reports/
// T-3203.md`) carry 4 and 6 physical NICs respectively — both comfortably
// under this threshold, consistent with the documented target's own
// reasoning above, so there is no basis to move 8 either direction from
// this run. This does NOT close the concern the threshold was always
// provisional against: a large chassis with many bonds or SR-IOV PFs (each
// carrying its own PhysNic entity) could still run materially higher, and a
// 2-node dev cluster with onboard NICs says nothing about that case. Still
// provisional for that scenario specifically; revisit again if real
// hardware with a denser NIC count ever becomes available to test against.
const DefaultPhysicalCollapseThreshold = 8

// EntityDetail is the GET /inventory/{ref} response: the resolved entity's
// canonical fields, per-field provenance, and the raw source text behind it
// (docs/api.md: "including raw source (interfaces stanza / PVE API
// object)").
//
// RawSource maps each contributing source name (inventory.Source strings:
// "host-interfaces", "pve-network", "pve-sdn", "pve-guest", "host-netlink",
// "host-lldp", ...) to the raw text that source's contribution was derived
// from — the verbatim interfaces(5) stanza for host-interfaces,
// pretty-printed JSON of the PVE API object for pve-* sources, compact JSON
// of the observed state for host-netlink/host-lldp (see
// inventory.Snapshot.RawSource). Values are always JSON strings, even when
// the content is itself JSON. Omitted when no source retained raw text.
// Provenance stays alongside it: RawSource shows what each source said
// verbatim, Provenance shows which source won each resolved field.
type EntityDetail struct {
	Ref         string                 `json:"ref"`
	Kind        string                 `json:"kind"`
	Node        string                 `json:"node"`
	Label       string                 `json:"label"`
	Fields      map[string]any         `json:"fields"`
	Provenance  map[string]FieldSource `json:"provenance"`
	RawSource   map[string]string      `json:"rawSource,omitempty"`
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

// FDBRow is one bridge forwarding-database entry, cluster-wide and
// enriched with an ownership label (T-306's MAC/FDB browser,
// docs/features/lldp-discovery.md §4: "search any MAC → which
// bridge/port/guest it lives behind, cluster-wide"). Owner is one of
// "guest" (Mac matches a GuestNic — OwnerRef/OwnerLabel identify the
// owning guest), "vnprox-known" (Mac matches a PhysNic elsewhere in
// inventory — a known infra device, not a guest), or "unknown" (no match —
// most often exactly what shows up on an uplink/trunk port). Score is only
// populated by FDBSearch (omitted, i.e. zero, on the plain FDB() listing).
type FDBRow struct {
	Node       string `json:"node"`
	Bridge     string `json:"bridge"`
	BridgeRef  string `json:"bridgeRef"`
	Mac        string `json:"mac"`
	Port       string `json:"port,omitempty"`
	Owner      string `json:"owner"`
	OwnerRef   string `json:"ownerRef,omitempty"`
	OwnerLabel string `json:"ownerLabel,omitempty"`
	Vlan       int    `json:"vlan,omitempty"`
	Score      int    `json:"score,omitempty"`
	Master     bool   `json:"master,omitempty"`
	Permanent  bool   `json:"permanent,omitempty"`
	Stale      bool   `json:"stale"`
}
