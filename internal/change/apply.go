// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// Change-engine op labels for MetricsRecorder.ObserveChangeOutcome
// (docs/features/monitoring.md §9's closed vocabulary for
// vnprox_change_outcomes_total's "op" label) — must match
// internal/metrics.ChangeOp* exactly; duplicated here (rather than
// importing internal/metrics's constants) so this package's dependency on
// that one stays the MetricsRecorder interface seam above, not a second
// concrete import. change_test.go's TestChangeOpLabelsMatchMetricsPackage
// pins the two in sync.
const (
	ChangeOpApply            = "apply"
	ChangeOpConfirm          = "confirm"
	ChangeOpRollback         = "rollback"
	ChangeOpUnattendedRevert = "unattended_revert"
)

// recordChangeOutcome is a nil-safe wrapper around s.metrics.ObserveChangeOutcome.
func (s *Service) recordChangeOutcome(op string, success bool) {
	if s.metrics != nil {
		s.metrics.ObserveChangeOutcome(op, success)
	}
}

// recordAwaitingConfirmDuration is a nil-safe wrapper around
// s.metrics.ObserveAwaitingConfirmDuration, computing the duration from
// enteredAtUnix (the changeset's awaiting_confirm-entry UpdatedAt) to now.
func (s *Service) recordAwaitingConfirmDuration(outcome string, enteredAtUnix int64) {
	if s.metrics == nil {
		return
	}
	dur := time.Duration(s.now().Unix()-enteredAtUnix) * time.Second
	if dur < 0 {
		dur = 0
	}
	s.metrics.ObserveAwaitingConfirmDuration(outcome, dur)
}

// ErrValidationBlocked is returned by Apply when the pre-apply revalidation
// (docs/features/change-management.md §2: validation "runs ... again
// immediately before apply") finds a blocking error — apply refuses and no
// mutation occurs. The API layer surfaces Findings in the same
// validation_failed shape the validate route uses.
type ErrValidationBlocked struct {
	Findings []Finding
}

func (e *ErrValidationBlocked) Error() string {
	return "change: changeset has blocking validation errors; cannot apply"
}

// Apply executes a draft/validated changeset per docs/architecture.md §4:
// acquire the cluster-wide single-applier lock, revalidate, render+persist the
// plan, snapshot the pre-state, run the ordered steps (per-node stage→reload,
// then sdn.apply), and on success arm the commit-confirm rollback timer and
// move to awaiting_confirm. On any step failure it rolls back every completed
// step and moves to failed. It streams status via the WS broadcaster and
// audits every transition.
//
// pveGW carries the user's own PVE ticket for cluster-scope steps (sdn.apply);
// it may be nil for a changeset with no such steps. confirmTimeout of 0 uses
// the Service default; any value is clamped to [Min,Max]ConfirmTimeout.
func (s *Service) Apply(ctx context.Context, id, author string, pveGW PVEGateway, confirmTimeout time.Duration) (Changeset, error) {
	return s.ApplyStaged(ctx, id, author, pveGW, confirmTimeout, ApplyStrategy{})
}

// ApplyStaged is Apply with an explicit T-2602 apply strategy. The ZERO
// strategy is `mode: all`, and Apply above passes exactly that — so the
// all-at-once path below is byte-for-byte the code that ran before this
// card, and every caller that never heard of a strategy is unaffected.
//
// A `mode: canary` strategy runs the named nodes' steps first and then
// PAUSES in a persisted, resumable state (apply_staged.go): the changeset is
// neither applied nor rolled back, the store records which nodes are in
// which state, and the returned Changeset is still `applying` with its
// `applyStage` describing the hold. The sequence then ends via
// ContinueStagedApply, AbortStagedApply, the auto gate, or the
// commit-confirm deadline — never by this call blocking on the hold.
func (s *Service) ApplyStaged(ctx context.Context, id, author string, pveGW PVEGateway, confirmTimeout time.Duration, strategy ApplyStrategy) (Changeset, error) {
	return s.ApplyWithOptions(ctx, id, author, pveGW, confirmTimeout, strategy, ApplyOptions{})
}

// ApplyWithOptions is ApplyStaged plus T-2603's per-apply options
// (autorollback.go's ApplyOptions). The ZERO options value means "no
// finding-triggered rollback unless the cluster default says otherwise", and
// the cluster default is itself off — so ApplyStaged above, and therefore
// Apply, and therefore every caller that predates this card, is unaffected.
func (s *Service) ApplyWithOptions(ctx context.Context, id, author string, pveGW PVEGateway, confirmTimeout time.Duration, strategy ApplyStrategy, opts ApplyOptions) (Changeset, error) {
	ctx = withHostWriteActor(ctx, author) // T-2902
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	if confirmTimeout == 0 {
		confirmTimeout = s.confirmTimeout
	}
	confirmTimeout = clampConfirmTimeout(confirmTimeout)

	cs, plan, strategy, err := s.beginApply(ctx, id, author, pveGW, strategy, confirmTimeout)
	if err != nil {
		return Changeset{}, err
	}

	// The commit-confirm deadline is computed once, up front — every node's
	// local rollback timer (T-304) is armed with this exact same absolute
	// instant as it's reached in plan order, so every node's safety net
	// expires at the same wall-clock time no matter how long earlier nodes'
	// steps took, and it matches the coordinator's own bookkeeping deadline
	// below.
	deadline := s.now().Add(confirmTimeout).Unix()

	// T-2603: arm the finding-triggered rollback guard HERE — after the
	// changeset is committed to applying, and before the pre-state snapshot
	// and every mutation. The findings baseline it captures is therefore
	// genuinely the cycle before the apply, which is what makes "a
	// pre-existing finding never triggers" a property rather than a race.
	// A no-op unless this changeset (or the cluster default) asked for it.
	s.armAutoRollback(ctx, cs, author, opts)

	// --- pre-state snapshot: before any mutation (docs/data-model.md §2).
	// captureSnapshotFull additionally captures SDN config (T-402) and, since
	// T-1805, every touched firewall ruleset's pre-image, in the same snapshot
	// row.
	pre, err := s.captureSnapshotFull(ctx, id, snapshotKindPre, plan, pveGW)
	if err != nil {
		return s.finishFailedApply(ctx, cs, plan, author, &ApplyLog{}, err)
	}

	// --- T-1805 / D1: seal the applying user's PVE ticket for this window,
	// BEFORE the first mutating step. Sealing here rather than at
	// finishAwaitingConfirm means a daemon killed mid-apply
	// (recoverInterruptedApply) also finds the credential, not only one killed
	// mid-window. Never fatal: a changeset whose ticket could not be sealed
	// still applies, but its apply response says plainly that its firewall/SDN
	// portion will not self-revert.
	revert := s.sealRevertTicket(ctx, id, plan, pveGW, deadline)

	// --- execute the plan ---
	//
	// T-2602: a plan is executed as one or more STAGES. `mode: all` — the
	// default and every pre-T-2602 apply — has exactly one stage containing
	// every step in plan order, so this is the same single ex.run(ctx) pass
	// it always was.
	ex := s.newExecutor(cs, plan, pre, pveGW, deadline)
	if !strategy.IsCanary() {
		runErr := ex.run(ctx)
		if runErr != nil {
			return s.finishFailedApply(ctx, cs, plan, author, ex.log, runErr)
		}
		// --- success: arm the commit-confirm window ---
		return s.finishAwaitingConfirm(ctx, cs, plan, author, ex.log, deadline, revert)
	}

	canaryIdx, restIdx := canaryStageIndexes(plan, strategy.CanaryNodes)
	applied, pending := plan.stageNodes(canaryIdx), plan.stageNodes(restIdx)
	// A failure inside the canary stage rolls back the canary nodes only —
	// the remaining nodes were never written to, so there is nothing on them
	// to undo and nothing that justifies contacting them.
	ex.rollbackNodes = applied
	if runErr := ex.runSteps(ctx, canaryIdx); runErr != nil {
		return s.finishFailedApply(ctx, cs, plan, author, ex.log, runErr)
	}
	return s.holdAfterCanary(ctx, cs, plan, author, ex.log, deadline, strategy, applied, pending)
}

