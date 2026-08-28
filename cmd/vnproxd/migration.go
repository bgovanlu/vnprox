// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/migration"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// migration.go wires T-1507's *migration.Planner into this daemon,
// reusing every dependency task's already-built machinery rather than a
// second reader of any kind:
//
//   - Graph: the same live *inventory.Graph every other read route shares.
//   - GuestConfig: sdnPVEClient, the collectors' own read-only PVE identity
//     (setupCollect's doc comment — "internal/sdn.Service reads PVE
//     directly and live per request, reusing the collectors' own read-only
//     identity rather than building a second client") — T-1507's guest-RAM
//     read is exactly that same shape of call, so it reuses the identical
//     client rather than requesting a third.
//   - Mesh: latMeshSvc (T-1303's *latmesh.Service) directly — it already
//     satisfies migration.MeshProvider structurally (the same "small
//     interface, real type satisfies it for free" seam
//     internal/findings.LatMeshProvider establishes for the identical
//     Heatmap method).
//   - Traffic: migrationTrafficAdapter below, reading the exact same
//     flow_samples ring + *flow.Classifier serviceclassify.go's
//     flowClassifyAdapter already reads for service_traffic_on_wrong_network
//     — no second flow reader, mirroring that adapter's own lazily-set
//     pattern (findings.Engine/api.Options.Migration are both constructed
//     before flowRepo exists — see server.go).
//
// sdnPVEClient/latMeshSvc can each independently be nil (a fresh daemon
// with no PVE credentials provisioned yet, docs/security.md's T-606 gap;
// or, in principle, a build wired without T-1303) — migration.Planner
// degrades each half of the assessment to a flagged best-effort default
// rather than failing to construct at all, the same nil-dependency
// degraded-mode convention every other Config in this codebase follows.

// setupMigrationPlanner builds T-1507's *migration.Planner and the
// traffic-volume adapter it needs — filled in once setupFlows returns
// (see migrationTrafficAdapter.set's call site in server.go, mirroring
// flowClassifyAdapterVal's identical two-step wiring).
func setupMigrationPlanner(graph *inventory.Graph, sdnPVEClient *pve.Client, latMeshSvc *latmesh.Service) (*migration.Planner, *migrationTrafficAdapter) {
	trafficAdapter := &migrationTrafficAdapter{}

	cfg := migration.Config{Graph: graph, Traffic: trafficAdapter}
	// A nil *pve.Client/*latmesh.Service must not be assigned into its
	// interface-typed Config field: an interface holding a typed nil is not
	// itself nil (the classic Go gotcha), and migration.Planner's own
	// nil-dependency checks compare the interface value, not the
	// underlying pointer — so this guards the exact same trap
	// serviceclassify.go's flowClassifyAdapter (implicitly, via its own
	// always-valid concrete type) never has to think about.
	if sdnPVEClient != nil {
		cfg.GuestConfig = sdnPVEClient
	}
	if latMeshSvc != nil {
		cfg.Mesh = latMeshSvc
	}

	return migration.New(cfg), trafficAdapter
}

// migrationTrafficLookbackSeconds bounds migrationTrafficAdapter's own
// flow_samples query — a short, recent window is the right shape for "how
// much migration traffic is on this link *right now*", mirroring
// serviceTrafficLookbackSeconds' identical reasoning for T-1504's
// service_traffic_on_wrong_network check (same file, same ring, same
// "recent slice, not a full retained-window scan" shape).
const migrationTrafficLookbackSeconds = 300

// migrationTrafficAdapter adapts a lazily-set *store.FlowSampleRepo +
// *flow.Classifier into migration.MigrationTrafficProvider: the current
// T-1504-classified "migration" service-class byte volume observed on this
// node's own flow_samples ring, converted to Mbps. flow_samples is
// node-local app data (docs/architecture.md §7), so this adapter only ever
// answers for *this* daemon's own node — see MigrationTrafficMbps's doc
// comment for what that means for a targetNode that isn't the local node.
type migrationTrafficAdapter struct {
	repo       *store.FlowSampleRepo
	classifier *flow.Classifier
	mu         sync.Mutex
}

func (a *migrationTrafficAdapter) set(repo *store.FlowSampleRepo, classifier *flow.Classifier) {
	a.mu.Lock()
	a.repo, a.classifier = repo, classifier
	a.mu.Unlock()
}

// MigrationTrafficMbps implements migration.MigrationTrafficProvider.
// node is accepted for interface-shape symmetry with a future cluster-wide
// (peer-fan-out) implementation; this node-local adapter answers from its
// own flow_samples ring regardless of which node is named — the same
// documented node-local-only scope T-1303's latmesh.Service.Heatmap and
// T-1006/T-1504's own local-ring reads already carry (docs/api.md's
// Latency mesh section: "no peer fan-out"). Returns ok=false — never an
// error — before repo/classifier are wired in (server.go's startup
// sequence) or on a query failure, so migration.Planner always degrades to
// its own flagged best-effort default rather than surfacing a raw error.
func (a *migrationTrafficAdapter) MigrationTrafficMbps(ctx context.Context, node string) (float64, bool) {
	_ = node
	a.mu.Lock()
	repo, classifier := a.repo, a.classifier
	a.mu.Unlock()
	if repo == nil || classifier == nil {
		return 0, false
	}

	since := time.Now().Add(-migrationTrafficLookbackSeconds * time.Second).Unix()
	samples, _, err := repo.Query(ctx, store.FlowFilter{FromTs: since}, "", serviceTrafficRowCap)
	if err != nil {
		return 0, false
	}
	if len(samples) == 0 {
		return 0, true
	}

	var migrationBytes int64
	for _, s := range samples {
		rec := flow.Record{
			Node: s.Node, SrcIP: s.SrcIP, DstIP: s.DstIP,
			SrcPort: s.SrcPort, DstPort: s.DstPort, Proto: s.Proto, VLAN: s.VLAN,
		}
		if classifier.Classify(rec) == flow.ServiceClassMigration {
			migrationBytes += s.Bytes
		}
	}

	mbps := float64(migrationBytes) * 8 / 1_000_000 / migrationTrafficLookbackSeconds
	return mbps, true
}
