package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/wan"
)

// setupWan builds T-1405's *wan.Service — a node-local scheduler probing
// this node's own operator-configured WAN reference targets
// (internal/wan.TargetDiscoverer), persisting readings to the bounded
// wan_probe_samples ring (store.WanProbeSampleRepo) — plus the two
// supervised actors (probe loop, prune loop) cmd/vnproxd's run group
// registers alongside every other owned goroutine (docs/development.md's
// "every goroutine has an owner and a shutdown path"). Its probe loop is
// literally *internal/latmesh.Service (wan.New's own doc comment) — this
// function is the WAN-probing sibling of setupLatMesh, reusing the exact
// same scheduler machinery T-1303 shipped and T-1306's setupMTUProbe
// already set the precedent of reusing rather than forking, retargeted at
// this task's own store table/config section instead of latency_samples/
// [latmesh]. Returns the Service itself (wired into api.Options.Wan (GET
// /wan/status, GET/PUT /wan/targets) and findings.Config.Wan (wan_degraded)
// — the same "one concrete type satisfies two small interfaces
// structurally, no adapter needed" shape *latmesh.Service itself already
// establishes for LatMeshService/LatMeshProvider) and the effective loss
// threshold (so findings.HealthThresholds.WanLossWarnPct can be kept in
// sync with the Service's own per-uplink "degraded" verdict rather than
// silently drifting from it).
func setupWan(cfg *config.Config, db *store.DB, localNode func() string, logger *slog.Logger) (*wan.Service, float64, []func(context.Context) error) {
	sampleRepo := store.NewWanProbeSampleRepo(db)
	targetRepo := store.NewWanTargetRepo(db)

	lossWarnPct := cfg.Wan.LossWarnPct
	if lossWarnPct <= 0 {
		lossWarnPct = wan.DefaultLossWarnPct
	}

	svc := wan.New(wan.Config{
		Store:            sampleRepo,
		Targets:          targetRepo,
		LocalNode:        localNode,
		Prober:           latmesh.RealProber{},
		Logger:           logger,
		ProbeIntervalSec: cfg.Wan.ProbeIntervalSec,
		RetentionMinutes: cfg.Wan.RetentionMinutes,
		MaxRows:          cfg.Wan.MaxRows,
		LossWarnPct:      lossWarnPct,
	})

	actors := []func(context.Context) error{
		svc.RunLoop,
		func(ctx context.Context) error {
			return svc.RunPruneLoop(ctx, wan.DefaultPruneInterval)
		},
	}
	return svc, lossWarnPct, actors
}
