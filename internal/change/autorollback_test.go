package change_test

// autorollback_test.go is T-2603's acceptance suite: a NEW error finding on an
// entity a changeset touched rolls that changeset back inside its
// commit-confirm window, and nothing else does.
//
// A NOTE ON THE NEGATIVE ASSERTIONS. Six of this card's seven criteria are
// "and this does NOT roll anything back". An assertion like that passes just
// as happily when the guard was never armed, when the finding never reached
// the engine, when the changeset was never applied, or when the whole trigger
// is broken. Every negative case here is therefore paired with a CONTROL LEG
// that changes exactly ONE thing — the severity, the entity, the flag, the
// timing — and asserts the SAME harness does roll back. A zero is only ever
// reported as evidence once we have shown the counter can be non-zero.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- helpers --------------------------------------------------------------

// guardedNodes are the two nodes every changeset in this file touches;
// outsideNode is the third node of the same fixture, deliberately left
// untouched so a finding on it is genuinely outside the changeset's Impact.
var guardedNodes = []string{"pve1", "pve2"}

const outsideNode = "pve3"

// guardedOps touches pve1 and pve2 and nothing else.
func guardedOps() []change.Op {
	return []change.Op{
		bridgeCreateOp("pve1", "vmbr91", nil),
		bridgeCreateOp("pve2", "vmbr91", nil),
	}
}

func boolPtr(b bool) *bool { return &b }

// errorFindingOn builds a new error-severity finding attributed to one node
// through both of the attribution keys the engine understands: the node list
// and a ref on that node.
func errorFindingOn(node, id string) change.ObservedFinding {
	return change.ObservedFinding{
		ID: id, Check: "bridge_no_carrier", Severity: "error",
		Detail: "vmbr91 on " + node + " has no carrier",
		Nodes:  []string{node}, Refs: []string{"bridge:" + node + ":vmbr91"},
	}
}

// warningFindingOn is errorFindingOn at warning severity — the only
// difference, so a test pairing the two isolates severity alone.
func warningFindingOn(node, id string) change.ObservedFinding {
	f := errorFindingOn(node, id)
	f.Severity = "warning"
	return f
}

// applyGuarded applies ops with the finding-triggered rollback explicitly
// armed, after observing one (caller-supplied) pre-apply findings cycle — the
// baseline every "was this finding already there?" decision is made against.
func applyGuarded(t *testing.T, h *applyHarness, ops []change.Op, preApply []change.ObservedFinding) change.Changeset {
	t.Helper()
	return applyWithOptions(t, h, ops, preApply, change.ApplyOptions{AutoRollbackOnError: boolPtr(true)})
}

func applyWithOptions(t *testing.T, h *applyHarness, ops []change.Op, preApply []change.ObservedFinding, opts change.ApplyOptions) change.Changeset {
	t.Helper()
	ctx := context.Background()
	cs := h.mustCreate(t, "brian", "guarded", ops)
	// The pre-apply cycle. It is fed BEFORE the apply on purpose: that is
	// what makes "pre-existing" a property of the recorded baseline rather
	// than of how fast the test runs.
	h.svc.ObserveFindings(ctx, preApply)
	applied, err := h.svc.ApplyWithOptions(ctx, cs.ID, "brian", nil, 120*time.Second, change.ApplyStrategy{}, opts)
	if err != nil {
		t.Fatalf("ApplyWithOptions: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %q, want %q", applied.Status, change.StatusAwaitingConfirm)
	}
	return applied
}

// auditDetail returns the decoded detail of the first entry matching
// action/result, so a test can assert on what the audit trail actually says
// rather than only that an entry exists.
func auditDetail(t *testing.T, h *applyHarness, id, action, result string) map[string]any {
	t.Helper()
	for _, e := range changesetAudit(t, h, id) {
		if e.Action != action || e.Result != result {
			continue
		}
		if !e.DetailJSON.Valid {
			return map[string]any{}
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(e.DetailJSON.String), &out); err != nil {
			t.Fatalf("decoding %s/%s audit detail: %v", action, result, err)
		}
		return out
	}
	t.Fatalf("no %s/%s audit entry for changeset %s", action, result, id)
	return nil
}

func hasAuditAction(entries []store.AuditEntry, action string) bool {
	for _, e := range entries {
		if e.Action == action {
			return true
		}
	}
	return false
}

// --- AC1 ------------------------------------------------------------------

