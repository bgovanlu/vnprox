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
	Interfaces map[string][]InterfaceRow
	SDNErr     string
	Nodes      []string
	VLANs      []VlanRow
	Firewall   []FirewallRow
	LLDP       []topology.PortRow
	// Annotations/Regions are T-2806's map annotation layer, included
	// because a note is most useful to exactly the reader who cannot see
	// the map. Only LIVE (non-expired) entries reach here — the read-time
	// filter runs in internal/annotate before Gather ever sees them, so an
	// export produced by a daemon that was stopped for a month cannot
	// contain a note that expired during the outage.
	Annotations []AnnotationRow
	Regions     []RegionRow
	Topology    topology.Topology
	SDN         sdn.Tree
	GeneratedAt int64
}

// AnnotationRow is one entity-pinned note in the export. Text is operator
// free text and is escaped by every renderer that emits it (html.go,
// markdown.go, svg.go) — see docs/api.md's Map annotation layer section.
type AnnotationRow struct {
	Ref       string
	Content   string
	CreatedBy string
	// Expires is a rendered date, or "" for a note that never expires.
	Expires string
	// Created is a rendered date.
	Created string
	// Orphaned reports that the annotated entity no longer exists. The note
	// is still rendered — deliberately, and prominently: it is often the
	// only surviving record of why the entity was removed (T-2806 AC2).
	Orphaned bool
}

// RegionRow is one labelled canvas region in the export. X/Y/W/H are the
// canvas graph-space rectangle; the SVG render draws them behind the
// topology so the document shows the same grouping the map does.
type RegionRow struct {
	Label     string
	CreatedBy string
	Created   string
	Expires   string
	X         float64
	Y         float64
	W         float64
	H         float64
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
	Nodes map[string][]string
	VID   int
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
