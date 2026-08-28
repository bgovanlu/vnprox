// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/store"
)

// serviceclassify.go wires T-1504's *flow.Classifier into this daemon: the
// one live NetworkSource this composition root has real data for today
// (corosync's configured ring addresses — the same static-address
// substrate T-803's corosync_link_degraded check and internal/change's
// safety interlocks already read via internal/host.ReadCorosyncConf), plus
// the recent-classified-flows adapter T-1504's
// service_traffic_on_wrong_network finding needs.
//
// Deliberately NOT wired here: a migration-network source (no live reader
// of PVE's own datacenter.cfg `migration: network=` exists anywhere in this
// codebase — a repeatedly-documented gap, see internal/flow/classify.go's
// doc comment and planning/reports/needs-hardware-validation.md), a
// backup-path (PBS) source (T-1206 is not present in this repo — see
// flow.NewBackupPathSource's doc comment), and Ceph CIDRs (T-1503's own
// job to register once that card lands). Each remains a fully-working,
// independently-tested NetworkSource constructor in internal/flow — only
// their live production data source is missing, not their mechanism.

// setupFlowClassifier builds T-1504's *flow.Classifier and registers
// corosync's ring addresses under NetworkSourceKindCorosync. Read once at
// startup (not re-read on a poll loop): corosync.conf changing on a running
// cluster is a rare, administrator-driven event no other reader in this
// codebase currently re-polls for either. A missing/unreadable
// corosync.conf (a single, not-yet-clustered node) degrades to an empty
// classifier — GET /flows' serviceClass is simply "unclassified" for
// everything, never an error.
func setupFlowClassifier(logger *slog.Logger) *flow.Classifier {
	classifier := flow.NewClassifier()

	cor, err := host.ReadCorosyncConf(host.DefaultCorosyncConfPath)
	if err != nil {
		logger.Info("flow classify: no corosync.conf found; corosync traffic will not be attributed to serviceClass", "error", err)
		return classifier
	}

	var ringAddrs []string
	for _, n := range cor.Nodes {
		ringAddrs = append(ringAddrs, n.RingAddrs...)
	}
	if len(ringAddrs) == 0 {
		return classifier
	}
	// vlans is left nil (no declared VLAN) — see flow.NewCorosyncSource's
	// doc comment: this composition root has no source for corosync's
	// intended VLAN today, so service_traffic_on_wrong_network stays
	// silent for corosync traffic until an operator/future card supplies
	// one, rather than guessing.
	classifier.RegisterNetworkSource(flow.NetworkSourceKindCorosync, flow.NewCorosyncSource(ringAddrs, nil))
	return classifier
}

// serviceTrafficLookbackSeconds/serviceTrafficRowCap bound
// flowClassifyAdapter.RecentClassified's own flow_samples query — a small,
// recent slice of the retained window is enough to detect a currently-
// ongoing wrong-network condition (the finding is hysteresis-debounced
// over several Engine cycles anyway, docs/features/monitoring.md §5's
// convention); this is deliberately not a full-window scan, mirroring
// checkFwRuleUnused's own "already-aggregated, bounded read" shape rather
// than re-querying flow_samples' full retained window every findings cycle.
const (
	serviceTrafficLookbackSeconds = 300
	serviceTrafficRowCap          = 5000
)

// flowClassifyAdapter adapts a lazily-set *store.FlowSampleRepo +
// *flow.Classifier into findings.FlowProvider (T-1504's
// service_traffic_on_wrong_network check). server.go constructs
// findings.Engine (via setupFindings) before setupFlows builds flowRepo —
// mirrors mgmtStatusAdapter/scheduleMissedAdapter's identical lazily-set
// pattern (findings.go's doc comments) — so this adapter is wired in with
// its target unset and filled in via set() once flowRepo/the classifier
// both exist, always before the daemon starts serving requests or the
// findings RunLoop actually runs. Safe for concurrent use.
type flowClassifyAdapter struct {
	repo       *store.FlowSampleRepo
	classifier *flow.Classifier
	mu         sync.Mutex
}

func (a *flowClassifyAdapter) set(repo *store.FlowSampleRepo, classifier *flow.Classifier) {
	a.mu.Lock()
	a.repo, a.classifier = repo, classifier
	a.mu.Unlock()
}

// RecentClassified implements findings.FlowProvider. Returns (nil, nil) —
// no findings this cycle, not an error — if called before set (can only
// happen if something evaluates findings before server.go finishes its
// startup sequence, which doesn't occur in production).
func (a *flowClassifyAdapter) RecentClassified() ([]flow.Classified, error) {
	a.mu.Lock()
	repo, classifier := a.repo, a.classifier
	a.mu.Unlock()
	if repo == nil || classifier == nil {
		return nil, nil
	}

	since := time.Now().Add(-serviceTrafficLookbackSeconds * time.Second).Unix()
	samples, _, err := repo.Query(context.Background(), store.FlowFilter{FromTs: since}, "", serviceTrafficRowCap)
	if err != nil {
		return nil, fmt.Errorf("cmd/vnproxd: querying recent flow samples for service classification: %w", err)
	}

	records := make([]flow.Record, len(samples))
	for i, s := range samples {
		records[i] = flow.Record{
			Node: s.Node, SrcIP: s.SrcIP, DstIP: s.DstIP,
			SrcPort: s.SrcPort, DstPort: s.DstPort, Proto: s.Proto, VLAN: s.VLAN,
		}
	}
	return classifier.ClassifyBatch(records), nil
}
