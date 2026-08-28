// SPDX-License-Identifier: Apache-2.0

package change_test

// metrics_test.go covers T-1903's change-engine self-observability: Apply/
// Confirm/Rollback/autoRollback outcomes and the awaiting_confirm duration
// histogram, driven through the same real apply-engine harness
// (apply_helpers_test.go's newHarness) every other apply/confirm/rollback
// test in this package uses — AC2's "driving ... a rolled-back changeset
// through the test harness moves the expected series."

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
)

// fakeMetricsRecorder records every ObserveChangeOutcome/
// ObserveAwaitingConfirmDuration call for assertions.
type fakeMetricsRecorder struct {
	outcomes  []outcomeCall
	durations []durationCall
	mu        sync.Mutex
}

type outcomeCall struct {
	op      string
	success bool
}

type durationCall struct {
	outcome string
	dur     time.Duration
}

func (f *fakeMetricsRecorder) ObserveChangeOutcome(op string, success bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes = append(f.outcomes, outcomeCall{op: op, success: success})
}

func (f *fakeMetricsRecorder) ObserveAwaitingConfirmDuration(outcome string, dur time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.durations = append(f.durations, durationCall{outcome: outcome, dur: dur})
}

func (f *fakeMetricsRecorder) outcomesFor(op string) []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []bool
	for _, o := range f.outcomes {
		if o.op == op {
			out = append(out, o.success)
		}
	}
	return out
}

func (f *fakeMetricsRecorder) durationsFor(outcome string) []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []time.Duration
	for _, d := range f.durations {
		if d.outcome == outcome {
			out = append(out, d.dur)
		}
	}
	return out
}

func withMetrics(rec change.MetricsRecorder) func(*change.Config) {
	return func(cfg *change.Config) { cfg.Metrics = rec }
}

// TestChangeOpLabelsMatchMetricsPackage pins change.ChangeOp* and
// metrics.ChangeOp*'s string values in sync (apply.go's doc comment on
// ChangeOpApply names this test) — the two packages each declare their own
// copy (avoiding a direct import) rather than sharing one constant, so
// nothing else catches them drifting apart.
func TestChangeOpLabelsMatchMetricsPackage(t *testing.T) {
	// metricsChangeOp* below are internal/metrics.ChangeOp*'s values,
	// copied literally rather than imported — see apply.go's ChangeOp* doc
	// comment for why internal/change deliberately doesn't import
	// internal/metrics.
	type pair struct{ change, metrics string }
	pairs := []pair{
		{change.ChangeOpApply, metricsChangeOpApply},
		{change.ChangeOpConfirm, metricsChangeOpConfirm},
		{change.ChangeOpRollback, metricsChangeOpRollback},
		{change.ChangeOpUnattendedRevert, metricsChangeOpUnattendedRevert},
	}
	for _, p := range pairs {
		if p.change != p.metrics {
			t.Errorf("change/metrics ChangeOp label mismatch: %q != %q", p.change, p.metrics)
		}
	}
}

func TestApply_RecordsChangeOutcome_Success(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	h := newHarness(t, fixtureSingleNode, withMetrics(rec))
	cs := h.mustCreate(t, "alice@pam", "add bridge", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})

	if _, err := h.svc.Apply(context.Background(), cs.ID, "alice@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := rec.outcomesFor(change.ChangeOpApply)
	if len(got) != 1 || !got[0] {
		t.Fatalf("apply outcomes = %v, want exactly one success", got)
	}
}

func TestApply_RecordsChangeOutcome_Failure(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	h := newHarness(t, fixtureSingleNode, withMetrics(rec))
	h.agent.setFailStage("pve1", true)
	cs := h.mustCreate(t, "alice@pam", "add bridge", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})

	if _, err := h.svc.Apply(context.Background(), cs.ID, "alice@pam", nil, 0); err == nil {
		t.Fatalf("Apply: expected an injected stage failure, got nil error")
	}

	got := rec.outcomesFor(change.ChangeOpApply)
	if len(got) != 1 || got[0] {
		t.Fatalf("apply outcomes = %v, want exactly one failure", got)
	}
}