// beginApply performs the locked prologue of Apply: acquire the advisory
// single-applier lock, load and revalidate the changeset, build+persist the
// plan, and transition to applying. It returns with the lock held (released
// by whichever terminal transition Apply reaches).
func (s *Service) beginApply(ctx context.Context, id, author string, pveGW PVEGateway, strategy ApplyStrategy, confirmTimeout time.Duration) (Changeset, Plan, ApplyStrategy, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	if s.lockHeldBy != "" {
		held := s.lockHeldBy
		s.appendAudit(ctx, author, "changeset.apply", "locked", id, map[string]any{"heldBy": held})
		return Changeset{}, Plan{}, ApplyStrategy{}, &ErrChangesetLocked{HeldBy: held}
	}

	cs, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, Plan{}, ApplyStrategy{}, err
	}
	if cs.Status != StatusDraft && cs.Status != StatusValidated {
		return Changeset{}, Plan{}, ApplyStrategy{}, &ErrIllegalTransition{From: cs.Status, To: StatusApplying}
	}

	// T-2003: the review-approval gate. This is an AUTHORIZATION check, not
	// a validation one — deliberately run before revalidation/snapshot/any
	// mutation, and deliberately never satisfiable by anything the caller
	// sends on this request: the ONLY way isApproved can return true is a
	// prior, separately-audited changeset.review_approve call that wrote a
	// changeset_approvals row this same server process can read back. A
	// disabled/misconfigured approval store (isApproved's fail-closed
	// contract) refuses apply exactly like an explicit rejection would —
	// never fails open.
	if s.approval.Required {
		approved, aerr := s.isApproved(ctx, id)
		if aerr != nil {
			return Changeset{}, Plan{}, ApplyStrategy{}, aerr
		}
		if !approved {
			s.appendAudit(ctx, author, "changeset.apply", "approval_required", id, nil)
			return Changeset{}, Plan{}, ApplyStrategy{}, &ErrApprovalRequired{ID: id}
		}
	}

	// Revalidate immediately before apply, through safetyOptions() so
	// allow_dangerous_ops and the protected-interface set are honored (T-203
	// deviation note 5a) — a bare Validate would silently drop them.
	//
	// T-2601: validationInputs is the single assembly point for every
	// validator input, including the cluster's policy set — so the
	// pre-apply revalidation cannot end up enforcing a different rule set
	// (or none) than the validate route did. Its policy findings are
	// prepended to the pipeline's, exactly as Service.validate does.
	var policyReport PolicyResult
	safety, policyFindings := s.validationInputs(ctx, cs.ClusterID, cs.ID, cs.Ops, &policyReport)
	findings := append(policyFindings, ValidateWithSafety(cs.Ops, s.inventorySnapshot(), safety)...)
	s.recordPolicyStats(ctx, cs.ClusterID, policyReport)
	cs.Findings = findings
	if hasError(findings) {
		if cs.Status == StatusValidated {
			_ = cs.Transition(StatusDraft, s.now().Unix())
		} else {
			cs.UpdatedAt = s.now().Unix()
		}
		if perr := s.persist(ctx, cs); perr != nil {
			return Changeset{}, Plan{}, ApplyStrategy{}, perr
		}
		s.appendAudit(ctx, author, "changeset.apply", "validation_failed", id, map[string]any{"findingCount": len(findings)})
		s.broadcastStatus(cs)
		return Changeset{}, Plan{}, ApplyStrategy{}, &ErrValidationBlocked{Findings: findings}
	}

	// An apply that proceeds only because allow_dangerous_ops downgraded
	// safety-interlock findings to warnings must leave an apply-time audit
	// entry of its own (T-203's card: "its use audited") — the create/
	// validate-time entries don't prove the flag was still exercised at the
	// moment of apply (audit-phase-2 F-12).
	s.auditSafetyOverride(ctx, author, id, findings)

	// T-2604: the two-person rule on protected op classes. Like the approval
	// gate above this is an AUTHORIZATION check, satisfiable only by rows a
	// prior, separately-audited request wrote — never by anything on this
	// request. It is placed HERE, after revalidation, deliberately: the
	// policy-tagged half of a protected class is read from the policy result
	// the revalidation just produced (policyReport), so the gate and the
	// validator can never be looking at different rule sets, and a
	// referentially-broken changeset is refused by validation before its
	// class is ever computed. Still before BuildPlan, the snapshot, and every
	// mutation, so a refusal leaves the changeset exactly where it was.
	if tpErr := s.enforceTwoPerson(ctx, cs, author, policyReport); tpErr != nil {
		var required *ErrTwoPersonRequired
		if errors.As(tpErr, &required) {
			s.appendAudit(ctx, author, "changeset.apply", "two_person_required", id, map[string]any{
				"class": required.Class, "required": required.Required, "have": required.Have,
				"approvers": required.Approvers, "classes": required.Classes,
			})
		} else {
			s.appendAudit(ctx, author, "changeset.apply", "two_person_undecidable", id, map[string]any{"error": tpErr.Error()})
		}
		return Changeset{}, Plan{}, ApplyStrategy{}, tpErr
	}

	plan, err := BuildPlan(cs.Ops)
	if err != nil {
		s.appendAudit(ctx, author, "changeset.apply", "unsupported_op", id, map[string]any{"error": err.Error()})
		return Changeset{}, Plan{}, ApplyStrategy{}, err
	}

	// T-3101-followup-01: the foreign-SDN-pending "surface and confirm"
	// gate (apply_sdn_foreign.go / planning/tasks/debt-sweep-2026-08-19.md
	// item 2). Placed HERE — after the plan exists (plan.hasSDN() needs
	// it) and before the peer-compatibility gate, the strategy check, the
	// StatusApplying transition, the snapshot, and every mutation — so a
	// refusal leaves the changeset exactly where it was, holding no lock,
	// the same "authorization check, not a validation one" placement the
	// approval and two-person gates above already use. The live PVE read
	// happens here, immediately before this changeset's own SDNStageOp
	// calls would run later in ApplyWithOptions — the timing boundary
	// detectSDNForeignPending's doc comment explains is the only sound one.
	if plan.hasSDN() {
		foreign, ferr := detectSDNForeignPending(ctx, pveGW)
		if ferr != nil {
			s.appendAudit(ctx, author, "changeset.apply", "sdn_foreign_pending_check_failed", id, map[string]any{"error": ferr.Error()})
			return Changeset{}, Plan{}, ApplyStrategy{}, ferr
		}
		covered, cerr := s.isSDNForeignPendingCovered(ctx, id, foreign)
		if cerr != nil {
			s.appendAudit(ctx, author, "changeset.apply", "sdn_foreign_pending_check_failed", id, map[string]any{"error": cerr.Error()})
			return Changeset{}, Plan{}, ApplyStrategy{}, cerr
		}
		if !covered {
			s.appendAudit(ctx, author, "changeset.apply", "sdn_foreign_pending_unacknowledged", id, map[string]any{"entryCount": len(foreign)})
			return Changeset{}, Plan{}, ApplyStrategy{}, &ErrSDNForeignPendingUnacknowledged{ID: id, Entries: foreign}
		}
	}

	// Peer-version compatibility gate (docs/architecture.md §5: "a daemon
	// refuses to coordinate changes involving a peer with an incompatible
	// schema version"), checked before any snapshot/mutation so an
	// incompatible peer never results in a partial apply. Only
	// NodeAgents that opt into PeerCompatibilityChecker are asked
	// (production's ClusterNodeAgent does; single-node test doubles
	// typically don't, and are treated as "every node compatible").
	//
	// Deliberately narrow to peer.ErrPeerIncompatible: a peer that is merely
	// unreachable right now (down, partitioned) is a different, already-
	// handled failure mode — the existing per-step apply/rollback machinery
	// (apply_exec.go/apply_distributed.go) tolerates and recovers from a
	// node going unreachable mid-apply, and pre-emptively refusing the
	// whole apply on a transient reachability blip here would make applies
	// needlessly fragile. Only a confirmed version mismatch blocks up
	// front; every other CheckNodeCompatible error is intentionally
	// ignored at this pre-flight stage.
	if checker, ok := s.nodes.(PeerCompatibilityChecker); ok {
		for _, node := range plan.affectedNodes() {
			cerr := checker.CheckNodeCompatible(ctx, node)
			if cerr == nil || !errors.Is(cerr, peer.ErrPeerIncompatible) {
				continue
			}
			incompatible := &ErrIncompatiblePeer{Node: node, Err: cerr}
			s.appendAudit(ctx, author, "changeset.apply", "peer_incompatible", id, map[string]any{"node": node, "error": cerr.Error()})
			return Changeset{}, Plan{}, ApplyStrategy{}, incompatible
		}
	}

	// T-2602: the apply strategy is validated against the plan it will run,
	// HERE — after the plan exists (a canary node list can only be checked
	// against the nodes the plan actually affects, AC7) and before the
	// changeset transitions to applying or anything is snapshotted or
	// mutated. A refused strategy therefore leaves the changeset exactly
	// where it was, holding no lock.
	strategy, serr := s.validateApplyStrategy(strategy, plan, confirmTimeout)
	if serr != nil {
		s.appendAudit(ctx, author, "changeset.apply", "invalid_apply_strategy", id, map[string]any{"error": serr.Error()})
		return Changeset{}, Plan{}, ApplyStrategy{}, serr
	}

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Changeset{}, Plan{}, ApplyStrategy{}, fmt.Errorf("change: marshaling plan for changeset %s: %w", id, err)
	}
	cs.Plan = planJSON
	cs.ApplyLog = nil
	if err := cs.Transition(StatusApplying, s.now().Unix()); err != nil {
		return Changeset{}, Plan{}, ApplyStrategy{}, err
	}
	if err := s.persist(ctx, cs); err != nil {
		return Changeset{}, Plan{}, ApplyStrategy{}, err
	}
	s.lockHeldBy = id
	s.appendAudit(ctx, author, "changeset.apply", "applying", id, map[string]any{"stepCount": len(plan.Steps)})
	s.broadcastStatus(cs)
	return cs, plan, strategy, nil
}