// TestAutoRollback_NewErrorFindingOnATouchedEntityRollsBack is acceptance
// criterion 1: the rollback happens, the node is restored, and the audit
// entry names the finding's STABLE ID — not a description of it.
func TestAutoRollback_NewErrorFindingOnATouchedEntityRollsBack(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	pre := map[string]string{}
	for _, node := range guardedNodes {
		pre[node] = preApplyFile(t, h, node)
	}

	cs := applyGuarded(t, h, guardedOps(), nil)
	for _, node := range guardedNodes {
		if h.agent.committedFile(node) == pre[node] {
			t.Fatalf("%s's committed file is unchanged after the apply; there would be nothing for a rollback to restore", node)
		}
	}

	// "Enabling it is audited."
	armed := auditDetail(t, h, cs.ID, "changeset.auto_rollback", "armed")
	if armed["source"] != "request" {
		t.Errorf("armed audit source = %v, want \"request\"", armed["source"])
	}

	const findingID = "health:bridge_no_carrier|bridge:pve1:vmbr91"
	h.svc.ObserveFindings(ctx, []change.ObservedFinding{errorFindingOn("pve1", findingID)})

	got := h.get(t, cs.ID)
	if got.Status != change.StatusRolledBack {
		t.Fatalf("status = %q, want %q — a new error finding on a touched entity must roll the changeset back", got.Status, change.StatusRolledBack)
	}
	for _, node := range guardedNodes {
		if h.agent.committedFile(node) != pre[node] {
			t.Errorf("%s was not restored to its pre-apply file", node)
		}
	}

	detail := auditDetail(t, h, cs.ID, "changeset.auto_rollback", "finding_triggered")
	if detail["findingId"] != findingID {
		t.Errorf("audit findingId = %v, want %q — the operator must be told WHICH finding rolled their change back", detail["findingId"], findingID)
	}
	if detail["check"] != "bridge_no_carrier" {
		t.Errorf("audit check = %v, want the finding's check name", detail["check"])
	}

	// ...and the same evidence is on the changeset itself, not only in the
	// audit log, so a UI reading the changeset can explain the rollback.
	trigger, ok, err := h.svc.AutoRollbackTriggerFor(ctx, cs.ID)
	if err != nil || !ok {
		t.Fatalf("AutoRollbackTriggerFor = (%+v, %v, %v), want the recorded trigger", trigger, ok, err)
	}
	if trigger.FindingID != findingID || len(trigger.Findings) != 1 {
		t.Errorf("recorded trigger = %+v, want the single finding %q", trigger, findingID)
	}
	if trigger.Detail == "" {
		t.Error("the recorded trigger carries no detail; \"something went wrong\" is exactly what this card forbids")
	}
}

// TestAutoRollback_TriggerIsAttributedToTheSystemAndNamesEveryMatch pins the
// two remaining reporting properties: the rollback is attributed to
// system:rollback (not to whoever happened to apply), and a cycle carrying
// several qualifying findings names all of them rather than only the one it
// reports.
func TestAutoRollback_TriggerIsAttributedToTheSystemAndNamesEveryMatch(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := applyGuarded(t, h, guardedOps(), nil)

	h.svc.ObserveFindings(ctx, []change.ObservedFinding{
		errorFindingOn("pve2", "health:b|bridge:pve2:vmbr91"),
		errorFindingOn("pve1", "health:a|bridge:pve1:vmbr91"),
	})

	trigger, ok, err := h.svc.AutoRollbackTriggerFor(ctx, cs.ID)
	if err != nil || !ok {
		t.Fatalf("AutoRollbackTriggerFor = (%+v, %v, %v)", trigger, ok, err)
	}
	if trigger.FindingID != "health:a|bridge:pve1:vmbr91" {
		t.Errorf("reported finding = %q, want the lowest stable ID (the stream's own canonical order)", trigger.FindingID)
	}
	if fmt.Sprint(trigger.Findings) != "[health:a|bridge:pve1:vmbr91 health:b|bridge:pve2:vmbr91]" {
		t.Errorf("recorded findings = %v, want both matches named", trigger.Findings)
	}
	log := h.applyLog(t, cs.ID)
	if log.RolledBackBy != "system:rollback" {
		t.Errorf("rolledBackBy = %q, want system:rollback — an unattended rollback is not the applying user's action", log.RolledBackBy)
	}
}