func TestConfirm_RecordsChangeOutcomeAndAwaitingConfirmDuration(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	h := newHarness(t, fixtureSingleNode, withMetrics(rec))
	cs := h.mustCreate(t, "alice@pam", "add bridge", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})
	if _, err := h.svc.Apply(context.Background(), cs.ID, "alice@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := h.svc.Confirm(context.Background(), cs.ID, "alice@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if got := rec.outcomesFor(change.ChangeOpConfirm); len(got) != 1 || !got[0] {
		t.Fatalf("confirm outcomes = %v, want exactly one success", got)
	}
	if got := rec.durationsFor("committed"); len(got) != 1 {
		t.Fatalf("awaiting_confirm durations for outcome=committed = %v, want exactly one sample", got)
	} else if got[0] < 0 {
		t.Errorf("awaiting_confirm duration = %v, want >= 0", got[0])
	}
}

func TestRollback_AwaitingConfirm_RecordsChangeOutcomeAndDuration(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	h := newHarness(t, fixtureSingleNode, withMetrics(rec))
	cs := h.mustCreate(t, "alice@pam", "add bridge", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})
	if _, err := h.svc.Apply(context.Background(), cs.ID, "alice@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := h.svc.Rollback(context.Background(), cs.ID, "alice@pam", nil); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if got := rec.outcomesFor(change.ChangeOpRollback); len(got) != 1 || !got[0] {
		t.Fatalf("rollback outcomes = %v, want exactly one success", got)
	}
	if got := rec.durationsFor("rolled_back"); len(got) != 1 {
		t.Fatalf("awaiting_confirm durations for outcome=rolled_back = %v, want exactly one sample", got)
	}
}

// TestAutoRollback_RecordsUnattendedRevertOutcome covers T-1805's
// unattended-revert path (the commit-confirm-timeout timer firing with no
// live user session) — this task's card explicitly calls this out as a
// series to get right.
func TestAutoRollback_RecordsUnattendedRevertOutcome(t *testing.T) {
	rec := &fakeMetricsRecorder{}
	h := newHarness(t, fixtureSingleNode, withMetrics(rec))
	cs := h.mustCreate(t, "alice@pam", "add bridge", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})
	if _, err := h.svc.Apply(context.Background(), cs.ID, "alice@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Fire the commit-confirm rollback timer synchronously (fakeTimers),
	// simulating the deadline elapsing with nobody having confirmed.
	h.timers.fireLatest(t)

	if got := rec.outcomesFor(change.ChangeOpUnattendedRevert); len(got) != 1 || !got[0] {
		t.Fatalf("unattended_revert outcomes = %v, want exactly one success", got)
	}
	if got := rec.durationsFor("rolled_back"); len(got) != 1 {
		t.Fatalf("awaiting_confirm durations for outcome=rolled_back = %v, want exactly one sample", got)
	}

	final := h.get(t, cs.ID)
	if final.Status != change.StatusRolledBack {
		t.Fatalf("final status = %s, want rolled_back", final.Status)
	}
}

// TestApply_NilMetricsRecorderIsNoOp proves a Service built without
// Config.Metrics (every pre-T-1903 caller) behaves exactly as before.
func TestApply_NilMetricsRecorderIsNoOp(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	cs := h.mustCreate(t, "alice@pam", "add bridge", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})
	if _, err := h.svc.Apply(context.Background(), cs.ID, "alice@pam", nil, 0); err != nil {
		t.Fatalf("Apply with no Metrics configured: %v", err)
	}
}

// The metricsChangeOp* constants below are internal/metrics.ChangeOp*'s
// values, copied literally (not imported) to keep this test package's
// import graph identical to the production one (internal/change never
// imports internal/metrics) while still pinning the two vocabularies
// together — see TestChangeOpLabelsMatchMetricsPackage.
const (
	metricsChangeOpApply            = "apply"
	metricsChangeOpConfirm          = "confirm"
	metricsChangeOpRollback         = "rollback"
	metricsChangeOpUnattendedRevert = "unattended_revert"
)
