// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// cephwire.go wires T-1503's internal/ceph package into this daemon's two
// consumers: T-1504's *flow.Classifier (registering Ceph's declared
// public/cluster CIDRs under NetworkSourceKindCeph via the generic
// flow.NewCIDRSource constructor — "T-1503 supplies T-1504's engine with
// Ceph's network declarations", classify.go's own doc comment names this
// exact call site as its one remaining integration step) and the findings
// engine's CephProvider seam (health_ceph.go), backing
// ceph_corosync_shared_link/ceph_cluster_mtu_mismatch/ceph_single_nic.
//
// PVE's own Ceph public/cluster network declaration and OSD placement are
// read once at startup via pveClient (the same read-only identity
// sdnPVEClient already is — no new credentials, no new PVE client,
// mirroring T-1206's "read the owning system's own knowledge of itself"
// boundary this task's card cites) — a Ceph network topology changes on the
// order of a cluster's lifetime, the same "rare, administrator-driven
// event" rationale setupFlowClassifier's own doc comment states for
// corosync's ring addresses. "Continuously computed" (this check family's
// documented convention) means cephProviderAdapter.CephOverlay re-projects
// that one-time-read Status against the graph's *current* snapshot on every
// findings.Engine cycle (ceph.Project is pure and cheap), not that PVE's
// Ceph config is re-read every cycle.
//
// A cluster with no Ceph installed at all (status.PublicNetwork/
// ClusterNetwork both empty, the common case in every fixture/dev
// environment this daemon runs against without real Ceph) degrades
// cleanly: no classifier sources get registered, and the three findings
// checks simply have nothing to evaluate — never an error, never a
// spurious finding.
func setupCeph(ctx context.Context, pveClient *pve.Client, graph *inventory.Graph, classifier *flow.Classifier, logger *slog.Logger) *cephProviderAdapter {
	adapter := &cephProviderAdapter{graph: graph}
	if pveClient == nil {
		return adapter
	}

	clusterStatus, err := pveClient.ClusterStatus(ctx)
	if err != nil {
		logger.Warn("ceph: reading cluster status for node list", "error", err)
		return adapter
	}
	var nodes []string
	for _, s := range clusterStatus {
		if s.Type == "node" {
			nodes = append(nodes, s.Name)
		}
	}

	status, err := ceph.Discover(ctx, pveClient, nodes)
	if err != nil {
		logger.Warn("ceph: discovering public/cluster network config and OSD placement", "error", err)
		return adapter
	}
	adapter.status = status

	if classifier != nil {
		registerCephClassifierSource(classifier, flow.ServiceClassCephPublic, status.PublicNetwork, logger)
		registerCephClassifierSource(classifier, flow.ServiceClassCephCluster, status.ClusterNetwork, logger)
	}

	if cor, err := host.ReadCorosyncConf(host.DefaultCorosyncConfPath); err == nil {
		adapter.cor = cor
	} else {
		logger.Info("ceph: no corosync.conf found; ceph_corosync_shared_link will not be evaluated", "error", err)
	}

	return adapter
}

// registerCephClassifierSource registers cidr (a declared Ceph public or
// cluster network) with classifier under class, when cidr is non-empty —
// see NewCIDRSource's doc comment for why this package uses the generic
// constructor directly rather than a Ceph-specific wrapper.
func registerCephClassifierSource(classifier *flow.Classifier, class flow.ServiceClass, cidr string, logger *slog.Logger) {
	if cidr == "" {
		return
	}
	src, err := flow.NewCIDRSource(flow.NetworkSourceKindCeph, class, []string{cidr}, nil)
	if err != nil {
		logger.Warn("ceph: building flow classifier source", "serviceClass", class, "cidr", cidr, "error", err)
		return
	}
	classifier.RegisterNetworkSource(flow.NetworkSourceKindCeph, src)
}

// cephProviderAdapter implements findings.CephProvider (health_ceph.go).
type cephProviderAdapter struct {
	graph  *inventory.Graph
	cor    *host.CorosyncConfig
	status ceph.Status
}

// CephOverlay implements findings.CephProvider. Returns a zero Overlay
// (never an error) when the daemon has no graph yet — mirrors every other
// lazily-ready adapter's before-startup-completes degradation.
func (a *cephProviderAdapter) CephOverlay() (ceph.Overlay, *host.CorosyncConfig, error) {
	if a == nil || a.graph == nil {
		return ceph.Overlay{}, nil, nil
	}
	snap := a.graph.Snapshot()
	return ceph.Project(snap, a.status), a.cor, nil
}

// Overlay implements api.CephService (GET /ceph/status) — the same
// re-project-against-current-snapshot behavior as CephOverlay above, minus
// the corosync config findings.CephProvider needs and this route doesn't.
func (a *cephProviderAdapter) Overlay(_ context.Context) (ceph.Overlay, error) {
	overlay, _, err := a.CephOverlay()
	return overlay, err
}
