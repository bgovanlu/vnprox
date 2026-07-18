package latmesh

import (
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// GraphDiscoverer is the production Discoverer: re-reads the live
// inventory snapshot and the local node's corosync.conf fresh on every
// Pairs() call (both cheap, local reads — see internal/host.
// ReadCorosyncConf's doc comment on why any node can read its own local
// copy) so cluster membership/bridge changes are picked up without a
// daemon restart.
type GraphDiscoverer struct {
	Graph            *inventory.Graph
	LocalNode        func() string
	Logger           *slog.Logger
	CorosyncConfPath string
}

// Pairs implements Discoverer.
func (g *GraphDiscoverer) Pairs() []Pair {
	logger := g.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if g.Graph == nil || g.LocalNode == nil {
		return nil
	}
	localNode := g.LocalNode()
	if localNode == "" {
		return nil
	}

	path := g.CorosyncConfPath
	if path == "" {
		path = host.DefaultCorosyncConfPath
	}
	coro, err := host.ReadCorosyncConf(path)
	if err != nil {
		// A node with no corosync.conf at all (not yet clustered) is not an
		// error worth logging above debug — the same tolerant treatment
		// health_corosync.go's own CorosyncProvider seam gives a missing
		// config; the guest fabric below still works standalone.
		logger.Debug("latmesh: no corosync config available, corosync fabric pairs skipped", "path", path, "error", err)
		coro = nil
	}

	return DiscoverPairs(g.Graph.Snapshot(), coro, localNode)
}
