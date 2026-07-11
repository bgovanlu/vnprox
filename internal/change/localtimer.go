package change

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// LocalTimerAgent is the node-side half of T-304's local-timer protocol
// (docs/features/change-management.md §4: "each node arms its own local
// timer at step start — no cross-node dependency for safety"). It runs in
// *every* daemon, independent of whether that daemon happens to be
// coordinating anything: it answers "what does this node do when told to
// arm/cancel/report a rollback timer for some changeset it may know nothing
// else about" using only NodeAgent (host writer) + a local store table
// (node_timers, 0003_node_timers.sql), never Service or the changesets
// table. It implements peer.TimerAgent structurally, so cmd/vnproxd wires
// one instance into both peer.ServerOptions.Timers (this node answering a
// coordinator's HTTP calls) and — for the coordinator's own node — directly
// into ClusterTimerAgent, bypassing the network round trip.
type LocalTimerAgent struct {
	nodes    NodeAgent
	repo     *store.NodeTimerRepo
	newTimer TimerFunc
	now      func() time.Time
	log      *slog.Logger
	timers   map[string]Stopper // key: changesetID+"\x00"+node
	mu       sync.Mutex
}

// LocalTimerConfig configures a LocalTimerAgent.
type LocalTimerConfig struct {
	Nodes     NodeAgent
	Repo      *store.NodeTimerRepo
	TimerFunc TimerFunc
	Now       func() time.Time
	Logger    *slog.Logger
}

// NewLocalTimerAgent constructs a LocalTimerAgent. Nodes and Repo are
// required.
func NewLocalTimerAgent(cfg LocalTimerConfig) *LocalTimerAgent {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timerFunc := cfg.TimerFunc
	if timerFunc == nil {
		timerFunc = func(d time.Duration, f func()) Stopper { return time.AfterFunc(d, f) }
	}
	return &LocalTimerAgent{
		nodes: cfg.Nodes, repo: cfg.Repo, newTimer: timerFunc, now: now, log: logger,
		timers: map[string]Stopper{},
	}
}

func timerKey(changesetID, node string) string { return changesetID + "\x00" + node }

// ArmTimer persists (changesetID, node)'s pre-apply content and deadline and
// arms a real timer that self-restores this node if CancelTimer doesn't run
// first — satisfies peer.TimerAgent.
func (a *LocalTimerAgent) ArmTimer(ctx context.Context, changesetID, node, content string, deadline int64) (peer.TimerRecord, error) {
	armedAt := a.now().Unix()
	row := store.NodeTimer{ChangesetID: changesetID, Node: node, PreContent: content, Deadline: deadline, ArmedAt: armedAt}
	if err := a.repo.Arm(ctx, row); err != nil {
		return peer.TimerRecord{}, fmt.Errorf("change: local-timer: arming %s/%s: %w", changesetID, node, err)
	}
	a.armRealTimerLocked(changesetID, node, deadline)
	return peer.TimerRecord{ChangesetID: changesetID, Node: node, Status: peer.TimerArmed, Deadline: deadline, ArmedAt: armedAt}, nil
}

// CancelTimer stops (changesetID, node)'s timer if still armed and marks it
// cancelled. Cancelling an already-resolved or never-armed timer is
// idempotent per peer.TimerAgent's contract; only a never-armed key returns
// peer.ErrTimerNotFound.
func (a *LocalTimerAgent) CancelTimer(ctx context.Context, changesetID, node string) (peer.TimerRecord, error) {
	a.mu.Lock()
	key := timerKey(changesetID, node)
	if t, ok := a.timers[key]; ok {
		t.Stop()
		delete(a.timers, key)
	}
	a.mu.Unlock()

	row, err := a.repo.Get(ctx, changesetID, node)
	if err != nil {
		if err == store.ErrNotFound {
			return peer.TimerRecord{}, peer.ErrTimerNotFound
		}
		return peer.TimerRecord{}, fmt.Errorf("change: local-timer: reading %s/%s before cancel: %w", changesetID, node, err)
	}
	resolvedAt := a.now().Unix()
	if row.Status == store.NodeTimerArmed {
		if err := a.repo.Resolve(ctx, changesetID, node, store.NodeTimerCancelled, resolvedAt, ""); err != nil {
			return peer.TimerRecord{}, fmt.Errorf("change: local-timer: cancelling %s/%s: %w", changesetID, node, err)
		}
		row.Status = store.NodeTimerCancelled
		row.ResolvedAt.Int64, row.ResolvedAt.Valid = resolvedAt, true
	}
	return nodeTimerToRecord(row), nil
}

// TimerStatus returns (changesetID, node)'s current record, or
// peer.ErrTimerNotFound if this node never armed one.
func (a *LocalTimerAgent) TimerStatus(ctx context.Context, changesetID, node string) (peer.TimerRecord, error) {
	row, err := a.repo.Get(ctx, changesetID, node)
	if err != nil {
		if err == store.ErrNotFound {
			return peer.TimerRecord{}, peer.ErrTimerNotFound
		}
		return peer.TimerRecord{}, fmt.Errorf("change: local-timer: reading %s/%s: %w", changesetID, node, err)
	}
	return nodeTimerToRecord(row), nil
}