// --- AC2 ------------------------------------------------------------------

// TestAutoRollback_PreExistingFindingNeverTriggers is acceptance criterion 2,
// proven by SEEDING the finding in the pre-apply cycle rather than by timing:
// both legs feed the identical finding to the identical harness in the
// identical cycle position after the apply, and differ only in whether that
// finding was also present in the cycle BEFORE the apply.
func TestAutoRollback_PreExistingFindingNeverTriggers(t *testing.T) {
	const findingID = "health:bridge_no_carrier|bridge:pve1:vmbr91"
	finding := errorFindingOn("pve1", findingID)

	tests := []struct {
		name       string
		wantStatus change.Status
		preApply   []change.ObservedFinding
	}{
		{
			// The control leg. Same finding, same post-apply cycle, but it was
			// NOT there before the apply — so it must roll back. If this leg
			// ever stops rolling back, the leg below proves nothing.
			name:       "control: a finding absent from the pre-apply cycle does trigger",
			preApply:   nil,
			wantStatus: change.StatusRolledBack,
		},
		{
			name:       "a finding seeded in the pre-apply cycle does not trigger",
			preApply:   []change.ObservedFinding{finding},
			wantStatus: change.StatusAwaitingConfirm,
		},
		{
			// A pre-existing finding stays pre-existing even when the cycle
			// around it changes: it is the ID set recorded at apply time that
			// decides, not "was it in the immediately previous cycle".
			name: "a pre-existing finding stays pre-existing alongside other churn",
			preApply: []change.ObservedFinding{
				finding,
				warningFindingOn("pve2", "health:noise|bridge:pve2:vmbr91"),
			},
			wantStatus: change.StatusAwaitingConfirm,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, fixtureThreeNode)
			preFile := preApplyFile(t, h, "pve1")
			cs := applyGuarded(t, h, guardedOps(), tc.preApply)

			h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

			if got := h.get(t, cs.ID); got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			restored := h.agent.committedFile("pve1") == preFile
			if want := tc.wantStatus == change.StatusRolledBack; restored != want {
				t.Errorf("pve1 restored = %v, want %v", restored, want)
			}
			if tc.wantStatus == change.StatusAwaitingConfirm &&
				hasAuditResult(changesetAudit(t, h, cs.ID), "changeset.auto_rollback", "finding_triggered") {
				t.Error("a pre-existing finding produced a finding_triggered audit entry")
			}
		})
	}
}

// TestAutoRollback_NoObservedBaselineNeverTriggers is rule 4: a guard armed by
// a daemon that has never seen a findings cycle adopts the first cycle it does
// see as its baseline. Everything looking new because nothing was watching
// before is not evidence that the apply broke anything.
//
// The control is the second cycle: a genuinely new finding, after the baseline
// has been adopted, does roll back — so the first cycle's silence is not the
// silence of a broken guard.
func TestAutoRollback_NoObservedBaselineNeverTriggers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := h.mustCreate(t, "brian", "guarded", guardedOps())

	// Deliberately NO ObserveFindings call before the apply.
	if _, err := h.svc.ApplyWithOptions(ctx, cs.ID, "brian", nil, 120*time.Second,
		change.ApplyStrategy{}, change.ApplyOptions{AutoRollbackOnError: boolPtr(true)}); err != nil {
		t.Fatalf("ApplyWithOptions: %v", err)
	}
	if armed := auditDetail(t, h, cs.ID, "changeset.auto_rollback", "armed"); armed["baselineObserved"] != false {
		t.Errorf("armed audit baselineObserved = %v, want false — the audit must say the guard started without a baseline", armed["baselineObserved"])
	}

	first := errorFindingOn("pve1", "health:first|bridge:pve1:vmbr91")
	h.svc.ObserveFindings(ctx, []change.ObservedFinding{first})
	if got := h.get(t, cs.ID); got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after the baseline-adopting cycle = %q, want %q — the first cycle a guard sees cannot trigger it", got.Status, change.StatusAwaitingConfirm)
	}

	// Control: the guard is alive and does fire on something genuinely new.
	h.svc.ObserveFindings(ctx, []change.ObservedFinding{first, errorFindingOn("pve2", "health:second|bridge:pve2:vmbr91")})
	if got := h.get(t, cs.ID); got.Status != change.StatusRolledBack {
		t.Fatalf("control: status = %q, want %q — a finding new relative to the adopted baseline must trigger, "+
			"or the assertion above proves only that the guard is broken", got.Status, change.StatusRolledBack)
	}
	trigger, _, _ := h.svc.AutoRollbackTriggerFor(ctx, cs.ID)
	if trigger.FindingID != "health:second|bridge:pve2:vmbr91" {
		t.Errorf("triggered on %q, want the finding that was new relative to the adopted baseline", trigger.FindingID)
	}
}

