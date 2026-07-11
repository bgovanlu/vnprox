package change_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// minimalNodeAgent is a small, self-contained change.NodeAgent double (no
// pvemock/pve.Client dependency) for exercising LocalTimerAgent in
// isolation, independent of the full three-daemon harness.
type minimalNodeAgent struct {
	committed  map[string]string
	staged     map[string]string
	failReload map[string]bool
}

func newMinimalNodeAgent() *minimalNodeAgent {
	return &minimalNodeAgent{committed: map[string]string{}, staged: map[string]string{}, failReload: map[string]bool{}}
}

func (a *minimalNodeAgent) ReadInterfaces(_ context.Context, node string) (string, error) {
	return a.committed[node], nil
}
func (a *minimalNodeAgent) StageInterfaces(_ context.Context, node, content string) error {
	a.staged[node] = content
	return nil
}
func (a *minimalNodeAgent) ReloadInterfaces(_ context.Context, node string) error {
	if a.failReload[node] {
		return errors.New("injected reload failure")
	}
	a.committed[node] = a.staged[node]
	delete(a.staged, node)
	return nil
}
func (a *minimalNodeAgent) DiscardStaged(_ context.Context, node string) error {
	delete(a.staged, node)
	return nil
}

func newLocalTimerHarness(t *testing.T) (*change.LocalTimerAgent, *minimalNodeAgent, *store.NodeTimerRepo, *fakeTimers, *fakeClock) {
	t.Helper()
	agent := newMinimalNodeAgent()
	agent.committed["pveX"] = "original content"
	db := openTestDB(t)
	repo := store.NewNodeTimerRepo(db)
	timers := &fakeTimers{}
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	local := change.NewLocalTimerAgent(change.LocalTimerConfig{
		Nodes: agent, Repo: repo, TimerFunc: timers.New, Now: clk.now,
	})
	return local, agent, repo, timers, clk
}

// fakeClock is a tiny mutable clock double for direct LocalTimerAgent unit
// tests (distinct from threeDaemonHarness.clock, which auto-ticks — these
// tests want full manual control instead).
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestLocalTimerAgent_ArmThenFire_SelfRestores(t *testing.T) {
	local, agent, _, timers, clk := newLocalTimerHarness(t)
	ctx := context.Background()

	rec, err := local.ArmTimer(ctx, "cs-1", "pveX", "original content", clk.now().Add(120*time.Second).Unix())
	if err != nil {
		t.Fatalf("ArmTimer: %v", err)
	}
	if rec.Status != peer.TimerArmed {
		t.Errorf("ArmTimer() status = %s, want armed", rec.Status)
	}
	if n := timers.armedCount(); n != 1 {
		t.Fatalf("armedCount = %d, want 1", n)
	}

	// Simulate a mutation happening (as apply's reload would) so firing has
	// something to actually restore away from.
	agent.committed["pveX"] = "mutated content"

	timers.fireLatest(t)

	if agent.committed["pveX"] != "original content" {
		t.Errorf("committed content = %q, want self-restored to %q", agent.committed["pveX"], "original content")
	}

	status, err := local.TimerStatus(ctx, "cs-1", "pveX")
	if err != nil {
		t.Fatalf("TimerStatus: %v", err)
	}
	if status.Status != peer.TimerRolledBack {
		t.Errorf("TimerStatus() = %+v, want rolled_back", status)
	}
}

func TestLocalTimerAgent_ArmThenCancel_NoRestore(t *testing.T) {
	local, agent, _, timers, clk := newLocalTimerHarness(t)
	ctx := context.Background()

	if _, err := local.ArmTimer(ctx, "cs-2", "pveX", "original content", clk.now().Add(120*time.Second).Unix()); err != nil {
		t.Fatalf("ArmTimer: %v", err)
	}
	agent.committed["pveX"] = "mutated content"

	rec, err := local.CancelTimer(ctx, "cs-2", "pveX")
	if err != nil {
		t.Fatalf("CancelTimer: %v", err)
	}
	if rec.Status != peer.TimerCancelled {
		t.Errorf("CancelTimer() = %+v, want cancelled", rec)
	}
	if n := timers.armedCount(); n != 0 {
		t.Errorf("armedCount after cancel = %d, want 0", n)
	}
	// Content must be untouched — cancelling must never restore.
	if agent.committed["pveX"] != "mutated content" {
		t.Errorf("committed content = %q, want untouched %q", agent.committed["pveX"], "mutated content")
	}

	// Cancelling again is idempotent (returns the already-cancelled record,
	// not an error).
	rec2, err := local.CancelTimer(ctx, "cs-2", "pveX")
	if err != nil {
		t.Fatalf("second CancelTimer: %v", err)
	}
	if rec2.Status != peer.TimerCancelled {
		t.Errorf("second CancelTimer() = %+v, want still cancelled", rec2)
	}
}

