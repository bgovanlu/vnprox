package sim

import "github.com/bgovanlu/vnprox/internal/inventory"

// Verdict is the top-level answer of a simulation.
//
// docs/api.md's one-line shape pins allow|deny|unreachable. This package
// adds a fourth value, indeterminate, because the honesty contract (AC5)
// forbids returning a confident verdict when the engine could not fully
// evaluate the path — e.g. a rule references an alias the snapshot does not
// define, or a firewall decision would depend on a guest IP the inventory
// does not carry. Rendering allow/deny/unreachable in those cases would be
// a lie. docs/api.md is updated in this same change (with a flagged note)
// to document the added state; see this task's report.
type Verdict string

const (
	// VerdictAllow: a path exists and no firewall enforcement point along
	// it drops or rejects the flow.
	VerdictAllow Verdict = "allow"
	// VerdictDeny: a path exists but a firewall rule (BlockingRule) drops or
	// rejects the flow.
	VerdictDeny Verdict = "deny"
	// VerdictUnreachable: no L2/L3 path exists between the endpoints (Missing
	// describes the break). A confident negative — only used when the break
	// is actually established from configured state.
	VerdictUnreachable Verdict = "unreachable"
	// VerdictIndeterminate: the engine could not fully evaluate the path.
	// At least one blocker-severity caveat explains why. Never returned with
	// a claim of allow/deny/unreachable.
	VerdictIndeterminate Verdict = "indeterminate"
)

// EndpointKind names the three endpoint forms docs/features/firewall.md §5
// supports: a guest NIC, an arbitrary IP literal, or external/WAN.
type EndpointKind string

const (
	EndpointGuestNic EndpointKind = "guest-nic"
	EndpointIP       EndpointKind = "ip"
	EndpointExternal EndpointKind = "external"
)

// Endpoint is one end of a simulated flow.
type Endpoint struct {
	Kind EndpointKind
	// NicRef is the guest NIC's inventory Ref (Kind == KindGuestNic), for
	// EndpointGuestNic.
	NicRef inventory.Ref
	// IP is a literal address, for EndpointIP.
	IP string
}

// Request is one simulation question: can Src reach Dst on Proto/Port.
type Request struct {
	Src   Endpoint
	Dst   Endpoint
	Proto string // "tcp" | "udp" | "icmp" | "" (any)
	Port  int    // destination port; 0 = unspecified/any
}

// GuestIP is a resolved address for a guest NIC, with the source it came
// from (which drives the confidence caveat). The inventory graph does not
// carry guest IPs (IPAM allocations / guest-agent reports are not ingested
// as inventory entities), so callers supply these out-of-band via
// Input.GuestIPs; nil is fully supported (guest IPs then resolve unknown).
type GuestIP struct {
	IP     string
	Source IPSource
}

// IPSource labels where a resolved IP came from, ordered by confidence.
type IPSource string

const (
	// IPSourceLiteral: the user typed the IP (an EndpointIP). Authoritative.
	IPSourceLiteral IPSource = "literal"
	// IPSourceStatic: a statically configured guest IP.
	IPSourceStatic IPSource = "static"
	// IPSourceIPAM: a PVE IPAM allocation. Configured state, high confidence.
	IPSourceIPAM IPSource = "ipam"
	// IPSourceAgent: a guest-agent-reported runtime IP. Lower confidence —
	// runtime, not configuration; drives the guest-agent-ip caveat.
	IPSourceAgent IPSource = "guest-agent"
	// IPSourceUnknown: no IP could be resolved.
	IPSourceUnknown IPSource = ""
)

// Input bundles the pure data one or more simulations run over.
type Input struct {
	// Inventory is the network graph snapshot (L2/L3/SDN entities + edges +
	// firewall rulesets). Required.
	Inventory inventory.Snapshot
	// GuestIPs is an optional side-table of resolved guest NIC IPs keyed by
	// the guest NIC's inventory Ref. Optional (nil == none known).
	GuestIPs map[inventory.Ref][]GuestIP
	// ShapedRefs is T-1505's shape-awareness input: the set of inventory
	// Refs (today, always a bridge) currently carrying an applied qos.shape
	// — sourced from the app-owned qos_shapes store table (internal/qos),
	// never guessed or re-derived from live tc state. Optional (nil == no
	// QoS gateway wired, or no shapes currently applied): a hop crossing a
	// ref in this set gets CodeQosShaped disclosed rather than the shape
	// being silently ignored (docs/features/change-management.md's honesty-
	// contract precedent T-503 already set for firewall/conntrack).
	ShapedRefs map[inventory.Ref]bool
}