// --- AC3 ------------------------------------------------------------------

// TestAutoRollback_FindingOutsideImpactNeverTriggers is acceptance criterion
// 3. Both legs use the same changeset (touching pve1 and pve2), the same
// severity and the same check — they differ only in WHICH node/ref the finding
// names.
func TestAutoRollback_FindingOutsideImpactNeverTriggers(t *testing.T) {
	tests := []struct {
		name       string
		wantStatus change.Status
		finding    change.ObservedFinding
	}{
		{
			name:       "control: an error finding on a touched node triggers",
			finding:    errorFindingOn("pve1", "health:x|bridge:pve1:vmbr91"),
			wantStatus: change.StatusRolledBack,
		},
		{
			name:       "an error finding on a node outside Impact does not trigger",
			finding:    errorFindingOn(outsideNode, "health:x|bridge:pve3:vmbr91"),
			wantStatus: change.StatusAwaitingConfirm,
		},
		{
			// A ref on an untouched node, with no node list at all — the
			// ref-only attribution path's negative case.
			name: "an error finding whose only ref is outside Impact does not trigger",
			finding: change.ObservedFinding{
				ID: "health:y|bridge:pve3:vmbr0", Check: "bridge_no_carrier", Severity: "error",
				Refs: []string{"bridge:pve3:vmbr0"},
			},
			wantStatus: change.StatusAwaitingConfirm,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, fixtureThreeNode)
			cs := applyGuarded(t, h, guardedOps(), nil)

			h.svc.ObserveFindings(ctx, []change.ObservedFinding{tc.finding})

			if got := h.get(t, cs.ID); got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (finding nodes=%v refs=%v)", got.Status, tc.wantStatus, tc.finding.Nodes, tc.finding.Refs)
			}
		})
	}
}

// --- AC4 ------------------------------------------------------------------

// TestAutoRollback_WarningNeverTriggers is acceptance criterion 4: a warning
// never triggers, at any position inside or outside Impact. The control leg is
// the identical finding at error severity on the identical entity.
func TestAutoRollback_WarningNeverTriggers(t *testing.T) {
	tests := []struct {
		name       string
		wantStatus change.Status
		findings   []change.ObservedFinding
	}{
		{
			name:       "control: the same finding at error severity, inside Impact, triggers",
			findings:   []change.ObservedFinding{errorFindingOn("pve1", "health:w|bridge:pve1:vmbr91")},
			wantStatus: change.StatusRolledBack,
		},
		{
			name:       "a warning inside Impact does not trigger",
			findings:   []change.ObservedFinding{warningFindingOn("pve1", "health:w|bridge:pve1:vmbr91")},
			wantStatus: change.StatusAwaitingConfirm,
		},
		{
			name:       "a warning outside Impact does not trigger",
			findings:   []change.ObservedFinding{warningFindingOn(outsideNode, "health:w|bridge:pve3:vmbr91")},
			wantStatus: change.StatusAwaitingConfirm,
		},
		{
			name: "an info finding inside Impact does not trigger",
			findings: []change.ObservedFinding{func() change.ObservedFinding {
				f := errorFindingOn("pve1", "health:i|bridge:pve1:vmbr91")
				f.Severity = "info"
				return f
			}()},
			wantStatus: change.StatusAwaitingConfirm,
		},
		{
			// Several warnings at once are still not an error. vnprox has no
			// `critical` severity — error|warning|info is the whole vocabulary
			// — so there is no rank above error to escalate into either.
			name: "warnings on every touched node do not add up to a trigger",
			findings: []change.ObservedFinding{
				warningFindingOn("pve1", "health:w1|bridge:pve1:vmbr91"),
				warningFindingOn("pve2", "health:w2|bridge:pve2:vmbr91"),
			},
			wantStatus: change.StatusAwaitingConfirm,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, fixtureThreeNode)
			cs := applyGuarded(t, h, guardedOps(), nil)

			h.svc.ObserveFindings(ctx, tc.findings)

			if got := h.get(t, cs.ID); got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
		})
	}
}

