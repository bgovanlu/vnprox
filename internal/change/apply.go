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
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	if confirmTimeout == 0 {
		confirmTimeout = s.confirmTimeout
	}
	confirmTimeout = clampConfirmTimeout(confirmTimeout)

	cs, plan, err := s.beginApply(ctx, id, author)
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

	// --- pre-state snapshot: before any mutation (docs/data-model.md §2) ---
	pre, err := s.captureSnapshot(ctx, id, snapshotKindPre, plan.affectedNodes())
	if err != nil {
		return s.finishFailedApply(ctx, cs, plan, author, &ApplyLog{}, err)
	}

	// --- execute the plan ---
	ex := s.newExecutor(cs, plan, pre, pveGW, deadline)
	runErr := ex.run(ctx)
	if runErr != nil {
		return s.finishFailedApply(ctx, cs, plan, author, ex.log, runErr)
	}

	// --- success: arm the commit-confirm window ---
	return s.finishAwaitingConfirm(ctx, cs, plan, author, ex.log, deadline)
}

// beginApply performs the locked prologue of Apply: acquire the advisory
// single-applier lock, load and revalidate the changeset, build+persist the
// plan, and transition to applying. It returns with the lock held (released
// by whichever terminal transition Apply reaches).
func (s *Service) beginApply(ctx context.Context, id, author string) (Changeset, Plan, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	if s.lockHeldBy != "" {
		held := s.lockHeldBy
		s.appendAudit(ctx, author, "changeset.apply", "locked", id, map[string]any{"heldBy": held})
		return Changeset{}, Plan{}, &ErrChangesetLocked{HeldBy: held}
	}

	cs, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, Plan{}, err
	}
	if cs.Status != StatusDraft && cs.Status != StatusValidated {
		return Changeset{}, Plan{}, &ErrIllegalTransition{From: cs.Status, To: StatusApplying}
	}

	// Revalidate immediately before apply, through safetyOptions() so
	// allow_dangerous_ops and the protected-interface set are honored (T-203
	// deviation note 5a) — a bare Validate would silently drop them.
	findings := ValidateWithSafety(cs.Ops, s.inventorySnapshot(), s.safetyOptions())
	cs.Findings = findings
	if hasError(findings) {
		if cs.Status == StatusValidated {
			_ = cs.Transition(StatusDraft, s.now().Unix())
		} else {
			cs.UpdatedAt = s.now().Unix()
		}
		if perr := s.persist(ctx, cs); perr != nil {
			return Changeset{}, Plan{}, perr
		}
		s.appendAudit(ctx, author, "changeset.apply", "validation_failed", id, map[string]any{"findingCount": len(findings)})
		s.broadcastStatus(cs)
		return Changeset{}, Plan{}, &ErrValidationBlocked{Findings: findings}
	}

	// An apply that proceeds only because allow_dangerous_ops downgraded
	// safety-interlock findings to warnings must leave an apply-time audit
	// entry of its own (T-203's card: "its use audited") — the create/
	// validate-time entries don't prove the flag was still exercised at the
	// moment of apply (audit-phase-2 F-12).
	s.auditSafetyOverride(ctx, author, id, findings)

	plan, err := BuildPlan(cs.Ops)
	if err != nil {
		s.appendAudit(ctx, author, "changeset.apply", "unsupported_op", id, map[string]any{"error": err.Error()})
		return Changeset{}, Plan{}, err
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
			return Changeset{}, Plan{}, incompatible
		}
	}

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Changeset{}, Plan{}, fmt.Errorf("change: marshaling plan for changeset %s: %w", id, err)
	}
	cs.Plan = planJSON
	cs.ApplyLog = nil
	if err := cs.Transition(StatusApplying, s.now().Unix()); err != nil {
		return Changeset{}, Plan{}, err
	}
	if err := s.persist(ctx, cs); err != nil {
		return Changeset{}, Plan{}, err
	}
	s.lockHeldBy = id
	s.appendAudit(ctx, author, "changeset.apply", "applying", id, map[string]any{"stepCount": len(plan.Steps)})
	s.broadcastStatus(cs)
	return cs, plan, nil
}