// ResolvedEndpoint echoes how the engine understood one endpoint, for the
// UI to render and for tests to assert against.
type ResolvedEndpoint struct {
	Kind        EndpointKind `json:"kind"`
	Ref         string       `json:"ref,omitempty"`
	Guest       string       `json:"guest,omitempty"`
	Node        string       `json:"node,omitempty"`
	IP          string       `json:"ip,omitempty"`
	IPSource    IPSource     `json:"ipSource,omitempty"`
	Attachment  string       `json:"attachment,omitempty"`
	Zone        string       `json:"zone,omitempty"`
	Vnet        string       `json:"vnet,omitempty"`
	Subnet      string       `json:"subnet,omitempty"`
	Description string       `json:"description,omitempty"`
	Vid         int          `json:"vid,omitempty"`
}

// Hop is one step along the traced path, rendered on the topology map by
// T-504. Ref is the inventory Ref string of the entity at this hop, or a
// synthetic id for non-inventory hops ("external", a fabric segment).
type Hop struct {
	Ref    string `json:"ref,omitempty"`
	Kind   string `json:"kind"`
	Node   string `json:"node,omitempty"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// RuleRef points at the exact firewall rule that produced a deny verdict,
// with enough context for T-504's "one click to the rule editor" deep link.
type RuleRef struct {
	EnforcementPoint string           `json:"enforcementPoint"`
	RulesetRef       string           `json:"rulesetRef"`
	Origin           string           `json:"origin"`
	GroupName        string           `json:"groupName,omitempty"`
	Direction        string           `json:"direction"`
	Action           string           `json:"action"`
	Rule             inventory.FwRule `json:"rule"`
	Pos              int              `json:"pos"`
}

// Missing describes why an unreachable verdict has no path. Message is the
// exact operator-facing string per docs/features/firewall.md §5 (e.g.
// "VLAN 30 is not trunked on bond0 of node pve2").
type Missing struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	AtRef   string `json:"atRef,omitempty"`
	AtNode  string `json:"atNode,omitempty"`
}

// CaveatSeverity ranks how load-bearing a caveat is for reading the verdict.
type CaveatSeverity string

const (
	// CaveatInfo: a standing honesty note (e.g. "results are simulated, not
	// live packets"); does not weaken the verdict.
	CaveatInfo CaveatSeverity = "info"
	// CaveatWarning: the verdict may not hold at runtime (SNAT asymmetry,
	// guest-agent IP confidence, an LLDP trunk cross-check mismatch).
	CaveatWarning CaveatSeverity = "warning"
	// CaveatBlocker: the engine could not fully evaluate; the verdict is
	// Indeterminate. The UI must surface these prominently.
	CaveatBlocker CaveatSeverity = "blocker"
)

// Caveat is one honesty-contract disclosure attached to a Result.
type Caveat struct {
	Code     string         `json:"code"`
	Severity CaveatSeverity `json:"severity"`
	Message  string         `json:"message"`
	// Feature names the un-evaluated PVE/SDN feature, for Code == not-evaluated.
	Feature string `json:"feature,omitempty"`
}

// Result is a full simulation answer. It always carries Caveats (AC3),
// even for a confident allow.
type Result struct {
	BlockingRule *RuleRef         `json:"blockingRule,omitempty"`
	Missing      *Missing         `json:"missing,omitempty"`
	Src          ResolvedEndpoint `json:"src"`
	Dst          ResolvedEndpoint `json:"dst"`
	Verdict      Verdict          `json:"verdict"`
	Proto        string           `json:"proto,omitempty"`
	Hops         []Hop            `json:"hops"`
	Caveats      []Caveat         `json:"caveats"`
	Port         int              `json:"port,omitempty"`
}

// addCaveat appends c to the result if an identical (code+message) caveat
// is not already present, keeping the honesty list free of duplicates when
// several evaluation steps note the same limitation.
func (r *Result) addCaveat(c Caveat) {
	for _, e := range r.Caveats {
		if e.Code == c.Code && e.Message == c.Message {
			return
		}
	}
	r.Caveats = append(r.Caveats, c)
}