// --- AC5 ------------------------------------------------------------------

// TestAutoRollback_FlagOffChangesNothing is acceptance criterion 5: with the
// flag off, none of the above triggers anything and the existing confirm
// behaviour is unchanged.
//
// The final leg is the control: the SAME finding, the SAME harness, the SAME
// changeset shape, flag ON — proving the silence above is the flag's doing.
func TestAutoRollback_FlagOffChangesNothing(t *testing.T) {
	finding := errorFindingOn("pve1", "health:off|bridge:pve1:vmbr91")

	tests := []struct {
		opts           change.ApplyOptions
		name           string
		clusterDefault bool
		wantRolledBack bool
	}{
		{
			name: "the default (no option, no cluster default) is off",
			opts: change.ApplyOptions{},
		},
		{
			name: "an explicit false is off",
			opts: change.ApplyOptions{AutoRollbackOnError: boolPtr(false)},
		},
		{
			name:           "an explicit false overrides a cluster default of on",
			opts:           change.ApplyOptions{AutoRollbackOnError: boolPtr(false)},
			clusterDefault: true,
		},
		{
			name:           "control: the cluster default alone arms it",
			opts:           change.ApplyOptions{},
			clusterDefault: true,
			wantRolledBack: true,
		},
		{
			name:           "control: an explicit true arms it",
			opts:           change.ApplyOptions{AutoRollbackOnError: boolPtr(true)},
			wantRolledBack: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, fixtureThreeNode, func(cfg *change.Config) { cfg.AutoRollbackOnError = tc.clusterDefault })
			preFile := preApplyFile(t, h, "pve1")
			cs := applyWithOptions(t, h, guardedOps(), nil, tc.opts)

			h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

			got := h.get(t, cs.ID)
			rolledBack := got.Status == change.StatusRolledBack
			if rolledBack != tc.wantRolledBack {
				t.Fatalf("rolled back = %v (status %q), want %v", rolledBack, got.Status, tc.wantRolledBack)
			}
			if tc.wantRolledBack {
				return
			}

			// With the guard off nothing about the window may differ from a
			// pre-T-2603 apply: still awaiting confirmation, still restorable
			// by confirming, and no auto-rollback bookkeeping anywhere.
			if got.ConfirmDeadline == nil {
				t.Error("the confirm window's deadline went missing on an unguarded apply")
			}
			if h.agent.committedFile("pve1") == preFile {
				t.Error("pve1's file was restored despite the guard being off")
			}
			if hasAuditAction(changesetAudit(t, h, cs.ID), "changeset.auto_rollback") {
				t.Error("an unguarded apply produced a changeset.auto_rollback audit entry")
			}
			if _, ok, _ := h.svc.AutoRollbackTriggerFor(ctx, cs.ID); ok {
				t.Error("an unguarded apply recorded an auto-rollback trigger on the changeset")
			}
			confirmed, err := h.svc.Confirm(ctx, cs.ID, "brian")
			if err != nil || confirmed.Status != change.StatusCommitted {
				t.Errorf("Confirm after an unguarded apply = (%q, %v), want committed — the existing confirm behaviour must be unchanged", confirmed.Status, err)
			}
		})
	}
}

// --- AC6 ------------------------------------------------------------------

