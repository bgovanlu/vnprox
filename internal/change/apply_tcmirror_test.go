// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// tcMirrorTestBridges seeds pve1's vmbr0/vmbr99 bridges into the harness's
// InventorySource so the referential validator's source/dest-exists check
// (validate_referential.go's checkTcMirrorIface) has something to resolve
// against — the tc.mirror analogue of qosTestVmbr0.
func tcMirrorTestBridges() []inventory.Entity {
	return []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr99"}, Name: "vmbr99"},
	}
}

// fakeTcMirrorGateway is a store-backed change.TcMirrorGateway double used
// by the tc.mirror lifecycle/rollback/expiry tests (T-4014). It mirrors the
// production gateway's shape custody: a tc.mirror.create/update/delete op
// is persisted to store.TcMirrorSessionRepo (the on-node tc/clsact/mirred
// exec itself is production-only — see cmd/vnproxd's hostTcMirrorGateway —
// this fake only needs to prove the change-engine lifecycle, not real
// kernel state).
type fakeTcMirrorGateway struct {
	repo *store.TcMirrorSessionRepo
	now  func() time.Time
}

func newFakeTcMirrorGateway(db *store.DB, now func() time.Time) *fakeTcMirrorGateway {
	if now == nil {
		now = time.Now
	}
	return &fakeTcMirrorGateway{repo: store.NewTcMirrorSessionRepo(db), now: now}
}

func (g *fakeTcMirrorGateway) ApplyTcMirrorOp(ctx context.Context, op change.Op) error {
	switch p := op.Params.(type) {
	case *change.TcMirrorCreateParams:
		startedAt := g.now().Unix()
		return g.repo.Insert(ctx, store.TcMirrorSession{
			ID: op.Target.ID, Node: op.Target.Node, SourceIface: p.SourceIface, DestIface: p.DestIface,
			MaxMbit: p.MaxMbit, MaxDurationSec: p.MaxDurationSec, Status: store.TcMirrorSessionActive,
			CreatedBy: "root@pam", StartedAt: startedAt, ExpiresAt: startedAt + int64(p.MaxDurationSec),
		})
	case *change.TcMirrorUpdateParams:
		s, err := g.repo.Get(ctx, op.Target.ID)
		if err != nil {
			return err
		}
		if p.MaxDurationSec != nil {
			return g.repo.UpdateDuration(ctx, op.Target.ID, *p.MaxDurationSec, s.StartedAt+int64(*p.MaxDurationSec))
		}
		return nil
	case *change.TcMirrorDeleteParams:
		if _, err := g.repo.Get(ctx, op.Target.ID); err != nil {
			return fmt.Errorf("fakeTcMirrorGateway: deleting %s: %w", op.Target.ID, err)
		}
		return g.repo.Delete(ctx, op.Target.ID)
	default:
		return fmt.Errorf("fakeTcMirrorGateway: unsupported op %s", op.Type)
	}
}

func (g *fakeTcMirrorGateway) SnapshotTcMirror(ctx context.Context, node string) (string, error) {
	sessions, err := g.repo.ActiveByNode(ctx, node)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(sessions)
	return string(b), err
}

func (g *fakeTcMirrorGateway) RestoreTcMirror(ctx context.Context, node, snapshot string) error {
	var want []store.TcMirrorSession
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return err
	}
	wantIDs := map[string]bool{}
	for _, s := range want {
		wantIDs[s.ID] = true
	}
	live, err := g.repo.ActiveByNode(ctx, node)
	if err != nil {
		return err
	}
	for _, s := range live {
		if !wantIDs[s.ID] {
			if err := g.repo.Delete(ctx, s.ID); err != nil {
				return err
			}
		}
	}
	for _, s := range want {
		if _, getErr := g.repo.Get(ctx, s.ID); getErr != nil {
			if err := g.repo.Insert(ctx, s); err != nil {
				return err
			}
		}
	}
	return nil
}

var _ change.TcMirrorGateway = (*fakeTcMirrorGateway)(nil)

func tcMirrorHarnessConfig(h *applyHarness, gw *fakeTcMirrorGateway) change.Config {
	return change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, TcMirror: gw, TcMirrorSessions: gw.repo,
	}
}