// finishFailedApply records a failed apply: persist the apply log, move to
// failed, release the lock, audit, and refresh inventory. The completed steps
// were already rolled back by the executor.
func (s *Service) finishFailedApply(ctx context.Context, cs Changeset, plan Plan, author string, log *ApplyLog, cause error) (Changeset, error) {
	// T-2603: a failed apply never enters a confirm window, so there is
	// nothing left for a finding to roll back.
	s.disarmAutoRollback(cs.ID)
	s.applyMu.Lock()
	logJSON, _ := json.Marshal(log)
	cs.ApplyLog = logJSON
	_ = cs.Transition(StatusFailed, s.now().Unix())
	cs.ConfirmDeadline = nil
	_ = s.persist(ctx, cs)
	// T-1805: a failed apply never enters the commit-confirm window, so the
	// ticket sealed at the top of Apply has nothing left to authorize — wipe
	// it here, unconditionally, alongside the terminal transition.
	s.wipeRevertTicket(ctx, cs.ID)
	cs.RevertTicketExpiresAt = 0
	s.lockHeldBy = ""
	s.applyMu.Unlock()

	s.recordChangeOutcome(ChangeOpApply, false)
	s.broadcastStatus(cs)
	detail := map[string]any{"error": cause.Error()}
	if log.FailedStep != nil {
		detail["failedStep"] = *log.FailedStep
	}
	s.appendAudit(ctx, author, "changeset.apply", "failed", cs.ID, detail)
	s.refreshAfterTerminal(ctx, plan)
	return cs, cause
}