// TestAutoRollback_FindingAfterTheWindowClosedNeverTriggers is acceptance
// criterion 6. The control leg is the identical finding delivered while the
// window is still open.
func TestAutoRollback_FindingAfterTheWindowClosedNeverTriggers(t *testing.T) {
	finding := errorFindingOn("pve1", "health:late|bridge:pve1:vmbr91")

	t.Run("control: the same finding inside the window rolls back", func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, fixtureThreeNode)
		cs := applyGuarded(t, h, guardedOps(), nil)

		h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

		if got := h.get(t, cs.ID); got.Status != change.StatusRolledBack {
			t.Fatalf("control: status = %q, want %q — if this leg stops rolling back, the leg below proves nothing", got.Status, change.StatusRolledBack)
		}
	})

	t.Run("a finding after confirm does not roll an already-committed changeset back", func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, fixtureThreeNode)
		preFile := preApplyFile(t, h, "pve1")
		cs := applyGuarded(t, h, guardedOps(), nil)

		confirmed, err := h.svc.Confirm(ctx, cs.ID, "brian")
		if err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		if confirmed.Status != change.StatusCommitted {
			t.Fatalf("status after confirm = %q, want %q", confirmed.Status, change.StatusCommitted)
		}
		applied := h.agent.committedFile("pve1")

		h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

		got := h.get(t, cs.ID)
		if got.Status != change.StatusCommitted {
			t.Errorf("status = %q, want %q — the window is closed; a finding arriving now is not a rollback trigger", got.Status, change.StatusCommitted)
		}
		if h.agent.committedFile("pve1") != applied || applied == preFile {
			t.Error("pve1's file changed after the window closed")
		}
		if hasAuditResult(changesetAudit(t, h, cs.ID), "changeset.auto_rollback", "finding_triggered") {
			t.Error("a finding after confirm produced a finding_triggered audit entry")
		}
	})

	t.Run("a finding after a manual rollback does not roll back twice", func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, fixtureThreeNode)
		cs := applyGuarded(t, h, guardedOps(), nil)
		if _, err := h.svc.Rollback(ctx, cs.ID, "brian", nil); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		before := len(h.agent.callsFor("pve1"))

		h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

		if delta := len(h.agent.callsFor("pve1")) - before; delta != 0 {
			t.Errorf("a finding after a manual rollback made %d further call(s) on pve1", delta)
		}
		if _, ok, _ := h.svc.AutoRollbackTriggerFor(ctx, cs.ID); ok {
			t.Error("a finding after a manual rollback recorded an auto-rollback trigger")
		}
	})
}

// --- AC7 ------------------------------------------------------------------

// TestAutoRollback_DuringACanaryHoldAbortsTheSequence is acceptance criterion
// 7: the T-2602 interaction. A trigger during a canary hold ABORTS the staged
// sequence — restoring only the stages that ran — rather than rolling back a
// plan half of which was never applied.
//
// The control leg rolls an ALL-at-once apply of the same changeset back through
// the same route and asserts the pending nodes ARE contacted by it, so the
// canary leg's zero-contact assertion is a real constraint rather than a
// rollback path that contacts nothing.
func TestAutoRollback_DuringACanaryHoldAbortsTheSequence(t *testing.T) {
	finding := errorFindingOn(canaryNode, "health:canary|bridge:pve1:vmbr90")

	t.Run("control: rolling back an all-at-once apply does contact the other nodes", func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, fixtureThreeNode)
		cs := applyWithOptions(t, h, canaryOps(), nil, change.ApplyOptions{AutoRollbackOnError: boolPtr(true)})
		before := map[string]int{}
		for _, node := range restNodes {
			before[node] = len(h.agent.callsFor(node))
		}

		h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

		if got := h.get(t, cs.ID); got.Status != change.StatusRolledBack {
			t.Fatalf("control: status = %q, want %q", got.Status, change.StatusRolledBack)
		}
		for _, node := range restNodes {
			if len(h.agent.callsFor(node))-before[node] == 0 {
				t.Fatalf("control: the finding-triggered rollback of an ordinary apply contacted %s zero times — "+
					"the rollback path therefore contacts nothing on these nodes, and the canary leg's "+
					"zero-contact assertion below would prove nothing", node)
			}
		}
	})

	t.Run("a trigger during the hold restores only the applied stages", func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, fixtureThreeNode)
		preCanary := preApplyFile(t, h, canaryNode)
		cs := h.mustCreate(t, "brian", "canary", canaryOps())

		h.svc.ObserveFindings(ctx, nil) // the pre-apply cycle: nothing firing
		held, err := h.svc.ApplyWithOptions(ctx, cs.ID, "brian", nil, 120*time.Second,
			canaryStrategy(change.ApplyGateManual, 30), change.ApplyOptions{AutoRollbackOnError: boolPtr(true)})
		if err != nil {
			t.Fatalf("ApplyWithOptions (canary): %v", err)
		}
		if held.Status != change.StatusApplying {
			t.Fatalf("status = %q, want %q (a paused staged apply)", held.Status, change.StatusApplying)
		}
		if _, paused, _ := h.svc.StagedApplyState(ctx, cs.ID); !paused {
			t.Fatal("the sequence is not paused; there is no canary hold for the trigger to interrupt")
		}
		if h.agent.committedFile(canaryNode) == preCanary {
			t.Fatal("the canary node's file is unchanged; there would be nothing for the abort to restore")
		}

		before := map[string]int{}
		for _, node := range append([]string{canaryNode}, restNodes...) {
			before[node] = len(h.agent.callsFor(node))
		}

		h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

		got := h.get(t, cs.ID)
		if got.Status != change.StatusRolledBack {
			t.Fatalf("status = %q, want %q — a trigger during a hold must resolve the sequence, not leave it paused", got.Status, change.StatusRolledBack)
		}
		if h.agent.committedFile(canaryNode) != preCanary {
			t.Error("the canary node was not restored")
		}
		if delta := len(h.agent.callsFor(canaryNode)) - before[canaryNode]; delta == 0 {
			t.Error("the abort contacted the canary node zero times, so it cannot have restored it")
		}
		for _, node := range restNodes {
			if delta := len(h.agent.callsFor(node)) - before[node]; delta != 0 {
				t.Errorf("the abort made %d call(s) on %s (%v) — a node the sequence never reached must never be restored",
					delta, node, h.agent.callsFor(node)[before[node]:])
			}
			if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
				t.Errorf("%s was written to (%v) at some point in a sequence that was aborted during its canary hold", node, writes)
			}
		}
		if _, ok := stageRow(t, h, cs.ID); ok {
			t.Error("the staged-apply row must be cleared once the trigger has aborted the sequence")
		}

		// The abort is routed through AbortStagedApply, so it is audited as an
		// abort naming both node lists — not as an ordinary rollback.
		abort := auditDetail(t, h, cs.ID, "changeset.abort", "rolled_back")
		if reason, _ := abort["reason"].(string); !strings.Contains(reason, finding.ID) {
			t.Errorf("abort reason = %q, want it to name the finding %q", reason, finding.ID)
		}
		if fmt.Sprint(abort["restoredNodes"]) != "[pve1]" || fmt.Sprint(abort["untouchedNodes"]) != "[pve2 pve3]" {
			t.Errorf("abort audit nodes = restored %v / untouched %v, want [pve1] / [pve2 pve3]",
				abort["restoredNodes"], abort["untouchedNodes"])
		}
		if detail := auditDetail(t, h, cs.ID, "changeset.auto_rollback", "finding_triggered"); detail["stagedApply"] != true {
			t.Errorf("finding_triggered audit stagedApply = %v, want true", detail["stagedApply"])
		}
	})
}

