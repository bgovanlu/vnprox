// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestThreeDaemon_ApplyConfirm_CancelsAllLocalTimers is T-304's AC1: a
// changeset touching all three nodes, applied then confirmed, cancels all
// three nodes' local rollback timers — verified against each node's own
// node_timers DB row and the coordinator's persisted apply log.
func TestThreeDaemon_ApplyConfirm_CancelsAllLocalTimers(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()
	nodes := []string{"pve1", "pve2", "pve3"}

	cs := h.mustCreate(t, threeNodeOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := h.get(t, cs.ID).Status; got != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", got)
	}

	// DB: every node armed its own timer.
	for _, node := range nodes {
		row := h.nodeTimer(t, cs.ID, node)
		if row.Status != store.NodeTimerArmed {
			t.Errorf("node %s timer status = %s, want armed", node, row.Status)
		}
	}

	h.advance(5 * time.Second) // real time passes before the user confirms
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got := h.get(t, cs.ID).Status; got != change.StatusCommitted {
		t.Fatalf("status after confirm = %s, want committed", got)
	}

	// DB: every node's timer is now cancelled, and no longer counts as
	// armed in that node's own in-process timer set (fireLatest would find
	// nothing to fire).
	for _, node := range nodes {
		row := h.nodeTimer(t, cs.ID, node)
		if row.Status != store.NodeTimerCancelled {
			t.Errorf("node %s timer status after confirm = %s, want cancelled", node, row.Status)
		}
	}
	if n := h.coordTimer.armedCount(); n != 0 {
		t.Errorf("pve1 local-timer armedCount = %d, want 0 after confirm", n)
	}
	for _, node := range []string{"pve2", "pve3"} {
		if n := h.peers[node].timers.armedCount(); n != 0 {
			t.Errorf("%s local-timer armedCount = %d, want 0 after confirm", node, n)
		}
	}

	// Log: the coordinator's persisted apply log agrees.
	log := h.applyLog(t, cs.ID)
	if len(log.NodeTimers) != 3 {
		t.Fatalf("ApplyLog.NodeTimers = %+v, want 3 entries", log.NodeTimers)
	}
	for _, nt := range log.NodeTimers {
		if nt.Status != change.NodeTimerStatusCancelled {
			t.Errorf("ApplyLog.NodeTimers[%s].Status = %s, want cancelled", nt.Node, nt.Status)
		}
	}
}