// finishAwaitingConfirm records a successful apply: persist the apply log,
// move to awaiting_confirm with a persisted confirm deadline, arm the
// daemon-side rollback timer, audit, and broadcast (keeping the apply lock
// held for the duration of the window). deadline is the same value every
// affected node's local timer (T-304) was already armed with during
// execution (apply_exec.go's execStep).
func (s *Service) finishAwaitingConfirm(ctx context.Context, cs Changeset, plan Plan, author string, log *ApplyLog, deadline int64, revert UnattendedRevert) (Changeset, error) {
	s.applyMu.Lock()
	if s.nodeTimers != nil {
		if len(log.NodeTimers) == 0 {
			for _, node := range plan.affectedNodes() {
				log.NodeTimers = append(log.NodeTimers, NodeTimerLog{Node: node, Status: NodeTimerStatusArmed, Deadline: deadline})
			}
		} else {
			// T-2602: a staged sequence already recorded the canary stage's
			// node timers at the hold. Merge rather than append, so promoting
			// the rest does not produce a duplicate entry per canary node.
			var armed []NodeTimerLog
			for _, node := range plan.affectedNodes() {
				armed = append(armed, NodeTimerLog{Node: node, Status: NodeTimerStatusArmed, Deadline: deadline})
			}
			log.NodeTimers = mergeNodeTimerLogs(log.NodeTimers, armed)
		}
	}
	logJSON, _ := json.Marshal(log)
	cs.ApplyLog = logJSON
	if err := cs.Transition(StatusAwaitingConfirm, s.now().Unix()); err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	cs.ConfirmDeadline = &deadline
	if err := s.persist(ctx, cs); err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	s.armTimerLocked(cs.ID, deadline)
	s.applyMu.Unlock()

	// T-1805: report unattended-revert coverage on the apply response
	// (docs/api.md's changesets section). Computed, never persisted, and
	// carrying no credential material — only whether one was sealed and the
	// instant its coverage lapses.
	r := revert
	cs.UnattendedRevert = &r

	s.recordChangeOutcome(ChangeOpApply, true)
	s.broadcastStatus(cs)
	detail := map[string]any{"confirmDeadline": deadline}
	if r.Required {
		// The audit trail records the *coverage decision*, never the
		// credential: whether a sealed ticket exists and until when it helps.
		detail["unattendedRevertAvailable"] = r.Available
		detail["unattendedRevertCoversUntil"] = r.CoversUntil
		detail["unattendedRevertFullWindow"] = r.FullWindow
	}
	s.appendAudit(ctx, author, "changeset.apply", "awaiting_confirm", cs.ID, detail)
	if r.Required && !r.FullWindow {
		s.log.Warn("change: unattended revert does not cover the whole confirm window",
			"changeset_id", cs.ID, "covers_until", r.CoversUntil, "confirm_deadline", deadline, "reason", r.Reason)
	}
	return cs, nil
}