// --- concurrency / single-resolution --------------------------------------

// TestAutoRollback_OnlyOneCycleResolvesAChangeset is rule 5: a second cycle
// carrying the same qualifying finding must not start a second rollback. The
// control is the first cycle, which does roll back.
func TestAutoRollback_OnlyOneCycleResolvesAChangeset(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := applyGuarded(t, h, guardedOps(), nil)
	finding := errorFindingOn("pve1", "health:once|bridge:pve1:vmbr91")

	h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})
	if got := h.get(t, cs.ID); got.Status != change.StatusRolledBack {
		t.Fatalf("status after the first cycle = %q, want %q", got.Status, change.StatusRolledBack)
	}
	after := len(h.agent.callsFor("pve1"))

	h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})
	h.svc.ObserveFindings(ctx, []change.ObservedFinding{finding})

	if delta := len(h.agent.callsFor("pve1")) - after; delta != 0 {
		t.Errorf("two further cycles made %d call(s) on pve1; exactly one callback may resolve a changeset", delta)
	}
	var triggered int
	for _, e := range changesetAudit(t, h, cs.ID) {
		if e.Action == "changeset.auto_rollback" && e.Result == "finding_triggered" {
			triggered++
		}
	}
	if triggered != 1 {
		t.Errorf("%d finding_triggered audit entries, want exactly 1", triggered)
	}
}

// TestAutoRollback_ObserveFindingsIsInertWithNothingArmed proves the seam is
// safe to call from the very first findings cycle of a fresh daemon, before
// anything has ever been applied.
func TestAutoRollback_ObserveFindingsIsInertWithNothingArmed(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	h.svc.ObserveFindings(ctx, []change.ObservedFinding{errorFindingOn("pve1", "health:none|bridge:pve1:vmbr91")})
	if calls := h.agent.writeCallsFor("pve1"); len(calls) != 0 {
		t.Errorf("a findings cycle with nothing armed wrote to pve1 (%v)", calls)
	}
}
