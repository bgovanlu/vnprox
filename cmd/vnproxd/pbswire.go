package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pbs"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// pbswire.go wires T-1206's internal/pbs package (PBS network awareness) into
// this daemon. PVE's own storage config (GET /storage) and backup jobs (GET
// /cluster/backup) are read once at startup via the same read-only
// sdnPVEClient every other read-only discovery here uses — no new PVE client,
// no new credentials, no PBS API client (this task's "read the owning
// system's own knowledge of itself" boundary). Storage and backup-job
// configuration change on the order of an administrator's action, not a poll
// interval, so pbsProviderAdapter.PBSOverlay re-projects that one-time-read
// Status against the graph's *current* snapshot on every /topology or /pbs
// request (pbs.Project is pure and cheap) rather than re-reading PVE each
// time — the same rationale setupCollect's own rare-config reads follow.
//
// A cluster with no PBS storage configured at all (the common case in every
// fixture/dev environment without a real PBS) degrades cleanly: zero hosts,
// zero paths, no map decoration — never an error, never a spurious node.
func setupPBS(ctx context.Context, pveClient *pve.Client, graph *inventory.Graph, logger *slog.Logger) *pbsProviderAdapter {
	adapter := &pbsProviderAdapter{graph: graph}
	if pveClient == nil {
		return adapter
	}

	clusterStatus, err := pveClient.ClusterStatus(ctx)
	if err != nil {
		logger.Warn("pbs: reading cluster status for node list", "error", err)
		return adapter
	}
	var nodes []string
	for _, s := range clusterStatus {
		if s.Type == "node" {
			nodes = append(nodes, s.Name)
		}
	}

	status, err := pbs.Discover(ctx, pveClient, nodes)
	if err != nil {
		logger.Warn("pbs: discovering storage and backup-job config", "error", err)
		return adapter
	}
	adapter.status = status
	return adapter
}

// pbsProviderAdapter implements api.PBSService (GET /pbs + the /topology
// pbs-host/backup-path overlay). Returns a zero Overlay (never an error)
// before the daemon has a graph — the same lazily-ready degradation every
// other snapshot-backed adapter uses.
type pbsProviderAdapter struct {
	graph  *inventory.Graph
	status pbs.Status
}

func (a *pbsProviderAdapter) PBSOverlay(_ context.Context) (pbs.Overlay, error) {
	if a == nil || a.graph == nil {
		return pbs.Overlay{}, nil
	}
	return pbs.Project(a.graph.Snapshot(), a.status), nil
}
