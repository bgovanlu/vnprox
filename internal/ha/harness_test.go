// SPDX-License-Identifier: Apache-2.0

package ha_test

// T-1704 two-daemon failover harness with a REAL change.Service on each daemon
// (an injected fake clock + fake timers, no real sleeps). Test <-> AC map:
//
//   AC1 -> TestFailover_ReArmsSameAbsoluteDeadline_RollsBackExactlyOnce,
//          TestFailover_ConfirmAfterPromotion_Commits
//   AC4 -> TestFailover_ScheduledWindowSurvivesFailover
//
// The two daemons operate on ONE shared set of cluster-node files (there is one
// /etc/network/interfaces per node; whichever coordinator is active writes it),
// but each has its OWN app store (changesets/snapshots/audit), replicated over
// an in-memory link with an injectable partition switch.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/ha"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// sharedNodes is an in-memory change.NodeAgent standing in for the cluster's
// real per-node interfaces files — shared by both daemons (one set of files).
type sharedNodes struct {
	committed map[string]string
	staged    map[string]string
	mu        sync.Mutex
}

func newSharedNodes() *sharedNodes {
	return &sharedNodes{committed: map[string]string{"pve1": "auto lo\niface lo inet loopback\n"}, staged: map[string]string{}}
}

func (n *sharedNodes) ReadInterfaces(_ context.Context, node string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.committed[node], nil
}
func (n *sharedNodes) StageInterfaces(_ context.Context, node, content string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.staged[node] = content
	return nil
}
func (n *sharedNodes) ReloadInterfaces(_ context.Context, node string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.committed[node] = n.staged[node]
	delete(n.staged, node)
	return nil
}
func (n *sharedNodes) DiscardStaged(_ context.Context, node string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.staged, node)
	return nil
}
func (n *sharedNodes) committedOf(node string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.committed[node]
}

// haTimer / haTimers is a change.TimerFunc registry that fires on demand (no
// real time), so autoRollback can be triggered deterministically at the fake
// clock's chosen instant.
type haTimer struct {
	fn      func()
	stopped bool
}

func (t *haTimer) Stop() bool {
	t.stopped = true
	return true
}

type haTimers struct {
	timers []*haTimer
	mu     sync.Mutex
}

func (ft *haTimers) New(_ time.Duration, f func()) change.Stopper {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &haTimer{fn: f}
	ft.timers = append(ft.timers, t)
	return t
}

// fireAll fires every currently-armed (not-stopped) timer exactly once.
func (ft *haTimers) fireAll() {
	ft.mu.Lock()
	pending := make([]func(), 0, len(ft.timers))
	for _, t := range ft.timers {
		if !t.stopped && t.fn != nil {
			pending = append(pending, t.fn)
			t.stopped = true
			t.fn = nil
		}
	}
	ft.mu.Unlock()
	for _, fn := range pending {
		fn()
	}
}

// armedCount reports how many timers are currently armed (not stopped).
func (ft *haTimers) armedCount() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	n := 0
	for _, t := range ft.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

// changeCoordinator adapts change.Service to ha.Coordinator: ReArm re-arms the
// commit-confirm timers and re-ticks the scheduler from the persisted absolute
// deadlines (the EXISTING T-205/T-1103 code paths); Quiesce stops the
// in-process timers without touching persisted status.
type changeCoordinator struct {
	svc *change.Service
}

func (c *changeCoordinator) ReArm(ctx context.Context) error {
	if err := c.svc.ArmPendingRollbacks(ctx); err != nil {
		return err
	}
	c.svc.TickSchedules(ctx)
	return nil
}
func (c *changeCoordinator) Quiesce() { c.svc.StopTimers() }

// leaderHandle lets the LeaderGuard closure reference the Manager that is
// constructed after the change.Service.
type leaderHandle struct {
	mgr *ha.Manager
	mu  sync.Mutex
}

func (h *leaderHandle) set(m *ha.Manager) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mgr = m
}
func (h *leaderHandle) IsLeader() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.mgr != nil && h.mgr.IsLeader()
}

// haDaemon is one daemon in the harness.
type haDaemon struct {
	svc    *change.Service
	mgr    *ha.Manager
	timers *haTimers
	repos  storeSet
	link   *link
}

type storeSet struct {
	db        *store.DB
	changeset *store.ChangesetRepo
	sched     *store.ChangeScheduleRepo
	snap      *store.SnapshotRepo
	blob      *store.BlobRepo
	audit     *store.AuditRepo
}

func newHADaemon(t *testing.T, id string, nodes change.NodeAgent, clk *fakeClock, l *link, bootstrap bool) *haDaemon {
	t.Helper()
	db := openStore(t)
	ss := storeSet{
		db: db, changeset: store.NewChangesetRepo(db), sched: store.NewChangeScheduleRepo(db),
		snap: store.NewSnapshotRepo(db), blob: store.NewBlobRepo(db), audit: store.NewAuditRepo(db),
	}
	timers := &haTimers{}
	handle := &leaderHandle{}
	svc, err := change.NewService(change.Config{
		Changesets: ss.changeset, Audit: ss.audit, Nodes: nodes, Snapshots: ss.snap, Blobs: ss.blob,
		Schedules: ss.sched, Now: clk.Now, TimerFunc: timers.New, Clock: clk,
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
		LeaderGuard:   handle.IsLeader,
	})
	if err != nil {
		t.Fatalf("change.NewService(%s): %v", id, err)
	}
	replRepos := ha.StoreReplicationRepos{
		Changesets: ss.changeset, Schedules: ss.sched, Tokens: store.NewAPITokenRepo(db),
		Snapshots: ss.snap, Blobs: ss.blob, Audit: ss.audit,
	}
	repl := ha.NewStoreReplication(replRepos)
	mgr, err := ha.NewManager(ha.Config{
		Clock: clk, InstanceID: id, Lease: ha.NewStoreLeaseStore(store.NewHALeaseRepo(db)),
		Coordinator: &changeCoordinator{svc: svc}, Replicator: l, Source: repl, Applier: repl,
		Announcer: ha.NoopAnnouncer{}, Bootstrap: bootstrap,
		LeaseTTL: 6 * time.Second, RenewInterval: 2 * time.Second, FencingMargin: 6 * time.Second,
	})
	if err != nil {
		t.Fatalf("ha.NewManager(%s): %v", id, err)
	}
	handle.set(mgr)
	if startErr := mgr.Start(context.Background()); startErr != nil {
		t.Fatalf("mgr.Start(%s): %v", id, startErr)
	}
	return &haDaemon{svc: svc, mgr: mgr, timers: timers, repos: ss, link: l}
}

func bridgeCreate(node, name string) change.Op {
	return change.Op{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Params: &change.BridgeCreateParams{Comments: "ha harness"},
	}
}

// buildPair constructs {A bootstrap-active, B standby} over shared nodes, with
// the replication links cross-wired.
func buildPair(t *testing.T) (*fakeClock, *sharedNodes, *haDaemon, *haDaemon) {
	t.Helper()
	clk := newFakeClock()
	nodes := newSharedNodes()
	aToB := &link{}
	bToA := &link{}
	a := newHADaemon(t, "node-a", nodes, clk, aToB, true)
	b := newHADaemon(t, "node-b", nodes, clk, bToA, false)
	aToB.setPeer(b.mgr)
	bToA.setPeer(a.mgr)
	return clk, nodes, a, b
}