// TestThreeDaemon_Partition_IndependentLocalRollback_ThenReconcile is T-304's
// AC2: after all three nodes committed their reload (awaiting_confirm), the
// coordinator<->pve3 link is cut. With no confirm, the coordinator's own
// deadline elapses: pve1/pve2 (still reachable) roll back via the
// coordinator's push; pve3 — unreachable — is left "unknown" in the log but
// independently rolls back at its own local deadline. Once the partition
// heals, Reconcile resolves pve3's entry, and the changeset reads
// rolled_back with per-node detail throughout.
func TestThreeDaemon_Partition_IndependentLocalRollback_ThenReconcile(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()
	nodes := []string{"pve1", "pve2", "pve3"}
	pre := map[string]string{}
	for _, node := range nodes {
		pre[node] = mustReadNode(t, h, node)
	}

	cs := h.mustCreate(t, threeNodeOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	newPve3 := h.committed("pve3")
	if newPve3 == pre["pve3"] {
		t.Fatalf("pve3 committed content unchanged after apply — test fixture assumption broken")
	}

	// Partition after every node's own reload already succeeded (each armed
	// its own local timer for the deadline the coordinator computed once,
	// up front).
	h.cutPeer("pve3")

	// Real time passes before the confirm window elapses (the default is
	// 120s) — advancing the shared clock also keeps every peer request's
	// HMAC timestamp/replay signature distinct from the ones already sent
	// during apply, exactly as real wall-clock time would.
	h.advance(130 * time.Second)

	// No confirm arrives; the coordinator's own bookkeeping deadline fires.
	h.svcTimers.fireLatest(t)

	got := h.get(t, cs.ID)
	if got.Status != change.StatusRolledBack {
		t.Fatalf("status after coordinator deadline = %s, want rolled_back", got.Status)
	}
	if h.committed("pve1") != pre["pve1"] {
		t.Errorf("pve1 not restored by coordinator push")
	}
	if h.committed("pve2") != pre["pve2"] {
		t.Errorf("pve2 not restored by coordinator push")
	}
	// pve3 could not be reached by the coordinator's push — it must not
	// have silently "failed" the whole rollback; it's still on the new
	// content, pending its own timer.
	if h.committed("pve3") != newPve3 {
		t.Fatalf("pve3 changed before its own local timer fired — coordinator must not have touched an unreachable node")
	}
	log := h.applyLog(t, cs.ID)
	found := false
	for _, nt := range log.NodeTimers {
		if nt.Node == "pve3" {
			found = true
			if nt.Status != change.NodeTimerStatusUnknown {
				t.Errorf("pve3 NodeTimerLog.Status = %s, want unknown (coordinator couldn't reach it)", nt.Status)
			}
		}
	}
	if !found {
		t.Fatalf("ApplyLog.NodeTimers has no pve3 entry: %+v", log.NodeTimers)
	}

	// pve3's own local timer fires independently — no coordinator
	// involvement, proving "no node's safety depends on cluster
	// connectivity".
	h.fireDeadline(t, "pve3")
	if h.committed("pve3") != pre["pve3"] {
		t.Fatalf("pve3 did not self-restore at its own local deadline")
	}
	row := h.nodeTimer(t, cs.ID, "pve3")
	if row.Status != store.NodeTimerRolledBack {
		t.Errorf("pve3 node_timers status = %s, want rolled_back", row.Status)
	}

	// Heal the partition and reconcile: the coordinator's own record of
	// pve3 catches up to "rolled_back" too.
	h.healPeer("pve3")
	n, err := h.svc.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n < 1 {
		t.Fatalf("Reconcile resolved %d entries, want >= 1", n)
	}

	log = h.applyLog(t, cs.ID)
	for _, nt := range log.NodeTimers {
		if nt.Status != change.NodeTimerStatusRolledBack {
			t.Errorf("post-reconcile ApplyLog.NodeTimers[%s].Status = %s, want rolled_back", nt.Node, nt.Status)
		}
	}
	// The changeset's own terminal status never changes retroactively —
	// only the per-node detail is completed.
	if got := h.get(t, cs.ID).Status; got != change.StatusRolledBack {
		t.Errorf("status after reconcile = %s, want rolled_back (unchanged)", got)
	}

	// A second Reconcile pass finds nothing left pending — every entry is
	// already resolved.
	n, err = h.svc.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if n != 0 {
		t.Errorf("second Reconcile resolved %d entries, want 0 (nothing left pending)", n)
	}
}

// TestService_Reconcile_NilTimers covers the disabled-protocol fast path:
// a Service built without Config.Timers (the T-205-only single-coordinator
// fallback) must not scan or panic — Reconcile is simply a no-op.
func TestService_Reconcile_NilTimers(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	n, err := h.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n != 0 {
		t.Errorf("Reconcile with no Timers configured resolved %d entries, want 0", n)
	}
}

// TestThreeDaemon_PeerDiesBeforeSteps_AbortRollsBackCompleted is T-304's
// AC3: pve3's stage step fails outright (the "this peer is dead" seam —
// pvemock has no synthetic host-unreachable flag for a step that hasn't
// even reached the network yet, so this uses the same seam-level fault
// injection T-205's own AtEachPosition test established; see also the
// property test below, which additionally covers a true peer.ErrPeerUnreachable
// path via partitionableTransport). pve1/pve2 (earlier in plan order)
// already committed and must be rolled back; pve3 must never have been
// touched at all.
func TestThreeDaemon_PeerDiesBeforeSteps_AbortRollsBackCompleted(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()
	pre1 := mustReadNode(t, h, "pve1")
	pre2 := mustReadNode(t, h, "pve2")
	pre3 := mustReadNode(t, h, "pve3")

	h.peers["pve3"].agent.setFailStage("pve3", true)

	cs := h.mustCreate(t, threeNodeOps())
	_, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second)
	if err == nil {
		t.Fatal("Apply should have failed (pve3's stage step)")
	}

	got := h.get(t, cs.ID)
	if got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if h.committed("pve1") != pre1 {
		t.Errorf("pve1 (completed before the dead peer) not rolled back")
	}
	if h.committed("pve2") != pre2 {
		t.Errorf("pve2 (completed before the dead peer) not rolled back")
	}
	if h.committed("pve3") != pre3 {
		t.Errorf("pve3 (the dead peer, never reached its own reload) changed — should stay untouched")
	}

	// No timer was ever armed for pve3 (arming only happens right before
	// the reload step, which pve3 never reached).
	if _, err := h.peers["pve3"].repo.Get(ctx, cs.ID, "pve3"); err != store.ErrNotFound {
		t.Errorf("pve3 node_timers row = %v, want ErrNotFound (never armed)", err)
	}
}

