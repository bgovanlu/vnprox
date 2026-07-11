package docexport

import (
	"github.com/bgovanlu/vnprox/internal/sdn"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// Data is the fully-assembled, render-format-agnostic content of one
// config documentation export. Markdown and HTML are both pure functions
// of a Data value (see markdown.go/html.go) — this type is the single
// source of truth for "every documented section" (docs/features/
// blueprints.md §4), so a golden test can assert section presence once
// against Data's shape and once against each renderer's output.
type Data struct {
	// GeneratedAt is unix seconds UTC (docs/api.md's timestamp
	// convention), the "Timestamped" requirement.
	GeneratedAt int64

	// Nodes is every cluster node name, sorted, driving the per-node
	// interface tables' section order.
	Nodes []string

	// Interfaces is the per-node interface table content, keyed by node
	// name (docs/features/blueprints.md §4: "per-node interface tables").
	Interfaces map[string][]InterfaceRow

	// VLANs is the VLAN matrix: one row per distinct VLAN ID discovered
	// from VLAN sub-interfaces and SDN VNet tags, each naming which nodes
	// (and which interface) carry it (docs/features/blueprints.md §4:
	// "VLAN matrix").
	VLANs []VlanRow

	// SDN is the SDN inventory tree (docs/features/blueprints.md §4: "SDN
	// inventory") — reused directly from internal/sdn.Tree rather than a
	// second parallel shape, so this package never re-derives SDN facts
	// internal/sdn already computed.
	SDN sdn.Tree
	// SDNErr is non-empty when the SDN tree could not be read (e.g. no
	// PVE client wired) — the export degrades gracefully (an explanatory
	// note in the rendered section) rather than failing the whole export,
	// matching every other read route's "optional dependency, not wired ->
	// degraded, not fatal" convention (see internal/api/router.go's
	// Options doc comments).
	SDNErr string

	// Firewall is one summary row per observed ruleset scope (docs/
	// features/blueprints.md §4: "firewall summaries").
	Firewall []FirewallRow

	// LLDP is the flat wiring table (docs/features/blueprints.md §4:
	// "LLDP wiring table"), reused directly from topology.PortRow (the
	// same data GET /ports already exposes) rather than re-deriving it.
	LLDP []topology.PortRow

	// Topology is the full projected topology (docs/features/blueprints.md
	// §4: "rendered topology (SVG)") — kept as the raw projection rather
	// than a pre-rendered SVG string so Markdown can report simple summary
	// counts while HTML additionally renders it via svg.go, both from the
	// exact same data.
	Topology topology.Topology
}

// InterfaceRow is one row of a per-node interface table: one physical NIC,
// bond, bridge, or VLAN sub-interface.
type InterfaceRow struct {
	// Kind is one of "physnic"|"bond"|"bridge"|"vlan", mirroring
	// docs/data-model.md's inventory entity kinds.
	Kind string
	Name string
	// Addresses is a comma-joined list of CIDRs (bridges/VLAN
	// sub-interfaces only; empty for physnics/bonds, which never carry an
	// address directly in this codebase's model).
	Addresses string
	// Detail is a short, human-readable, kind-specific extra: a bond's
	// mode + slave list, a bridge's VLAN-awareness + trunked VID ranges +
	// port list, a VLAN sub-interface's parent + VID.
	Detail string
	MTU    int
	Up     bool
}

// VlanRow is one VLAN matrix row: which nodes (and which named interface
// on each) carry VID.
type VlanRow struct {
	VID int
	// Nodes maps node name -> the interface name(s) on that node carrying
	// this VID (a VLAN sub-interface's name, or "sdn:<vnet>" for an SDN
	// VNet tag realized on that node's zone).
	Nodes map[string][]string
}

// FirewallRow is one ruleset scope's summary (docs/features/blueprints.md
// §4: "firewall summaries" — a summary, not the full rule table; the full
// table is already served by GET /firewall/rulesets for the UI's own
// firewall cockpit).
type FirewallRow struct {
	Scope      string
	Ref        string
	DefaultIn  string
	DefaultOut string
	// Banners are docs/features/firewall.md §2's "footgun" warnings
	// (fw.ScopeBanners) — e.g. "datacenter firewall is OFF" — included
	// verbatim since an as-built doc that omitted them would misrepresent
	// what's actually enforced.
	Banners   []string
	RuleCount int
	Enabled   bool
}