// Confirm commits a changeset within its commit-confirm window
// (docs/api.md: POST /changesets/{id}/confirm). It cancels the rollback timer,
// moves awaiting_confirm→committed, releases the apply lock, captures a
// post-state snapshot, and refreshes inventory. It returns *ErrNotConfirmable
// if the changeset is not currently awaiting confirmation.
func (s *Service) Confirm(ctx context.Context, id, author string) (Changeset, error) {
	ctx = withHostWriteActor(ctx, author) // T-2902
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	s.applyMu.Lock()
	cs, err := s.Get(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	if cs.Status != StatusAwaitingConfirm {
		s.applyMu.Unlock()
		return Changeset{}, &ErrNotConfirmable{ID: id, Status: cs.Status}
	}
	// T-2603 / AC6: confirming closes the window. A finding arriving after
	// this instant must not roll an already-committed changeset back, so the
	// guard is dropped inside the same lock hold as the transition — and only
	// once the confirm is known to be going ahead, so a refused confirm never
	// silently disarms a changeset that is still in its window.
	s.disarmAutoRollback(id)
	// The changeset's current UpdatedAt is its awaiting_confirm-entry
	// timestamp (finishAwaitingConfirm stamped it there and nothing has
	// transitioned it since) — captured before Transition below overwrites
	// it, for vnprox_change_awaiting_confirm_seconds.
	enteredAt := cs.UpdatedAt
	s.cancelTimerLocked(id)
	if err := cs.Transition(StatusCommitted, s.now().Unix()); err != nil {
		s.applyMu.Unlock()
		s.recordChangeOutcome(ChangeOpConfirm, false)
		return Changeset{}, err
	}
	cs.ConfirmDeadline = nil
	if err := s.persist(ctx, cs); err != nil {
		s.applyMu.Unlock()
		s.recordChangeOutcome(ChangeOpConfirm, false)
		return Changeset{}, err
	}
	// T-1805: confirm ends the commit-confirm window, so the sealed revert
	// ticket has nothing left to authorize. Wiped inside the same lock hold as
	// the status transition, before the lock is released and before any of the
	// best-effort post-confirm work below — a wipe is not best-effort.
	s.wipeRevertTicket(ctx, id)
	cs.RevertTicketExpiresAt = 0
	s.lockHeldBy = ""
	plan := decodePlan(cs.Plan)
	log := decodeApplyLog(cs.ApplyLog)
	s.applyMu.Unlock()

	s.recordChangeOutcome(ChangeOpConfirm, true)
	s.recordAwaitingConfirmDuration("committed", enteredAt)
	s.broadcastStatus(cs)
	s.appendAudit(ctx, author, "changeset.confirm", "committed", id, nil)

	// T-304: fan out cancellation to every node's local rollback timer — a
	// confirmed changeset must not have some node still counting down to a
	// self-restore behind the coordinator's back (docs/features/
	// change-management.md §4: "confirm fans out cancellations"). This is
	// best-effort per node: a node this call can't reach right now keeps its
	// timer armed and may roll back a change the user already confirmed —
	// a known, documented edge case (see T-304's report) — surfaced here as
	// NodeTimerStatusUnknown for Reconcile to notice and audit.
	if s.nodeTimers != nil && len(plan.affectedNodes()) > 0 {
		results := s.cancelNodeTimers(ctx, id, plan.affectedNodes())
		log.NodeTimers = mergeNodeTimerLogs(log.NodeTimers, results)
		if err := s.updateApplyLog(ctx, id, log); err != nil {
			s.log.Warn("change: persisting post-confirm node-timer cancellation log", "changeset_id", id, "error", err)
		}
	}

	if _, err := s.captureSnapshot(ctx, id, snapshotKindPost, plan.affectedNodes()); err != nil {
		s.log.Warn("change: capturing post-commit snapshot", "changeset_id", id, "error", err)
	}
	s.refreshAfterTerminal(ctx, plan)
	return cs, nil
}

// Rollback handles docs/api.md's POST /changesets/{id}/rollback for both
// documented cases:
//
//   - awaiting_confirm: an immediate manual rollback of the in-window change —
//     restore the pre-apply files, reload, move to rolled_back (attributed to
//     author), release the lock.
//   - committed (within the rollback window — *ErrRollbackWindowExpired
//     beyond it, audit phase-2 F-10): create a NEW restoring draft whose diff
//     is the inverse (docs/features/change-management.md §4), leaving the
//     committed changeset untouched. The returned Changeset is that draft.
//
// Any other status returns *ErrNotConfirmable.
//
// pveGW carries the requesting user's own PVE ticket (T-402), needed only
// when the changeset being rolled back has an SDN portion (plan.hasSDN()) —
// see restoreSDN's doc comment for why that revert has no daemon-level,
// ticket-less path the way node-file rollback does. It may be nil: a
// node-file-only changeset never touches it, and a nil gateway on an
// SDN-carrying changeset degrades to a logged, non-fatal
// "sdn restore skipped" rollback-log entry (doRollbackLocked) rather than
// failing the whole rollback — the node-file half still completes.
func (s *Service) Rollback(ctx context.Context, id, author string, pveGW PVEGateway) (Changeset, error) {
	ctx = withHostWriteActor(ctx, author) // T-2902
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	s.applyMu.Lock()
	cs, err := s.Get(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	switch cs.Status {
	case StatusAwaitingConfirm:
		s.cancelTimerLocked(id)
		plan, rbErr := s.doRollbackLocked(ctx, &cs, author, ChangeOpRollback, pveGW)
		s.applyMu.Unlock()
		s.refreshAfterTerminal(ctx, plan)
		return cs, rbErr
	case StatusApplying:
		// T-2602: rolling back a changeset PAUSED between apply stages is the
		// abort case — restore exactly the stages that ran. An `applying`
		// changeset with no staged pause is a genuine mid-flight apply and
		// still falls through to *ErrNotConfirmable below, unchanged.
		s.applyMu.Unlock()
		if _, paused, serr := s.StagedApplyState(ctx, id); serr == nil && paused {
			return s.abortStagedApply(ctx, id, author, "operator aborted the staged apply during the canary hold", pveGW)
		}
		return Changeset{}, &ErrNotConfirmable{ID: id, Status: cs.Status}
	case StatusCommitted:
		s.applyMu.Unlock()
		// F-10 (audit phase-2): the manual-rollback offer expires after the
		// rollback window (docs/features/change-management.md §4: "offered
		// for 7 days") — beyond it, retention no longer pins the pre-apply
		// snapshot, so a restoring draft may be unbuildable. The changeset's
		// UpdatedAt is its commit time (Transition stamps it).
		windowEnd := cs.UpdatedAt + int64(s.rollbackWindowDays)*24*60*60
		if s.now().Unix() > windowEnd {
			s.appendAudit(ctx, author, "changeset.rollback", "window_expired", cs.ID, map[string]any{"committedAt": cs.UpdatedAt, "windowDays": s.rollbackWindowDays})
			return Changeset{}, &ErrRollbackWindowExpired{ID: cs.ID, CommittedAt: cs.UpdatedAt, WindowDays: s.rollbackWindowDays}
		}
		draft, draftErr := s.createRestoringDraft(ctx, author, cs)
		s.recordChangeOutcome(ChangeOpRollback, draftErr == nil)
		return draft, draftErr
	default:
		s.applyMu.Unlock()
		return Changeset{}, &ErrNotConfirmable{ID: id, Status: cs.Status}
	}
}

// autoRollback is the commit-confirm timer callback: if the changeset is
// still awaiting confirmation when the deadline elapses (connectivity may
// have broken so no confirm arrived), restore the pre-apply state and roll
// back, attributed to system:rollback (docs/features/change-management.md §4).
func (s *Service) autoRollback(ctx context.Context, id string) {
	s.applyMu.Lock()
	delete(s.timers, id)
	// T-1704 single-writer fence: only the current HA lease-term holder may
	// drive this unattended rollback decision. A demoted/fenced former-active
	// whose timer still fires (e.g. a partition that healed after the standby
	// promoted) no-ops here — the changeset stays awaiting_confirm and the
	// true leader's own re-armed timer, keyed to the same absolute deadline,
	// resolves it exactly once. Fail-safe: withholding is always the safe
	// choice (a missed confirm just means the changeset keeps waiting for the
	// leader), never a double-rollback.
	if !s.mayLead() {
		s.applyMu.Unlock()
		s.log.Info("change: auto-rollback skipped — not the current HA leader", "changeset_id", id)
		return
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		s.log.Error("change: auto-rollback: loading changeset", "changeset_id", id, "error", err)
		return
	}
	if cs.Status == StatusApplying {
		// T-2602 AC5: the commit-confirm deadline covers the WHOLE staged
		// sequence. A changeset still paused in a canary hold when it elapses
		// rolls back everything applied so far — the hold does not get to
		// keep the cluster open past the window it was granted.
		s.applyMu.Unlock()
		if _, paused, serr := s.StagedApplyState(ctx, id); serr == nil && paused {
			if _, err := s.abortStagedApply(ctx, id, systemRollbackActor,
				"the commit-confirm window expired while the staged apply was still paused", nil); err != nil {
				s.log.Error("change: auto-rollback of a paused staged apply failed", "changeset_id", id, "error", err)
			}
		}
		return
	}
	if cs.Status != StatusAwaitingConfirm {
		s.applyMu.Unlock()
		return
	}
	// The confirm-timeout timer fires with no live user session at all
	// (docs/features/change-management.md §4: "the rollback timer runs on the
	// node's daemon"), so no PVEGateway is passed here. Since T-1805 that is
	// no longer the end of the story: doRollbackLocked falls back to the
	// ticket sealed at apply time, so a firewall/SDN-carrying changeset's
	// ticket-scoped portion reverts on this path too — the gap T-402 and
	// T-502 both flagged.
	plan, rbErr := s.doRollbackLocked(ctx, &cs, systemRollbackActor, ChangeOpUnattendedRevert, nil)
	s.applyMu.Unlock()
	if rbErr != nil {
		s.log.Error("change: auto-rollback failed", "changeset_id", id, "error", rbErr)
	}
	s.refreshAfterTerminal(ctx, plan)
}

// doRollbackLocked restores an applied (awaiting_confirm) changeset's pre-apply
// file state on every affected node (and, when pveGW is available and the
// plan carries an SDN portion, its SDN config too — T-402's restoreSDN) and
// transitions it to rolled_back (or failed if any node/SDN restore could not
// complete — the "couldn't even fully roll back" case changeset.go's
// StatusFailed doc distinguishes). It releases the apply lock. Caller must
// hold applyMu; it does NOT refresh inventory (the caller does, after
// unlocking).
// op is the change-engine metrics label this call should be attributed to
// (ChangeOpRollback for a manual, interactive rollback; ChangeOpUnattendedRevert
// for the commit-confirm-timeout timer's own call) — see this file's
// ChangeOp* doc comment for why doRollbackLocked's two callers being the
// only two op values that ever reach here makes this the single right
// place to record both the outcome counter and the awaiting_confirm
// duration histogram, regardless of which of doRollbackLocked's several
// return points is taken.
func (s *Service) doRollbackLocked(ctx context.Context, cs *Changeset, actor, op string, pveGW PVEGateway) (retPlan Plan, retErr error) {
	return s.doRollbackScopedLocked(ctx, cs, actor, op, pveGW, nil)
}

// doRollbackScopedLocked is doRollbackLocked restricted to a node set.
//
// restrictNodes nil — every caller that existed before T-2602 — means "the
// whole plan", i.e. exactly what doRollbackLocked always did. A non-nil list
// (T-2602's abort of a paused staged apply) narrows the plan to those nodes'
// steps before anything is restored, so a node the sequence never reached is
// not restored, not reloaded, and not contacted at all. Restricting the PLAN
// rather than filtering inside each restore is deliberate: every downstream
// decision this function makes — which nodes to converge, whether there is
// an SDN/firewall/QoS/WireGuard/switch portion to revert, which nodes to
// refresh afterwards — is derived from the plan, so narrowing it once
// narrows all of them consistently instead of leaving one of them to be
// forgotten.
func (s *Service) doRollbackScopedLocked(ctx context.Context, cs *Changeset, actor, op string, pveGW PVEGateway, restrictNodes []string) (retPlan Plan, retErr error) {
	// cs.UpdatedAt is still its awaiting_confirm-entry timestamp here —
	// nothing has transitioned it since finishAwaitingConfirm — captured
	// before this function's own cs.Transition calls (both the early
	// failure path and the normal one below) overwrite it.
	enteredAt := cs.UpdatedAt
	// T-2603: whatever ends this changeset's window — a manual rollback, the
	// commit-confirm timeout, a staged abort, or a finding trigger itself —
	// ends the guard with it. Dropping it here, on the one function every
	// rollback path funnels through, is why no later cycle can roll a
	// terminal changeset back a second time.
	s.disarmAutoRollback(cs.ID)
	defer func() {
		if s.metrics == nil {
			return
		}
		outcome := "rolled_back"
		if cs.Status == StatusFailed {
			outcome = "failed"
		}
		s.metrics.ObserveChangeOutcome(op, retErr == nil)
		s.recordAwaitingConfirmDuration(outcome, enteredAt)
	}()

	plan := decodePlan(cs.Plan)
	if restrictNodes != nil {
		plan = plan.restrictedToNodes(restrictNodes)
	}
	log := decodeApplyLog(cs.ApplyLog)

	// T-1805 / D1: when the caller has no live PVE credential (the
	// commit-confirm-timeout timer, and crash recovery — both run with no user
	// session at all) but this changeset's revert needs one, unseal the ticket
	// captured at apply time. This is the *only* place a sealed ticket is
	// turned back into a gateway, and only ever for the changeset being
	// reverted. sealedGW is tracked separately so the wipe below runs whether
	// or not it was actually used.
	usedSealedTicket := false
	if pveGW == nil && plan.needsRevertTicket() {
		if gw, ok := s.revertGatewayFor(ctx, cs.ID); ok {
			pveGW = gw
			usedSealedTicket = true
		}
	}

	pre, err := s.loadPreSnapshot(ctx, cs.ID)
	if err != nil {
		s.log.Error("change: rollback: loading pre-snapshot", "changeset_id", cs.ID, "error", err)
		// Without the pre-snapshot we cannot safely restore; mark failed so an
		// operator investigates rather than leaving it awaiting_confirm forever.
		_ = cs.Transition(StatusFailed, s.now().Unix())
		cs.ConfirmDeadline = nil
		_ = s.persist(ctx, *cs)
		s.wipeRevertTicket(ctx, cs.ID)
		cs.RevertTicketExpiresAt = 0
		s.lockHeldBy = ""
		s.broadcastStatus(*cs)
		s.appendAudit(ctx, actor, "changeset.rollback", "error", cs.ID, map[string]any{"error": err.Error()})
		return plan, err
	}

	var rbLogs []RollbackLog
	var anyFailed bool
	if s.nodeTimers != nil {
		var nodeTimers []NodeTimerLog
		rbLogs, nodeTimers, anyFailed = s.restoreAllDistributed(ctx, cs.ID, plan, pre)
		log.NodeTimers = mergeNodeTimerLogs(log.NodeTimers, nodeTimers)
	} else {
		rbLogs, anyFailed = s.restoreAll(ctx, plan, pre)
	}

	if plan.hasSDN() {
		if sdnPre, ok := sdnConfigFromSnapshot(pre); ok {
			sdnLog := s.restoreSDN(ctx, pveGW, sdnPre)
			if sdnLog.Status != StepOK {
				anyFailed = true
			}
			rbLogs = append(rbLogs, sdnLog)
		}
	}

	// T-1805: firewall-ruleset restore. Like SDN (and unlike the node-file,
	// QoS, WireGuard and switch restores) this needs the user's PVE ticket —
	// supplied either by an interactive rollback's own live session or, on the
	// unattended paths, by the ticket sealed at apply time above. Before this
	// card a fw.* changeset reaching this point was simply never reverted, on
	// any path including a manual rollback; that is the hole D1 closes.
	if plan.hasFw() {
		if fwPre, ok := fwStateFromSnapshot(pre); ok {
			fwLogs := s.restoreFwState(ctx, pveGW, fwPre)
			for _, l := range fwLogs {
				if l.Status != StepOK {
					anyFailed = true
				}
			}
			rbLogs = append(rbLogs, fwLogs...)
		}
	}

	// T-1505: QoS shape state restore. Like NodeAgent's interfaces-file
	// restore (and unlike SDN/fw.*), this works on the unattended
	// commit-confirm-timeout / crash-recovery path too — the QosGateway is
	// daemon-level (no user ticket needed) — so a qos.shape.create that
	// times out un-confirmed fully reverts (tc class/filter + qos_shapes
	// row removed).
	if plan.hasQos() {
		if qosPre, ok := qosStateFromSnapshot(pre); ok {
			qosLogs := s.restoreQosState(ctx, qosPre)
			for _, l := range qosLogs {
				if l.Status != StepOK {
					anyFailed = true
				}
			}
			rbLogs = append(rbLogs, qosLogs...)
		}
	}

	// T-1401: WireGuard state restore. Unlike SDN/fw, this works on the
	// unattended commit-confirm-timeout / crash-recovery paths too — the
	// WGGateway is daemon-level (no user ticket needed) — so a wg.tunnel.create
	// that times out un-confirmed fully reverts (tunnel + generated keypair
	// removed, no orphaned key material — AC6).
	if plan.hasWg() {
		if wgPre, ok := wgStateFromSnapshot(pre); ok {
			wgLogs := s.restoreWgState(ctx, wgPre)
			for _, l := range wgLogs {
				if l.Status != StepOK {
					anyFailed = true
				}
			}
			rbLogs = append(rbLogs, wgLogs...)
		}
	}

	// T-1205: switch-port state restore. Like the interfaces-file restore (and
	// unlike SDN/fw), this works on the unattended commit-confirm-timeout /
	// crash-recovery path too — the SwitchGateway is daemon-level (no user
	// ticket needed). If the switch is unreachable at this moment, its restore
	// fails and anyFailed escalates the changeset to a distinguishable
	// "rollback incomplete" state (StatusFailed / result "rollback_incomplete")
	// rather than a silent rolled_back (T-1205 AC6).
	if plan.hasSwitch() {
		if switchPre, ok := switchStateFromSnapshot(pre); ok {
			switchLogs := s.restoreSwitchState(ctx, switchPre)
			for _, l := range switchLogs {
				if l.Status != StepOK {
					anyFailed = true
				}
			}
			rbLogs = append(rbLogs, switchLogs...)
		}
	}

	log.Rollback = append(log.Rollback, rbLogs...)
	log.RolledBackBy = actor
	if logJSON, mErr := json.Marshal(log); mErr == nil {
		cs.ApplyLog = logJSON
	}
	cs.ConfirmDeadline = nil

	target := StatusRolledBack
	result := "rolled_back"
	if anyFailed {
		target = StatusFailed
		result = "rollback_incomplete"
	}
	_ = cs.Transition(target, s.now().Unix())
	persistErr := s.persist(ctx, *cs)
	// T-1805: the changeset has now left awaiting_confirm by this path, so the
	// sealed ticket is wiped — unconditionally, including when the persist
	// above failed. A wipe that only happened on the happy path would be
	// exactly the best-effort behaviour this card forbids.
	s.wipeRevertTicket(ctx, cs.ID)
	cs.RevertTicketExpiresAt = 0
	if persistErr != nil {
		s.lockHeldBy = ""
		return plan, persistErr
	}
	s.lockHeldBy = ""
	s.broadcastStatus(*cs)
	s.appendAudit(ctx, actor, "changeset.rollback", result, cs.ID, map[string]any{
		"nodes": plan.affectedNodes(),
		// Records *that* a sealed credential was used, never anything about
		// its value — so an auditor can tell an unattended firewall/SDN revert
		// from an interactive one.
		"sealedRevertTicketUsed": usedSealedTicket,
	})
	if anyFailed {
		return plan, fmt.Errorf("change: rollback of changeset %s did not fully restore every node", cs.ID)
	}
	return plan, nil
}

// createRestoringDraft builds a new draft that reverses a committed changeset
// (docs/features/change-management.md §4). It goes through the normal Create
// path (validated, audited, broadcast) and links back to the original in an
// audit entry.
func (s *Service) createRestoringDraft(ctx context.Context, author string, orig Changeset) (Changeset, error) {
	ops, err := s.buildRestoringOpsFromSnapshot(ctx, orig)
	if err != nil {
		return Changeset{}, err
	}
	if len(ops) == 0 {
		return Changeset{}, fmt.Errorf("change: changeset %s: live state already matches its pre-apply snapshot; nothing to restore", orig.ID)
	}
	draft, err := s.Create(ctx, author, restoringTitle(orig), ops)
	if err != nil {
		return Changeset{}, err
	}
	s.appendAudit(ctx, author, "changeset.rollback", "restoring_draft_created", orig.ID, map[string]any{"draftId": draft.ID})
	return draft, nil
}

// Diff renders the per-file unified diffs and op summaries for a changeset's
// node-file ops (docs/api.md: GET /changesets/{id}/diff), reusing T-204's
// ifaces.DiffChangeset by adapting the NodeAgent to the host.Reader it needs.
// SDN/guest/fw op contributions are other packages' concern; T-205 renders the
// interface-file half.
func (s *Service) Diff(ctx context.Context, id string) (*ifaces.ChangesetDiff, error) {
	if !s.applyConfigured() {
		return nil, &ErrApplyNotConfigured{}
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// T-2601 acceptance criterion 3: policy evaluation happens BEFORE diff.
	// A changeset a `deny` rule refuses produces no diff at all — this
	// returns before any node file is read, so the diff is never computed
	// rather than computed and discarded. (The rest of the validator
	// pipeline is deliberately NOT re-run here: diff has always been a read
	// over whatever the draft currently says, and only the policy gate is
	// specified to precede it.)
	if blocked := s.policyDenial(ctx, cs); blocked != nil {
		s.log.Info("change: refusing to diff a changeset a policy rule denies", "changeset_id", id)
		return nil, blocked
	}

	fileOps := make([]Op, 0, len(cs.Ops))
	for _, op := range cs.Ops {
		if nodeFileOpTypes[op.Type] {
			fileOps = append(fileOps, op)
		}
	}
	ifOps, err := changeOpsToIfaces(fileOps)
	if err != nil {
		return nil, err
	}
	diff, err := ifaces.DiffChangeset(ctx, nodeAgentReader{s.nodes}, ifOps, id)
	if err != nil {
		return nil, err
	}
	// T-2003: extend the config-diff tab with the SDN portion — operators
	// reason in /etc/pve/sdn/*.cfg terms too, and a changeset mixing
	// node-file and sdn.* ops previously rendered a config diff that
	// silently covered only the interfaces-file half (acceptance criterion
	// 4). Purely additive to ChangesetDiff.Files (more entries, same shape),
	// so this is safe on the frozen changesets.diff MCP tool's response too
	// (docs/architecture.md §13.1) — no field is renamed or removed.
	diff.Files = append(diff.Files, sdnConfigDiffFiles(cs.Ops, s.inventorySnapshot())...)
	return diff, nil
}

// ArmPendingRollbacks re-establishes apply-engine state from the DB at daemon
// startup (docs/development.md: "Rollback timers must survive daemon restart
// ... re-armed on startup"). For every changeset found awaiting_confirm it
// re-acquires the apply lock and re-arms its rollback timer from the persisted
// confirm_deadline — so killing the daemon mid-window and restarting still
// rolls back on schedule. Any changeset left in applying (an apply interrupted
// by the crash) is recovered by restoring its pre-apply snapshot and marking
// it failed.
func (s *Service) ArmPendingRollbacks(ctx context.Context) error {
	if !s.applyConfigured() {
		return nil
	}

	// T-1805: a PVE ticket sealed before the daemon went down may well have
	// expired while it was down. Clear those first, so no dead credential
	// survives a restart and so the re-armed timers below see an accurate
	// picture of what they can still revert.
	s.SweepExpiredRevertTickets(ctx)

	awaiting, err := s.List(ctx, string(StatusAwaitingConfirm))
	if err != nil {
		return fmt.Errorf("change: listing awaiting-confirm changesets on startup: %w", err)
	}
	for _, cs := range awaiting {
		s.applyMu.Lock()
		s.lockHeldBy = cs.ID
		if cs.ConfirmDeadline != nil {
			s.armTimerLocked(cs.ID, *cs.ConfirmDeadline)
			s.log.Info("change: re-armed commit-confirm rollback timer from DB", "changeset_id", cs.ID, "deadline", *cs.ConfirmDeadline)
		}
		s.applyMu.Unlock()
		s.appendAudit(ctx, systemRollbackActor, "changeset.timer_rearm", "armed", cs.ID, nil)
	}

	interrupted, err := s.List(ctx, string(StatusApplying))
	if err != nil {
		return fmt.Errorf("change: listing interrupted applies on startup: %w", err)
	}
	for _, cs := range interrupted {
		// T-2602 AC4: an `applying` changeset that was PAUSED between stages
		// is not an interrupted apply — it is a recorded hold, and it is
		// resolved per the strategy the store remembers (resume the hold, or
		// take its decision now if the deadline has already passed). Only a
		// changeset with no staged pause is the pre-T-2602 "the daemon died
		// mid-apply" case, whose handling is untouched.
		if state, paused, serr := s.StagedApplyState(ctx, cs.ID); serr != nil {
			s.log.Error("change: reading staged-apply state during startup recovery; treating as an interrupted apply", "changeset_id", cs.ID, "error", serr)
			s.recoverInterruptedApply(ctx, cs)
		} else if paused {
			s.recoverStagedApply(ctx, cs, state)
		} else {
			s.recoverInterruptedApply(ctx, cs)
		}
	}
	return nil
}

// recoverInterruptedApply cleans up a changeset the daemon crashed on while it
// was still applying: best-effort restore its pre-apply files and mark it
// failed, so it never lingers in a non-terminal state holding the apply lock.
func (s *Service) recoverInterruptedApply(ctx context.Context, cs Changeset) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	plan := decodePlan(cs.Plan)
	if pre, err := s.loadPreSnapshot(ctx, cs.ID); err == nil {
		if _, anyFailed := s.restoreAll(ctx, plan, pre); anyFailed {
			s.log.Error("change: recovery of interrupted apply did not fully restore", "changeset_id", cs.ID)
		}
		// T-1805: the ticket is sealed *before* the first mutating step, so a
		// daemon killed part-way through an apply — not only one killed during
		// the confirm window — can still revert the firewall/SDN portion of
		// what it had already done.
		if plan.needsRevertTicket() {
			if gw, ok := s.revertGatewayFor(ctx, cs.ID); ok {
				if sdnPre, hasSDN := sdnConfigFromSnapshot(pre); hasSDN {
					if l := s.restoreSDN(ctx, gw, sdnPre); l.Status != StepOK {
						s.log.Error("change: recovery: SDN restore of interrupted apply failed", "changeset_id", cs.ID, "error", l.Error)
					}
				}
				if fwPre, hasFw := fwStateFromSnapshot(pre); hasFw {
					for _, l := range s.restoreFwState(ctx, gw, fwPre) {
						if l.Status != StepOK {
							s.log.Error("change: recovery: firewall restore of interrupted apply failed", "changeset_id", cs.ID, "error", l.Error)
						}
					}
				}
			}
		}
	} else {
		s.log.Warn("change: recovery: no pre-snapshot for interrupted apply", "changeset_id", cs.ID, "error", err)
	}
	_ = cs.Transition(StatusFailed, s.now().Unix())
	cs.ConfirmDeadline = nil
	_ = s.persist(ctx, cs)
	s.wipeRevertTicket(ctx, cs.ID)
	if s.lockHeldBy == cs.ID {
		s.lockHeldBy = ""
	}
	s.broadcastStatus(cs)
	s.appendAudit(ctx, systemRollbackActor, "changeset.recover", "failed", cs.ID, nil)
}

// StopTimers cancels every armed rollback timer, for graceful daemon shutdown.
// It does not change any changeset status: an awaiting_confirm changeset stays
// awaiting_confirm in the DB and its timer is re-armed by ArmPendingRollbacks
// on the next start.
func (s *Service) StopTimers() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	for id, t := range s.timers {
		t.Stop()
		delete(s.timers, id)
	}
}

// --- lock / timer / persistence helpers (applyMu held unless noted) ---

func (s *Service) armTimerLocked(id string, deadlineUnix int64) {
	if t, ok := s.timers[id]; ok {
		t.Stop()
		delete(s.timers, id)
	}
	d := time.Until(time.Unix(deadlineUnix, 0))
	if d < 0 {
		d = 0
	}
	s.timers[id] = s.newTimer(d, func() { s.autoRollback(context.Background(), id) })
}

func (s *Service) cancelTimerLocked(id string) {
	if t, ok := s.timers[id]; ok {
		t.Stop()
		delete(s.timers, id)
	}
}

func (s *Service) persist(ctx context.Context, cs Changeset) error {
	row, err := toStoreRow(cs)
	if err != nil {
		return err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return fmt.Errorf("change: persisting changeset %s: %w", cs.ID, err)
	}
	return nil
}

func (s *Service) refreshAfterTerminal(ctx context.Context, plan Plan) {
	if s.refresher == nil {
		return
	}
	nodes := plan.affectedNodes()
	if len(nodes) == 0 {
		if _, err := s.refresher.RefreshNow(ctx, inventory.Scope{}); err != nil {
			s.log.Warn("change: RefreshNow after terminal state", "error", err)
		}
		return
	}
	for _, node := range nodes {
		if _, err := s.refresher.RefreshNow(ctx, inventory.Scope{Node: node}); err != nil {
			s.log.Warn("change: RefreshNow after terminal state", "node", node, "error", err)
		}
	}
}

func decodePlan(raw json.RawMessage) Plan {
	var p Plan
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	return p
}

func decodeApplyLog(raw json.RawMessage) ApplyLog {
	var a ApplyLog
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &a)
	}
	return a
}

