// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// setupMTUProbe builds T-1306's *mtuprobe.Service — a node-local scheduler
// binary-search DF-probing every path discoverer names, on this package's
// own coarser interval than latmesh's. discoverer is the exact same
// *latmesh.GraphDiscoverer instance setupLatMesh built (passed in, not
// rebuilt here) — see internal/mtuprobe's package doc comment for why this
// is literal instance reuse, not a second implementation of path discovery.
// Returns the Service itself, wired into api.Options.MTUProbe (GET
// /mtuprobe/results), findings.Config.MTU (vxlan_underlay_mtu's measured
// upgrade), and the map-edge annotation query — plus the single supervised
// probe-loop actor cmd/vnproxd's run group registers alongside every other
// owned goroutine (docs/development.md's "every goroutine has an owner and
// a shutdown path"). Unlike latmesh there is no prune-loop actor: this
// package holds only each link's current reading in memory, not a
// SQLite-backed ring (internal/mtuprobe's doc.go, "current-state, not a
// ring").
func setupMTUProbe(cfg *config.Config, discoverer *latmesh.GraphDiscoverer, logger *slog.Logger) (*mtuprobe.Service, []func(context.Context) error) {
	svc := mtuprobe.New(mtuprobe.Config{
		Discoverer:       discoverer,
		Prober:           mtuprobe.RealProber{},
		Logger:           logger,
		ProbeIntervalSec: cfg.MTUProbe.ProbeIntervalSec,
	})

	actors := []func(context.Context) error{svc.RunLoop}
	return svc, actors
}