// finishFailedApply records a failed apply: persist the apply log, move to
// failed, release the lock, audit, and refresh inventory. The completed steps
// were already rolled back by the executor.
func (s *Service) finishFailedApply(ctx context.Context, cs Changeset, plan Plan, author string, log *ApplyLog, cause error) (Changeset, error) {
	s.applyMu.Lock()
	logJSON, _ := json.Marshal(log)
	cs.ApplyLog = logJSON
	_ = cs.Transition(StatusFailed, s.now().Unix())
	cs.ConfirmDeadline = nil
	_ = s.persist(ctx, cs)
	s.lockHeldBy = ""
	s.applyMu.Unlock()

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
func (s *Service) finishAwaitingConfirm(ctx context.Context, cs Changeset, plan Plan, author string, log *ApplyLog, deadline int64) (Changeset, error) {
	s.applyMu.Lock()
	if s.nodeTimers != nil {
		for _, node := range plan.affectedNodes() {
			log.NodeTimers = append(log.NodeTimers, NodeTimerLog{Node: node, Status: NodeTimerStatusArmed, Deadline: deadline})
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

	s.broadcastStatus(cs)
	s.appendAudit(ctx, author, "changeset.apply", "awaiting_confirm", cs.ID, map[string]any{"confirmDeadline": deadline})
	return cs, nil
}

// Confirm commits a changeset within its commit-confirm window
// (docs/api.md: POST /changesets/{id}/confirm). It cancels the rollback timer,
// moves awaiting_confirm→committed, releases the apply lock, captures a
// post-state snapshot, and refreshes inventory. It returns *ErrNotConfirmable
// if the changeset is not currently awaiting confirmation.
func (s *Service) Confirm(ctx context.Context, id, author string) (Changeset, error) {
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
	s.cancelTimerLocked(id)
	if err := cs.Transition(StatusCommitted, s.now().Unix()); err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	cs.ConfirmDeadline = nil
	if err := s.persist(ctx, cs); err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	s.lockHeldBy = ""
	plan := decodePlan(cs.Plan)
	log := decodeApplyLog(cs.ApplyLog)
	s.applyMu.Unlock()

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
func (s *Service) Rollback(ctx context.Context, id, author string) (Changeset, error) {
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
		plan, rbErr := s.doRollbackLocked(ctx, &cs, author)
		s.applyMu.Unlock()
		s.refreshAfterTerminal(ctx, plan)
		return cs, rbErr
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
		return s.createRestoringDraft(ctx, author, cs)
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
	cs, err := s.Get(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		s.log.Error("change: auto-rollback: loading changeset", "changeset_id", id, "error", err)
		return
	}
	if cs.Status != StatusAwaitingConfirm {
		s.applyMu.Unlock()
		return
	}
	plan, rbErr := s.doRollbackLocked(ctx, &cs, systemRollbackActor)
	s.applyMu.Unlock()
	if rbErr != nil {
		s.log.Error("change: auto-rollback failed", "changeset_id", id, "error", rbErr)
	}
	s.refreshAfterTerminal(ctx, plan)
}

// doRollbackLocked restores an applied (awaiting_confirm) changeset's pre-apply
// file state on every affected node and transitions it to rolled_back (or
// failed if any node could not be restored — the "couldn't even fully roll
// back" case changeset.go's StatusFailed doc distinguishes). It releases the
// apply lock. Caller must hold applyMu; it does NOT refresh inventory (the
// caller does, after unlocking).
func (s *Service) doRollbackLocked(ctx context.Context, cs *Changeset, actor string) (Plan, error) {
	plan := decodePlan(cs.Plan)
	log := decodeApplyLog(cs.ApplyLog)

	pre, err := s.loadPreSnapshot(ctx, cs.ID)
	if err != nil {
		s.log.Error("change: rollback: loading pre-snapshot", "changeset_id", cs.ID, "error", err)
		// Without the pre-snapshot we cannot safely restore; mark failed so an
		// operator investigates rather than leaving it awaiting_confirm forever.
		_ = cs.Transition(StatusFailed, s.now().Unix())
		cs.ConfirmDeadline = nil
		_ = s.persist(ctx, *cs)
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
	if err := s.persist(ctx, *cs); err != nil {
		s.lockHeldBy = ""
		return plan, err
	}
	s.lockHeldBy = ""
	s.broadcastStatus(*cs)
	s.appendAudit(ctx, actor, "changeset.rollback", result, cs.ID, map[string]any{"nodes": plan.affectedNodes()})
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
	return ifaces.DiffChangeset(ctx, nodeAgentReader{s.nodes}, ifOps, id)
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
		s.recoverInterruptedApply(ctx, cs)
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
	} else {
		s.log.Warn("change: recovery: no pre-snapshot for interrupted apply", "changeset_id", cs.ID, "error", err)
	}
	_ = cs.Transition(StatusFailed, s.now().Unix())
	cs.ConfirmDeadline = nil
	_ = s.persist(ctx, cs)
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
// includePending=false), so the other three methods are intentionally
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