func TestLocalTimerAgent_CancelNeverArmed_ErrTimerNotFound(t *testing.T) {
	local, _, _, _, _ := newLocalTimerHarness(t)
	ctx := context.Background()

	if _, err := local.CancelTimer(ctx, "cs-none", "pveX"); !errors.Is(err, peer.ErrTimerNotFound) {
		t.Errorf("CancelTimer(never armed) err = %v, want ErrTimerNotFound", err)
	}
	if _, err := local.TimerStatus(ctx, "cs-none", "pveX"); !errors.Is(err, peer.ErrTimerNotFound) {
		t.Errorf("TimerStatus(never armed) err = %v, want ErrTimerNotFound", err)
	}
}

func TestLocalTimerAgent_FireWithReloadFailure_RollbackFailed(t *testing.T) {
	local, agent, repo, timers, clk := newLocalTimerHarness(t)
	ctx := context.Background()

	if _, err := local.ArmTimer(ctx, "cs-3", "pveX", "original content", clk.now().Add(120*time.Second).Unix()); err != nil {
		t.Fatalf("ArmTimer: %v", err)
	}
	agent.committed["pveX"] = "mutated content"
	agent.failReload["pveX"] = true

	timers.fireLatest(t)

	row, err := repo.Get(ctx, "cs-3", "pveX")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != store.NodeTimerRollbackFailed || !row.Error.Valid || row.Error.String == "" {
		t.Errorf("node_timers row = %+v, want rollback_failed with an error detail", row)
	}
}

func TestLocalTimerAgent_ReArm_ReplacesPriorRecord(t *testing.T) {
	local, _, repo, timers, clk := newLocalTimerHarness(t)
	ctx := context.Background()

	if _, err := local.ArmTimer(ctx, "cs-4", "pveX", "v1", clk.now().Add(10*time.Second).Unix()); err != nil {
		t.Fatalf("first ArmTimer: %v", err)
	}
	if n := timers.armedCount(); n != 1 {
		t.Fatalf("armedCount after first arm = %d, want 1", n)
	}
	// Re-arming replaces the in-process timer too (the old one is stopped),
	// not just the DB row.
	if _, err := local.ArmTimer(ctx, "cs-4", "pveX", "v2", clk.now().Add(20*time.Second).Unix()); err != nil {
		t.Fatalf("second ArmTimer: %v", err)
	}
	if n := timers.armedCount(); n != 1 {
		t.Errorf("armedCount after re-arm = %d, want 1 (old timer replaced, not duplicated)", n)
	}
	row, err := repo.Get(ctx, "cs-4", "pveX")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.PreContent != "v2" {
		t.Errorf("PreContent = %q, want v2 (re-arm overwrites)", row.PreContent)
	}
}

func TestLocalTimerAgent_ArmPendingOnStartup_PastDeadlineFiresImmediately(t *testing.T) {
	local, agent, repo, timers, clk := newLocalTimerHarness(t)
	ctx := context.Background()

	// Arm directly against the repo (as if a previous process instance had
	// armed it) with a deadline already in the past relative to clk.
	if err := repo.Arm(ctx, store.NodeTimer{
		ChangesetID: "cs-5", Node: "pveX", PreContent: "original content",
		Deadline: clk.now().Add(-10 * time.Second).Unix(), ArmedAt: clk.now().Add(-20 * time.Second).Unix(),
	}); err != nil {
		t.Fatalf("seed Arm: %v", err)
	}
	agent.committed["pveX"] = "mutated content"

	if err := local.ArmPendingOnStartup(ctx); err != nil {
		t.Fatalf("ArmPendingOnStartup: %v", err)
	}
	if n := timers.armedCount(); n != 1 {
		t.Fatalf("armedCount after ArmPendingOnStartup = %d, want 1 (a zero-duration timer, but still armed until it fires)", n)
	}
	timers.fireLatest(t)
	if agent.committed["pveX"] != "original content" {
		t.Errorf("committed content = %q, want self-restored after startup re-arm", agent.committed["pveX"])
	}
}

func TestLocalTimerAgent_StopTimers_CancelsInProcessOnly(t *testing.T) {
	local, _, repo, timers, clk := newLocalTimerHarness(t)
	ctx := context.Background()

	if _, err := local.ArmTimer(ctx, "cs-6", "pveX", "original content", clk.now().Add(120*time.Second).Unix()); err != nil {
		t.Fatalf("ArmTimer: %v", err)
	}
	local.StopTimers()
	if n := timers.armedCount(); n != 0 {
		t.Errorf("armedCount after StopTimers = %d, want 0", n)
	}
	// The DB row must still say armed — StopTimers is a graceful-shutdown
	// stop, not a cancellation; ArmPendingOnStartup re-arms it next boot.
	row, err := repo.Get(ctx, "cs-6", "pveX")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != store.NodeTimerArmed {
		t.Errorf("row.Status after StopTimers = %s, want armed (unchanged)", row.Status)
	}
}