// nodeAgentReader adapts a NodeAgent to the host.Reader ifaces.DiffChangeset
// consumes. DiffChangeset only ever calls InterfacesFile (with
// includePending=false), so the other methods (including T-404's
// FRRBGPSummary/FRREVPNVNI and T-602's Services) are intentionally
// unsupported — they are never reached on the diff path.
type nodeAgentReader struct{ agent NodeAgent }

func (r nodeAgentReader) InterfacesFile(ctx context.Context, node string, _ bool) (string, error) {
	return r.agent.ReadInterfaces(ctx, node)
}

func (r nodeAgentReader) Links(context.Context, string) ([]host.LinkState, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.Links not supported")
}

func (r nodeAgentReader) LLDP(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.LLDP not supported")
}

func (r nodeAgentReader) Stats(context.Context, string) (map[string]host.IfaceStats, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.Stats not supported")
}

func (r nodeAgentReader) FRRBGPSummary(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.FRRBGPSummary not supported")
}

func (r nodeAgentReader) FRREVPNVNI(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.FRREVPNVNI not supported")
}

func (r nodeAgentReader) DHCPLeases(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.DHCPLeases not supported")
}

func (r nodeAgentReader) Services(context.Context, string) (map[string]bool, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.Services not supported")
}

func (r nodeAgentReader) Neighbors(context.Context, string) ([]host.Neighbor, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.Neighbors not supported")
}

func (r nodeAgentReader) CorosyncStatus(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.CorosyncStatus not supported")
}

func (r nodeAgentReader) ContainerInterior(context.Context, string, int) (host.ContainerInteriorRaw, error) {
	return host.ContainerInteriorRaw{}, fmt.Errorf("change: nodeAgentReader.ContainerInterior not supported")
}

func (r nodeAgentReader) ContainerPing(context.Context, string, int, string) (bool, error) {
	return false, fmt.Errorf("change: nodeAgentReader.ContainerPing not supported")
}

func (r nodeAgentReader) Conntrack(context.Context, string) ([]host.ConntrackEntry, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.Conntrack not supported")
}

func (r nodeAgentReader) IPv6RA(context.Context, string) ([]host.IPv6RAObservation, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.IPv6RA not supported")
}

func (r nodeAgentReader) MDB(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.MDB not supported")
}

func (r nodeAgentReader) NftRuleset(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("change: nodeAgentReader.NftRuleset not supported")
}
