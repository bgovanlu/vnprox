// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/store"
)

// setupLatMesh builds T-1303's *latmesh.Service — a node-local scheduler
// probing every corosync/guest-fabric pair this node shares with another
// cluster node (internal/latmesh.GraphDiscoverer), persisting readings to
// the bounded latency_samples ring (store.LatencySampleRepo) — plus the two
// supervised actors (probe loop, prune loop) cmd/vnproxd's run group
// registers alongside every other owned goroutine (docs/development.md's
// "every goroutine has an owner and a shutdown path"). Returns the Service
// itself (wired directly into api.Options.LatMesh (GET /latmesh/*) and
// findings.Config.LatMesh (path_latency_degraded/path_loss) — the same
// "one concrete type satisfies two small interfaces structurally, no
// adapter needed" shape *metrics.Sampler already establishes for
// MetricsProvider/MetricsService) and the GraphDiscoverer instance itself,
// so setupMTUProbe (T-1306) can share the exact same discoverer rather than
// building a second, functionally-identical one — internal/mtuprobe's own
// package doc comment documents this as literal instance reuse, not just
// "the same algorithm called twice".
func setupLatMesh(cfg *config.Config, db *store.DB, graph *inventory.Graph, localNode func() string, logger *slog.Logger) (*latmesh.Service, *latmesh.GraphDiscoverer, []func(context.Context) error) {
	repo := store.NewLatencySampleRepo(db)
	discoverer := &latmesh.GraphDiscoverer{Graph: graph, LocalNode: localNode, Logger: logger}

	svc := latmesh.New(latmesh.Config{
		Store:            repo,
		Discoverer:       discoverer,
		Prober:           latmesh.RealProber{},
		Logger:           logger,
		ProbeIntervalSec: cfg.Latmesh.ProbeIntervalSec,
		RetentionMinutes: cfg.Latmesh.RetentionMinutes,
		MaxRows:          cfg.Latmesh.MaxRows,
	})

	actors := []func(context.Context) error{
		svc.RunLoop,
		func(ctx context.Context) error {
			return svc.RunPruneLoop(ctx, latmesh.DefaultPruneInterval)
		},
	}
	return svc, discoverer, actors
}
