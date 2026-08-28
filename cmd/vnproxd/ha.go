// SPDX-License-Identifier: Apache-2.0

// ha.go wires T-1704's active/standby HA manager into the daemon: the change-
// engine coordinator adapter (re-arm/quiesce), the late-bound LeaderGuard and
// findings provider (the manager is constructed after change.Service and the
// findings engine), and buildHAManager itself.

package main

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/ha"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// changeHACoordinator adapts *change.Service to ha.Coordinator: ReArm re-
// establishes the commit-confirm/schedule timers from the replicated absolute
// deadlines through the EXISTING T-205/T-1103 code paths (ArmPendingRollbacks +
// TickSchedules); Quiesce stops the in-process timers on demotion without
// touching persisted status.
type changeHACoordinator struct{ svc *change.Service }

func (c changeHACoordinator) ReArm(ctx context.Context) error {
	if err := c.svc.ArmPendingRollbacks(ctx); err != nil {
		return err
	}
	c.svc.TickSchedules(ctx)
	return nil
}

func (c changeHACoordinator) Quiesce() {
	c.svc.StopTimers()
	// T-2602: a demoted daemon must also stop driving canary holds; the
	// promoted one re-arms them from the store via ReArm above.
	c.svc.StopHoldTimers()
}

// haLeaderGuard lets change.Service's LeaderGuard reference the ha.Manager that
// is constructed after it. Until set (HA disabled, or before the manager
// exists), it reports leader=true so single-daemon behaviour is unchanged.
type haLeaderGuard struct {
	mgr *ha.Manager
	mu  sync.Mutex
}

func (g *haLeaderGuard) set(m *ha.Manager) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mgr = m
}

func (g *haLeaderGuard) IsLeader() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mgr == nil || g.mgr.IsLeader()
}

// haFindingsAdapter is the late-bound findings.HAReplicationProvider (the
// findings engine is built before the ha.Manager). A nil manager reports a
// zero Status (not degraded), so the ha_replication_degraded check is inert
// until HA is wired.
type haFindingsAdapter struct {
	mgr *ha.Manager
	mu  sync.Mutex
}

func (a *haFindingsAdapter) set(m *ha.Manager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mgr = m
}

func (a *haFindingsAdapter) Status() ha.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mgr == nil {
		return ha.Status{}
	}
	return a.mgr.Status()
}

// buildHAManager constructs the active/standby manager from the [ha] config
// section, replicating over the same coordinator peer.Client T-304 already
// uses. The announcer is chosen by mode (an operator VIP command or a DNS
// webhook), defaulting to a no-op (an externally-fronted pair).
func buildHAManager(cfg config.HAConfig, db *store.DB, svc *change.Service, client *peer.Client, logger *slog.Logger) (*ha.Manager, error) {
	repos := ha.StoreReplicationRepos{
		Changesets: store.NewChangesetRepo(db),
		Schedules:  store.NewChangeScheduleRepo(db),
		Tokens:     store.NewAPITokenRepo(db),
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Audit:      store.NewAuditRepo(db),
	}
	repl := ha.NewStoreReplication(repos)

	var announcer ha.Announcer = ha.NoopAnnouncer{}
	switch ha.Mode(cfg.Mode) {
	case ha.ModeVIP:
		if cfg.VipCommand != "" {
			announcer = ha.CommandAnnouncer{Path: cfg.VipCommand}
		}
	case ha.ModeDNS:
		if cfg.DNSWebhook != "" {
			announcer = ha.WebhookAnnouncer{URL: cfg.DNSWebhook}
		}
	}

	instanceID := cfg.InstanceID
	if instanceID == "" {
		if hn, err := os.Hostname(); err == nil {
			instanceID = hn
		} else {
			instanceID = "vnproxd"
		}
	}

	return ha.NewManager(ha.Config{
		InstanceID:    instanceID,
		Lease:         ha.NewStoreLeaseStore(store.NewHALeaseRepo(db)),
		Coordinator:   changeHACoordinator{svc: svc},
		Replicator:    ha.NewPeerReplicator(client, peer.Peer{Node: cfg.PeerNode, Addr: cfg.PeerAddr}),
		Source:        repl,
		Applier:       repl,
		Announcer:     announcer,
		Bootstrap:     cfg.Bootstrap,
		LeaseTTL:      cfg.LeaseTTL,
		RenewInterval: cfg.RenewInterval,
		FencingMargin: cfg.FencingMargin,
		LagThreshold:  int64(cfg.LagThreshold),
		Logger:        logger,
	})
}
