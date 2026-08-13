package docexport

import (
	"context"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/annotate"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/sdn"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// InventorySource is the subset of *inventory.Graph this package needs — the
// same one-method seam internal/api's FirewallGraph interface already uses
// for exactly this purpose.
type InventorySource interface {
	Snapshot() inventory.Snapshot
}

// SDNSource is the subset of *sdn.Service this package needs — the same
// seam internal/api's SDNService interface uses for GET /sdn. Nil-safe: a
// daemon with no PVE client wired (cmd/vnproxd/collect.go's degraded mode)
// simply gets an SDN section reporting itself unavailable rather than a
// failed export.
type SDNSource interface {
	Tree(ctx context.Context) (sdn.Tree, error)
}

// PortsSource is the subset of *topology.Service this package needs for the
// LLDP wiring table — the same data GET /ports already exposes.
type PortsSource interface {
	Ports() []topology.PortRow
}

// TopologySource is the subset of *topology.Service this package needs for
// the rendered topology section/SVG — the same data GET /topology exposes.
type TopologySource interface {
	Topology(f topology.Filter) topology.Topology
}

// AnnotationSource is the subset of *annotate.Service this package needs
// for T-2806's annotation section: the same read model GET /annotations
// and GET /map-regions serve, asked for the live-only view. Passing false
// here is what keeps an expired note out of the document — the export has
// no expiry logic of its own, and must not grow one. Nil-safe: a daemon
// with no annotation layer wired simply exports no annotation section.
type AnnotationSource interface {
	Notes(ctx context.Context, includeExpired bool) ([]annotate.Note, error)
	Regions(ctx context.Context, includeExpired bool) ([]annotate.Region, error)
}

// Service assembles config documentation exports from live daemon state.
// Every field is the same seam type internal/api already declares for the
// equivalent read route, so cmd/vnproxd's wiring passes the identical
// concrete values it already constructs for the router — this package
// never opens a second path to any of these sources.
type Service struct {
	Inventory   InventorySource
	SDN         SDNSource // nil-safe: SDN section reports itself unavailable
	Ports       PortsSource
	Topo        TopologySource
	Annotations AnnotationSource // nil-safe: no annotation section
	Now         func() time.Time
	// Logger records an annotation read that failed. A failed annotation
	// read degrades that one section rather than failing the export, the
	// same treatment SDNErr already gives an unavailable SDN tree.
	Logger *slog.Logger
}

// Build gathers current state and renders both export formats.
func (s *Service) Build(ctx context.Context) Data {
	now := s.Now
	if now == nil {
		now = time.Now
	}

	snap := s.Inventory.Snapshot()
	fwSnap := fw.BuildSnapshot(snap.All())

	var ports []topology.PortRow
	if s.Ports != nil {
		ports = s.Ports.Ports()
	}

	var topo topology.Topology
	if s.Topo != nil {
		topo = s.Topo.Topology(topology.Filter{})
	}

	var tree sdn.Tree
	var treeErr error
	if s.SDN != nil {
		tree, treeErr = s.SDN.Tree(ctx)
	}

	d := Gather(snap, tree, treeErr, ports, fwSnap, topo, now())
	d.Annotations, d.Regions = s.gatherAnnotations(ctx)
	return d
}

// gatherAnnotations reads the live-only annotation layer. Both reads ask
// for includeExpired=false: the export is a display surface like any
// other, so it shows exactly what the map shows.
func (s *Service) gatherAnnotations(ctx context.Context) ([]AnnotationRow, []RegionRow) {
	if s.Annotations == nil {
		return nil, nil
	}
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}

	notes, err := s.Annotations.Notes(ctx, false)
	if err != nil {
		log.Warn("docexport: annotation read failed; exporting without notes", "err", err)
		notes = nil
	}
	regions, err := s.Annotations.Regions(ctx, false)
	if err != nil {
		log.Warn("docexport: region read failed; exporting without regions", "err", err)
		regions = nil
	}
	return AnnotationRows(notes), RegionRows(regions)
}
