package docexport

import (
	"context"
	"time"

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

// Service assembles config documentation exports from live daemon state.
// Every field is the same seam type internal/api already declares for the
// equivalent read route, so cmd/vnproxd's wiring passes the identical
// concrete values it already constructs for the router — this package
// never opens a second path to any of these sources.
type Service struct {
	Inventory InventorySource
	SDN       SDNSource // nil-safe: SDN section reports itself unavailable
	Ports     PortsSource
	Topo      TopologySource
	Now       func() time.Time
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

	return Gather(snap, tree, treeErr, ports, fwSnap, topo, now())
}