// TestThreeDaemon_CoordinatorDies_PeersStillRollBackLocally is T-304's AC4:
// once pve2 and pve3 have armed their own local timers, the coordinator
// (pve1) is treated as gone for the rest of the test — no Confirm, no
// Rollback, no firing of the coordinator's own bookkeeping timer. Each
// peer's independently-armed local timer still fires and self-restores.
func TestThreeDaemon_CoordinatorDies_PeersStillRollBackLocally(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()
	pre2 := mustReadNode(t, h, "pve2")
	pre3 := mustReadNode(t, h, "pve3")

	cs := h.mustCreate(t, threeNodeOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The coordinator "dies" here: nothing further is done through h.svc.
	h.fireDeadline(t, "pve2")
	h.fireDeadline(t, "pve3")

	if h.committed("pve2") != pre2 {
		t.Errorf("pve2 did not self-restore with no coordinator involvement")
	}
	if h.committed("pve3") != pre3 {
		t.Errorf("pve3 did not self-restore with no coordinator involvement")
	}
	if row := h.nodeTimer(t, cs.ID, "pve2"); row.Status != store.NodeTimerRolledBack {
		t.Errorf("pve2 node_timers status = %s, want rolled_back", row.Status)
	}
	if row := h.nodeTimer(t, cs.ID, "pve3"); row.Status != store.NodeTimerRolledBack {
		t.Errorf("pve3 node_timers status = %s, want rolled_back", row.Status)
	}

	// The coordinator's own changeset row is stuck in awaiting_confirm
	// forever in this scenario (it really is dead) — that's expected and
	// is exactly what ArmPendingRollbacks (T-205's restart-survival
	// property, unchanged by T-304) resolves once the coordinator itself
	// comes back and re-arms from its own persisted confirm_deadline.
	if got := h.get(t, cs.ID).Status; got != change.StatusAwaitingConfirm {
		t.Fatalf("coordinator's own changeset status = %s, want awaiting_confirm (coordinator is 'dead', hasn't rolled its own record back yet)", got)
	}
}

// TestThreeDaemon_PeerRestart_ReArmsFromDB strengthens AC4: a peer daemon's
// own local timer must survive *that node's* restart too (not just the
// coordinator's), the same restart-survival property T-205 established for
// the coordinator, now proven for a node with no changesets table of its
// own at all — node_timers is self-sufficient.
func TestThreeDaemon_PeerRestart_ReArmsFromDB(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()
	pre2 := mustReadNode(t, h, "pve2")

	cs := h.mustCreate(t, threeNodeOps())
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// pve2's daemon "restarts": a brand-new LocalTimerAgent over the same
	// DB, with a fresh (empty) in-process timer map and a fresh fakeTimers
	// double — nothing carried over except what's in node_timers.
	h.advance(200 * time.Second) // well past the deadline
	freshTimers := &fakeTimers{}
	fresh := change.NewLocalTimerAgent(change.LocalTimerConfig{
		Nodes: h.peers["pve2"].agent, Repo: h.peers["pve2"].repo, TimerFunc: freshTimers.New,
		Now: h.clock,
	})
	if err := fresh.ArmPendingOnStartup(ctx); err != nil {
		t.Fatalf("ArmPendingOnStartup: %v", err)
	}
	// The deadline was already in the past, so ArmPendingOnStartup arms a
	// zero-duration timer — fire it to run the (synchronous, in this test's
	// fake) callback.
	freshTimers.fireLatest(t)

	if h.committed("pve2") != pre2 {
		t.Fatalf("pve2 did not self-restore after simulated restart past its deadline")
	}
	row := h.nodeTimer(t, cs.ID, "pve2")
	if row.Status != store.NodeTimerRolledBack {
		t.Errorf("pve2 node_timers status = %s, want rolled_back", row.Status)
	}
}

// TestThreeDaemon_Property_EveryNodeConvergesNeverMixed is T-304's AC5: a
// property, checked across a battery of injected failure points, that every
// node ends up at exactly one of its two valid states — pre-apply content or
// the newly-applied content — never anything else (a torn/mixed state).
//
// Two families of failure points are covered: mid-apply (a step fails
// before the changeset ever reaches awaiting_confirm — every touched node
// must land back on pre-apply content) and post-apply (the changeset
// reached awaiting_confirm; confirm, timeout, or a partition decide each
// node's fate independently — every node must land on exactly one of its
// two valid contents, even when different nodes land on different ones).
func TestThreeDaemon_Property_EveryNodeConvergesNeverMixed(t *testing.T) {
	nodes := []string{"pve1", "pve2", "pve3"}

	t.Run("mid_apply_failure", func(t *testing.T) {
		// Plan order for threeNodeOps() is pve1, pve2, pve3, each a
		// stage+reload pair: positions 0..5.
		type failure struct {
			inject func(t *testing.T, h *threeDaemonHarness)
			name   string
		}
		failures := []failure{
			{name: "stage_pve1", inject: func(t *testing.T, h *threeDaemonHarness) { h.coordAgent.setFailStage("pve1", true) }},
			{name: "reload_pve1", inject: func(t *testing.T, h *threeDaemonHarness) { h.setReloadFail(t, "pve1", true) }},
			{name: "stage_pve2", inject: func(t *testing.T, h *threeDaemonHarness) { h.peers["pve2"].agent.setFailStage("pve2", true) }},
			{name: "reload_pve2", inject: func(t *testing.T, h *threeDaemonHarness) { h.setReloadFail(t, "pve2", true) }},
			{name: "stage_pve3", inject: func(t *testing.T, h *threeDaemonHarness) { h.peers["pve3"].agent.setFailStage("pve3", true) }},
			{name: "reload_pve3", inject: func(t *testing.T, h *threeDaemonHarness) { h.setReloadFail(t, "pve3", true) }},
			{name: "pve3_unreachable_at_reload", inject: func(t *testing.T, h *threeDaemonHarness) { h.cutPeer("pve3") }},
		}
		for _, f := range failures {
			f := f
			t.Run(f.name, func(t *testing.T) {
				h := newThreeDaemonHarness(t)
				ctx := context.Background()
				pre := map[string]string{}
				for _, n := range nodes {
					pre[n] = mustReadNode(t, h, n)
				}

				f.inject(t, h)

				cs := h.mustCreate(t, threeNodeOps())
				if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second); err == nil {
					t.Fatalf("apply should have failed (%s)", f.name)
				}
				if got := h.get(t, cs.ID).Status; got != change.StatusFailed {
					t.Fatalf("status = %s, want failed", got)
				}

				for _, n := range nodes {
					got := h.committed(n)
					if got != pre[n] {
						t.Errorf("node %s = %q, want pre-apply content %q (mid-apply failure must fully roll back)", n, got, pre[n])
					}
				}
			})
		}
	})

	t.Run("post_apply", func(t *testing.T) {
		cases := []struct {
			action func(t *testing.T, h *threeDaemonHarness, cs change.Changeset)
			name   string
		}{
			{
				name: "confirm_all_reachable",
				action: func(t *testing.T, h *threeDaemonHarness, cs change.Changeset) {
					if _, err := h.svc.Confirm(context.Background(), cs.ID, "root@pam"); err != nil {
						t.Fatalf("Confirm: %v", err)
					}
				},
			},
			{
				name: "timeout_all_reachable",
				action: func(t *testing.T, h *threeDaemonHarness, cs change.Changeset) {
					h.svcTimers.fireLatest(t)
				},
			},
			{
				name: "timeout_one_partitioned_then_local_timer_fires",
				action: func(t *testing.T, h *threeDaemonHarness, cs change.Changeset) {
					h.cutPeer("pve3")
					h.svcTimers.fireLatest(t)
					h.fireDeadline(t, "pve3")
				},
			},
			{
				name: "confirm_but_one_node_uncancellable",
				action: func(t *testing.T, h *threeDaemonHarness, cs change.Changeset) {
					// A known, documented edge case (see T-304's report): if
					// the cancellation fan-out can't reach a node, that
					// node's own timer is still armed and will roll IT back
					// even though the changeset committed. The per-node
					// property must still hold for that node individually.
					h.cutPeer("pve3")
					if _, err := h.svc.Confirm(context.Background(), cs.ID, "root@pam"); err != nil {
						t.Fatalf("Confirm: %v", err)
					}
					h.fireDeadline(t, "pve3")
				},
			},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				h := newThreeDaemonHarness(t)
				ctx := context.Background()
				pre := map[string]string{}
				for _, n := range nodes {
					pre[n] = mustReadNode(t, h, n)
				}

				cs := h.mustCreate(t, threeNodeOps())
				if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 120*time.Second); err != nil {
					t.Fatalf("Apply: %v", err)
				}
				newContent := map[string]string{}
				for _, n := range nodes {
					newContent[n] = h.committed(n)
				}

				tc.action(t, h, cs)

				for _, n := range nodes {
					got := h.committed(n)
					if got != pre[n] && got != newContent[n] {
						t.Fatalf("node %s ended up in neither its pre-apply nor its new content — a mixed/torn state (%s): got %q", n, tc.name, got)
					}
				}
			})
		}
	})
}