// TestApply_TcMirrorLifecycle_CreateConfirm is T-4014 acceptance criterion
// 1's happy path: a tc.mirror.create against three-node-vlan stages,
// validates, diffs, applies, and confirms cleanly, landing the session in
// the TcMirrorGateway's store — the ordinary stage->validate->diff->apply
// ->confirm lifecycle, no second mutation path.
func TestApply_TcMirrorLifecycle_CreateConfirm(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	gw := newFakeTcMirrorGateway(h.db, nil)
	cfg := tcMirrorHarnessConfig(h, gw)
	withInventory(tcMirrorTestBridges()...)(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"tc.mirror.create","target":"tc-mirror:pve1:span1","params":{"sourceIface":"vmbr0","destIface":"vmbr99","maxDurationSec":3600}}]`)
	cs, err := svc.Create(ctx, "root@pam", "mirror span1", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	validated, err := svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, f := range validated.Findings {
		if f.Severity == change.SeverityError {
			t.Fatalf("Validate found a blocking error: %+v", f)
		}
	}

	if _, diffErr := svc.Diff(ctx, cs.ID); diffErr != nil {
		t.Fatalf("Diff: %v", diffErr)
	}

	applied, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", applied.Status)
	}

	sess, err := gw.repo.Get(ctx, "span1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.SourceIface != "vmbr0" || sess.DestIface != "vmbr99" || sess.MaxDurationSec != 3600 {
		t.Fatalf("stored session = %+v, want sourceIface=vmbr0 destIface=vmbr99 maxDurationSec=3600", sess)
	}
	if sess.Status != store.TcMirrorSessionActive {
		t.Fatalf("stored session status = %s, want active", sess.Status)
	}

	confirmed, err := svc.Confirm(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != change.StatusCommitted {
		t.Fatalf("status = %s, want committed", confirmed.Status)
	}
	if _, err := gw.repo.Get(ctx, "span1"); err != nil {
		t.Fatalf("session should survive a committed apply: %v", err)
	}
}

// TestApply_TcMirrorLifecycle_RollbackOnTimeout is T-4014's rollback path:
// a tc.mirror.create that reaches awaiting_confirm and then times out
// un-confirmed is fully reverted on the unattended auto-rollback path (the
// TcMirrorGateway is daemon-level, no user ticket needed — T-205's
// existing inverse-order rollback contract, same as QoS/WG).
func TestApply_TcMirrorLifecycle_RollbackOnTimeout(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	gw := newFakeTcMirrorGateway(h.db, nil)
	cfg := tcMirrorHarnessConfig(h, gw)
	withInventory(tcMirrorTestBridges()...)(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"tc.mirror.create","target":"tc-mirror:pve1:span1","params":{"sourceIface":"vmbr0","destIface":"vmbr99","maxDurationSec":3600}}]`)
	cs, err := svc.Create(ctx, "root@pam", "mirror span1", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	if _, getErr := gw.repo.Get(ctx, "span1"); getErr != nil {
		t.Fatalf("session should exist after apply: %v", getErr)
	}

	// Deadline elapses with no confirm -> auto-rollback.
	h.timers.fireLatest(t)

	got, err := svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusRolledBack {
		t.Fatalf("status = %s, want rolled_back", got.Status)
	}
	if _, getErr := gw.repo.Get(ctx, "span1"); getErr == nil {
		t.Fatal("session should be removed after rollback — no orphaned mirror left duplicating traffic")
	}
}

// TestApply_TcMirrorLifecycle_UpdateAndDelete rounds out the happy path
// with the other two members of the op group: an update (re-arming the
// duration) against the session TestApply_TcMirrorLifecycle_CreateConfirm's
// create leaves behind, then a delete, each its own ordinary changeset.
func TestApply_TcMirrorLifecycle_UpdateAndDelete(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	gw := newFakeTcMirrorGateway(h.db, nil)
	cfg := tcMirrorHarnessConfig(h, gw)
	withInventory(tcMirrorTestBridges()...)(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	createOps := opsFromJSON(t, `[{"op":"tc.mirror.create","target":"tc-mirror:pve1:span1","params":{"sourceIface":"vmbr0","destIface":"vmbr99","maxDurationSec":60}}]`)
	cs1, err := svc.Create(ctx, "root@pam", "create", createOps)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs1.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply create: %v", applyErr)
	}
	if _, confirmErr := svc.Confirm(ctx, cs1.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm create: %v", confirmErr)
	}

	updateOps := opsFromJSON(t, `[{"op":"tc.mirror.update","target":"tc-mirror:pve1:span1","params":{"maxDurationSec":7200}}]`)
	cs2, err := svc.Create(ctx, "root@pam", "update", updateOps)
	if err != nil {
		t.Fatalf("Create update: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs2.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply update: %v", applyErr)
	}
	if _, confirmErr := svc.Confirm(ctx, cs2.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm update: %v", confirmErr)
	}
	sess, err := gw.repo.Get(ctx, "span1")
	if err != nil {
		t.Fatalf("GetSession after update: %v", err)
	}
	if sess.MaxDurationSec != 7200 {
		t.Fatalf("maxDurationSec after update = %d, want 7200", sess.MaxDurationSec)
	}

	deleteOps := opsFromJSON(t, `[{"op":"tc.mirror.delete","target":"tc-mirror:pve1:span1","params":{}}]`)
	cs3, err := svc.Create(ctx, "root@pam", "delete", deleteOps)
	if err != nil {
		t.Fatalf("Create delete: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs3.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply delete: %v", applyErr)
	}
	if _, err := svc.Confirm(ctx, cs3.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm delete: %v", err)
	}
	if _, getErr := gw.repo.Get(ctx, "span1"); getErr == nil {
		t.Fatal("session should be gone after delete")
	}
}