// armRealTimerLocked (re-)arms the in-process timer for key, replacing any
// existing one, firing at deadline (clamped to "now" if already past —
// ArmPendingOnStartup's crash-while-overdue case).
func (a *LocalTimerAgent) armRealTimerLocked(changesetID, node string, deadline int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := timerKey(changesetID, node)
	if t, ok := a.timers[key]; ok {
		t.Stop()
		delete(a.timers, key)
	}
	d := time.Until(time.Unix(deadline, 0))
	if d < 0 {
		d = 0
	}
	a.timers[key] = a.newTimer(d, func() { a.fire(context.Background(), changesetID, node) })
}

// fire is the timer callback: if the row is still armed (not cancelled in
// the meantime), restore node from its recorded pre-apply content and
// reload, then resolve the row to rolled_back or rollback_failed.
func (a *LocalTimerAgent) fire(ctx context.Context, changesetID, node string) {
	a.mu.Lock()
	delete(a.timers, timerKey(changesetID, node))
	a.mu.Unlock()

	row, err := a.repo.Get(ctx, changesetID, node)
	if err != nil {
		a.log.Error("change: local-timer: loading row on fire", "changeset_id", changesetID, "node", node, "error", err)
		return
	}
	if row.Status != store.NodeTimerArmed {
		return // already cancelled (or previously resolved) — nothing to do
	}

	resolvedAt := a.now().Unix()
	if restoreErr := a.restore(ctx, node, row.PreContent); restoreErr != nil {
		a.log.Error("change: local-timer: self-restore failed", "changeset_id", changesetID, "node", node, "error", restoreErr)
		if err := a.repo.Resolve(ctx, changesetID, node, store.NodeTimerRollbackFailed, resolvedAt, restoreErr.Error()); err != nil {
			a.log.Error("change: local-timer: recording rollback_failed", "changeset_id", changesetID, "node", node, "error", err)
		}
		return
	}
	a.log.Info("change: local-timer: self-restored (no confirm/cancel before deadline)", "changeset_id", changesetID, "node", node)
	if err := a.repo.Resolve(ctx, changesetID, node, store.NodeTimerRolledBack, resolvedAt, ""); err != nil {
		a.log.Error("change: local-timer: recording rolled_back", "changeset_id", changesetID, "node", node, "error", err)
	}
}

// restore stages content as node's committed interfaces file and reloads —
// the same stage+reload composition apply_exec.go's restoreNode uses, kept
// as its own small copy here so LocalTimerAgent has no dependency on
// Service (it must keep working when this node isn't coordinating anything
// at all).
func (a *LocalTimerAgent) restore(ctx context.Context, node, content string) error {
	if err := a.nodes.StageInterfaces(ctx, node, content); err != nil {
		return fmt.Errorf("change: local-timer: staging restore for node %s: %w", node, err)
	}
	if err := a.nodes.ReloadInterfaces(ctx, node); err != nil {
		return fmt.Errorf("change: local-timer: reloading restore for node %s: %w", node, err)
	}
	return nil
}

// ArmPendingOnStartup re-arms every still-armed node_timers row from the DB
// (docs/development.md: rollback timers must survive a daemon restart —
// T-205's ArmPendingRollbacks, applied here to the per-node timer table). A
// deadline already in the past fires (restores) immediately rather than
// waiting for a zero-duration timer to schedule, so a box that was down past
// its own deadline self-heals as soon as it comes back regardless of how
// long it was down.
func (a *LocalTimerAgent) ArmPendingOnStartup(ctx context.Context) error {
	armed, err := a.repo.ListByStatus(ctx, store.NodeTimerArmed)
	if err != nil {
		return fmt.Errorf("change: local-timer: listing armed timers on startup: %w", err)
	}
	for _, row := range armed {
		a.armRealTimerLocked(row.ChangesetID, row.Node, row.Deadline)
		a.log.Info("change: local-timer: re-armed from DB", "changeset_id", row.ChangesetID, "node", row.Node, "deadline", row.Deadline)
	}
	return nil
}

// StopTimers cancels every in-process timer (graceful shutdown) without
// touching any node_timers row — ArmPendingOnStartup re-arms them from the
// DB on the next start, exactly like change.Service.StopTimers.
func (a *LocalTimerAgent) StopTimers() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, t := range a.timers {
		t.Stop()
		delete(a.timers, k)
	}
}

func nodeTimerToRecord(row store.NodeTimer) peer.TimerRecord {
	rec := peer.TimerRecord{
		ChangesetID: row.ChangesetID, Node: row.Node,
		Status: peer.TimerStatus(row.Status), Deadline: row.Deadline, ArmedAt: row.ArmedAt,
	}
	if row.ResolvedAt.Valid {
		rec.ResolvedAt = row.ResolvedAt.Int64
	}
	if row.Error.Valid {
		rec.Error = row.Error.String
	}
	return rec
}