// TestTcMirrorExpiry_TickTearsDownOverdueSession is T-4014's bounding path:
// a confirmed session whose max duration has elapsed is torn down by
// TickTcMirrorSessions — via an ordinary, system-drafted tc.mirror.delete
// changeset (Create->Apply->Confirm), never a bypass of the change engine
// — and the store row (and, in the fake gateway, the "live" tc state) is
// gone afterward.
func TestTcMirrorExpiry_TickTearsDownOverdueSession(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	clock := int64(1_700_000_000)
	now := func() time.Time { return time.Unix(clock, 0) }
	gw := newFakeTcMirrorGateway(h.db, now)
	cfg := tcMirrorHarnessConfig(h, gw)
	cfg.Now = now
	withInventory(tcMirrorTestBridges()...)(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"tc.mirror.create","target":"tc-mirror:pve1:span1","params":{"sourceIface":"vmbr0","destIface":"vmbr99","maxDurationSec":60}}]`)
	cs, err := svc.Create(ctx, "root@pam", "mirror span1", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	if _, confirmErr := svc.Confirm(ctx, cs.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm: %v", confirmErr)
	}
	if _, getErr := gw.repo.Get(ctx, "span1"); getErr != nil {
		t.Fatalf("session should exist after confirm: %v", getErr)
	}

	// Before the deadline: a tick must not touch it.
	svc.TickTcMirrorSessions(ctx)
	if _, getErr := gw.repo.Get(ctx, "span1"); getErr != nil {
		t.Fatalf("session should still exist before its deadline: %v", getErr)
	}

	// Advance the clock past startedAt+maxDurationSec.
	clock += 61
	svc.TickTcMirrorSessions(ctx)

	if _, getErr := gw.repo.Get(ctx, "span1"); getErr == nil {
		t.Fatal("overdue session should be torn down by TickTcMirrorSessions — no unbounded mirror")
	}

	// The teardown is an ordinary, visible changeset — never a silent
	// bypass of the change engine (CLAUDE.md's core invariant).
	list, err := svc.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, c := range list {
		if c.ID == cs.ID {
			continue
		}
		for _, op := range c.Ops {
			if op.Type == change.OpTcMirrorDelete && op.Target.ID == "span1" {
				found = true
				if c.Status != change.StatusCommitted {
					t.Errorf("system-drafted delete changeset status = %s, want committed", c.Status)
				}
			}
		}
	}
	if !found {
		t.Fatal("expiry should have staged, applied, and confirmed an ordinary tc.mirror.delete changeset")
	}
}

// TestTcMirrorExpiry_SurvivesDaemonRestart is T-4014's explicit "what
// happens if the daemon restarts mid-session" answer: a session whose
// deadline passed WHILE THE DAEMON WAS DOWN is caught and torn down the
// moment RunTcMirrorSweep's eager startup tick runs on the new process —
// never left silently duplicating traffic. This test drives that eager
// tick directly (TickTcMirrorSessions, the same call RunTcMirrorSweep
// primes with) rather than actually running the goroutine, matching this
// package's own no-sleeps testing convention.
func TestTcMirrorExpiry_SurvivesDaemonRestart(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	clock := int64(1_700_000_000)
	now := func() time.Time { return time.Unix(clock, 0) }
	gw := newFakeTcMirrorGateway(h.db, now)
	h.cfg = tcMirrorHarnessConfig(h, gw)
	h.cfg.Now = now
	withInventory(tcMirrorTestBridges()...)(&h.cfg)
	h.svc = newService(t, h.cfg)
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"tc.mirror.create","target":"tc-mirror:pve1:span1","params":{"sourceIface":"vmbr0","destIface":"vmbr99","maxDurationSec":60}}]`)
	cs, err := h.svc.Create(ctx, "root@pam", "mirror span1", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, applyErr := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// The deadline passes while the daemon is "down" (no tick runs).
	clock += 3600

	// Daemon restart: a fresh Service over the same store.
	h.restart(t)

	// The eager startup tick RunTcMirrorSweep primes with, run directly.
	h.svc.TickTcMirrorSessions(ctx)

	if _, err := gw.repo.Get(ctx, "span1"); err == nil {
		t.Fatal("a session overdue since before restart must be torn down by the restart's own priming tick — an orphaned mirror silently duplicating traffic is the worst outcome here")
	}
}
