// SPDX-License-Identifier: Apache-2.0

// apply_staged.go implements T-2602's canary / staged multi-node apply: a
// changeset that fans out to several nodes can apply to a named subset
// first, PAUSE, and only then continue to the rest.
//
// WHY THIS EXISTS. Before this card an apply reached every affected node in
// one pass. If the change was wrong, it was wrong everywhere at once, and
// commit-confirm caught it only after the damage was already cluster-wide.
// A staged apply gives the operator (and T-2603's findings engine)
// something intermediate to observe: one node carrying the change while the
// rest of the cluster is still known-good.
//
// THREE INVARIANTS, all load-bearing:
//
//  1. `mode: all` IS UNTOUCHED. The default strategy is the zero value, and
//     the zero value is `all`. Apply's existing body runs the whole plan in
//     one executor pass exactly as it always did — the staged path is a
//     branch taken only when a caller explicitly asks for it. The
//     pre-existing apply test suite is the assertion.
//
//  2. THE PAUSE IS PERSISTED, NEVER IN MEMORY. A daemon killed during a
//     canary hold comes back, reads changeset_apply_stages, and either
//     resumes the hold or aborts it — per the strategy it recorded, not per
//     whatever the operator happens to click next. A changeset is never left
//     in a state nothing can describe.
//
//  3. THE COMMIT-CONFIRM DEADLINE COVERS THE WHOLE SEQUENCE. It is computed
//     once, at the very start, and every hold deadline is clamped to it. A
//     canary that stalls cannot hold the cluster open past the window: the
//     hold expires at (or before) the confirm deadline and everything
//     applied so far is rolled back.

package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// ApplyMode is an apply strategy's fan-out shape.
type ApplyMode string

const (
	// ApplyModeAll is the historical (and default) behaviour: every affected
	// node's steps run in one pass. The zero ApplyStrategy means this, so a
	// caller that never heard of staged apply gets exactly what it always got.
	ApplyModeAll ApplyMode = "all"
	// ApplyModeCanary runs the named canary nodes' steps first, then pauses.
	ApplyModeCanary ApplyMode = "canary"
)

// ApplyGate decides what ends a canary hold.
type ApplyGate string

const (
	// ApplyGateManual waits for POST /changesets/{id}/continue. The hold
	// deadline is a ceiling, not a target: nobody continuing before it is an
	// abort, not an indefinite pause.
	ApplyGateManual ApplyGate = "manual"
	// ApplyGateAuto promotes automatically at the hold deadline provided the
	// canary nodes are healthy and no error-severity finding attributable to
	// them appeared during the hold.
	ApplyGateAuto ApplyGate = "auto"
)

// Canary hold bounds. The lower bound is not arbitrary: a hold shorter than
// one findings cycle observes nothing, which would make `gate: auto` a
// rubber stamp. The upper bound is the commit-confirm ceiling — a hold can
// never outlive the window that guards it (see clampHold).
const (
	// DefaultCanaryHold is the hold used when a canary strategy names none.
	DefaultCanaryHold = 60 * time.Second
	// MinCanaryHold is the shortest hold a caller may request.
	MinCanaryHold = 10 * time.Second
	// MaxCanaryHold is the longest hold a caller may request, before the
	// confirm-deadline clamp is additionally applied.
	MaxCanaryHold = MaxConfirmTimeout
)

// ApplyStrategy is the T-2602 apply strategy: how an apply fans out across
// the nodes its plan touches. Its ZERO VALUE is `mode: all` — the historical
// behaviour — so every existing caller and every request body that omits it
// is unaffected.
//
// HoldFor is expressed in seconds (`holdForSec`) rather than as a duration
// string, matching `confirmTimeoutSec` on the same request body: the change
// API has exactly one convention for a caller-supplied duration and this
// follows it.
type ApplyStrategy struct {
	Mode        ApplyMode `json:"mode,omitempty"`
	Gate        ApplyGate `json:"gate,omitempty"`
	CanaryNodes []string  `json:"canaryNodes,omitempty"`
	HoldForSec  int       `json:"holdForSec,omitempty"`
}

// IsCanary reports whether this strategy stages the apply at all.
func (s ApplyStrategy) IsCanary() bool { return s.Mode == ApplyModeCanary }

// holdFor returns the strategy's hold duration, defaulted.
func (s ApplyStrategy) holdFor() time.Duration {
	if s.HoldForSec <= 0 {
		return DefaultCanaryHold
	}
	return time.Duration(s.HoldForSec) * time.Second
}

// ErrInvalidApplyStrategy is returned, before any mutation and before the
// changeset even transitions to applying, when a caller's applyStrategy
// cannot be honoured. Reason is written for the operator, not the log.
type ErrInvalidApplyStrategy struct {
	Reason string
	Nodes  []string
}

func (e *ErrInvalidApplyStrategy) Error() string {
	if len(e.Nodes) == 0 {
		return "change: invalid apply strategy: " + e.Reason
	}
	return fmt.Sprintf("change: invalid apply strategy: %s (%v)", e.Reason, e.Nodes)
}

// ErrNotResumable is returned by Continue/Abort when the named changeset is
// not currently paused between stages.
type ErrNotResumable struct {
	ID     string
	Status Status
	State  string
}

func (e *ErrNotResumable) Error() string {
	if e.State == "" {
		return fmt.Sprintf("change: changeset %s is not paused between apply stages (status %s)", e.ID, e.Status)
	}
	return fmt.Sprintf("change: changeset %s is not paused between apply stages (status %s, stage state %s)", e.ID, e.Status, e.State)
}

// CanaryVerdict is a CanaryHealthChecker's answer about the canary nodes at
// the end of a hold.
//
// Findings names the error-severity findings attributable to the canary
// nodes that were NOT present before the apply — the evidence, so the audit
// entry and the operator are never told merely that "something went wrong".
type CanaryVerdict struct {
	Reason   string   `json:"reason,omitempty"`
	Findings []string `json:"findings,omitempty"`
	Healthy  bool     `json:"healthy"`
}

// Clean reports whether the verdict permits automatic promotion.
func (v CanaryVerdict) Clean() bool { return v.Healthy && len(v.Findings) == 0 }

// CanaryHealthChecker is the seam `gate: auto` consults at the end of a
// hold. cmd/vnproxd wires it from the findings engine; a deployment that
// does not wire one cannot use `gate: auto` at all — asking for automatic
// promotion on evidence the daemon has no way to gather is refused at
// validation time rather than silently treated as "no evidence, therefore
// fine". That refusal is the whole reason this is a required dependency of
// the auto gate rather than a nil-safe optional one.
type CanaryHealthChecker interface {
	// CheckCanary reports whether nodes are healthy and free of NEW
	// error-severity findings observed since sinceUnix (the instant the
	// canary stage started mutating them). An error return is treated as an
	// un-clean verdict: an unassessable canary is not a proof of safety.
	CheckCanary(ctx context.Context, nodes []string, sinceUnix int64) (CanaryVerdict, error)
}

// StagedApplyState is the read model of a paused staged apply — the
// `applyStage` field on a changeset response, and the state T-2603 reads
// before deciding to interrupt a sequence.
//
//nolint:govet // fieldalignment: response DTO; field order is the JSON shape, not packing (same precedent as internal/api's changesetResponse).
type StagedApplyState struct {
	State           string        `json:"state"`
	Author          string        `json:"author,omitempty"`
	Strategy        ApplyStrategy `json:"strategy"`
	AppliedNodes    []string      `json:"appliedNodes"`
	PendingNodes    []string      `json:"pendingNodes"`
	HoldStartedAt   int64         `json:"holdStartedAt"`
	HoldDeadline    int64         `json:"holdDeadline"`
	ConfirmDeadline int64         `json:"confirmDeadline"`
}

// --- strategy validation --------------------------------------------------

// stageableKinds are the plan step kinds a canary split can safely reorder
// around. Everything NOT here is a cluster-scope step whose documented
// position is BEFORE the per-node stage/reload pairs (docs/data-model.md
// §3's category ordering, and StepSwitchApply's own "switch-port steps
// before node-network steps" rule) — running it in the canary stage would
// already have changed cluster-wide state before the "only the canary
// nodes" promise, and running it in the remaining stage would invert an
// ordering the plan builder deliberately chose. Rather than quietly pick
// one of two wrong answers, canary mode refuses such a plan outright.
var canaryUnstageableKinds = map[StepKind]bool{
	StepSwitchApply: true,
	StepSDNStage:    true,
	StepIpamAlloc:   true,
}

// validateApplyStrategy normalizes and checks strategy against the plan it
// will run, returning the normalized value or *ErrInvalidApplyStrategy. It
// runs BEFORE the changeset transitions to applying and before any snapshot
// or mutation, so a refused strategy leaves the changeset exactly where it
// was.
func (s *Service) validateApplyStrategy(strategy ApplyStrategy, plan Plan, confirmTimeout time.Duration) (ApplyStrategy, error) {
	switch strategy.Mode {
	case "", ApplyModeAll:
		// A body that says `all` but also names canary nodes (or a gate, or a
		// hold) is a mistake worth naming, not a set of fields to ignore.
		if len(strategy.CanaryNodes) > 0 || strategy.HoldForSec != 0 || strategy.Gate != "" {
			return ApplyStrategy{}, &ErrInvalidApplyStrategy{
				Reason: `mode "all" applies to every affected node at once and takes no canaryNodes, holdForSec or gate`,
			}
		}
		return ApplyStrategy{Mode: ApplyModeAll}, nil
	case ApplyModeCanary:
	default:
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: fmt.Sprintf("unknown mode %q (want \"all\" or \"canary\")", strategy.Mode)}
	}

	if s.stages == nil {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: "staged apply is not configured on this daemon: the pause between stages has nowhere to be recorded, and an unrecorded pause is exactly the unknown state a staged apply must never produce"}
	}

	switch strategy.Gate {
	case "":
		strategy.Gate = ApplyGateManual
	case ApplyGateManual:
	case ApplyGateAuto:
		if s.canary == nil {
			return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: `gate "auto" needs a canary health checker, and this daemon has none wired — automatic promotion on evidence nothing can gather would be a rubber stamp, so it is refused rather than assumed clean`}
		}
		if planRequiresPVESession(plan) {
			return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: `gate "auto" cannot promote a changeset whose remaining steps need a live PVE session (sdn.apply / fw.*): the automatic promotion runs from a timer with no user session, and the apply-time revert ticket exists only to UNDO this changeset, never to carry it forward`}
		}
	default:
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: fmt.Sprintf("unknown gate %q (want \"manual\" or \"auto\")", strategy.Gate)}
	}

	var unstageable []string
	seenKind := map[StepKind]bool{}
	for _, st := range plan.Steps {
		if canaryUnstageableKinds[st.Kind] && !seenKind[st.Kind] {
			seenKind[st.Kind] = true
			unstageable = append(unstageable, string(st.Kind))
		}
	}
	if len(unstageable) > 0 {
		sort.Strings(unstageable)
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{
			Reason: "this changeset's plan carries cluster-scope steps that must run before any per-node step, so it cannot be split into a canary stage",
			Nodes:  unstageable,
		}
	}

	affected := plan.affectedNodes()
	if len(affected) < 2 {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: "a canary apply needs at least two affected nodes; this changeset touches fewer", Nodes: affected}
	}

	if len(strategy.CanaryNodes) == 0 {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{Reason: `mode "canary" requires a non-empty canaryNodes list`}
	}
	inPlan := map[string]bool{}
	for _, n := range affected {
		inPlan[n] = true
	}
	canary := make([]string, 0, len(strategy.CanaryNodes))
	seen := map[string]bool{}
	var unaffected []string
	for _, n := range strategy.CanaryNodes {
		if seen[n] {
			continue
		}
		seen[n] = true
		if !inPlan[n] {
			unaffected = append(unaffected, n)
			continue
		}
		canary = append(canary, n)
	}
	// AC7: naming a node the changeset does not touch is a validation error,
	// not a silently-ignored entry. A typo'd canary node would otherwise
	// produce an apply that staged nothing at all while reporting success.
	if len(unaffected) > 0 {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{
			Reason: "canaryNodes names node(s) this changeset does not affect",
			Nodes:  unaffected,
		}
	}
	if len(canary) == len(affected) {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{
			Reason: "canaryNodes covers every node this changeset affects, so there would be no second stage to hold before",
			Nodes:  canary,
		}
	}
	strategy.CanaryNodes = canary

	hold := strategy.holdFor()
	if hold < MinCanaryHold || hold > MaxCanaryHold {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{
			Reason: fmt.Sprintf("holdForSec must be between %d and %d seconds", int(MinCanaryHold.Seconds()), int(MaxCanaryHold.Seconds())),
		}
	}
	// A hold that could not possibly end before the commit-confirm window
	// does is refused up front rather than silently clamped to zero useful
	// observation time.
	if hold >= confirmTimeout {
		return ApplyStrategy{}, &ErrInvalidApplyStrategy{
			Reason: fmt.Sprintf("holdForSec (%d) must be shorter than the commit-confirm window (%d seconds): the window covers the WHOLE staged sequence, so a hold that fills it leaves no time to apply the remaining nodes",
				int(hold.Seconds()), int(confirmTimeout.Seconds())),
		}
	}
	strategy.HoldForSec = int(hold.Seconds())
	return strategy, nil
}

// planRequiresPVESession reports whether plan carries any step that can only
// execute with a live user PVE ticket — mirroring apply_exec.go's own
// "if e.pveGW == nil" guards.
func planRequiresPVESession(plan Plan) bool {
	for _, st := range plan.Steps {
		switch st.Kind {
		case StepSDNStage, StepSDNApply, StepFwApply, StepFwVerify, StepIpamAlloc:
			return true
		}
	}
	return false
}

// --- stage partitioning ---------------------------------------------------

// canaryStageIndexes partitions plan's step indexes into the canary stage
// and the remaining stage, preserving each stage's internal plan order. A
// step with no node (a cluster-scope step) always lands in the remaining
// stage: validateApplyStrategy has already refused the kinds whose
// documented position is before the per-node steps, so what is left here
// (the trailing sdn.apply, a cluster-scope fw ruleset) belongs after them
// anyway.
func canaryStageIndexes(plan Plan, canaryNodes []string) (canary, rest []int) {
	isCanary := make(map[string]bool, len(canaryNodes))
	for _, n := range canaryNodes {
		isCanary[n] = true
	}
	for i, st := range plan.Steps {
		if st.Node != "" && isCanary[st.Node] {
			canary = append(canary, i)
			continue
		}
		rest = append(rest, i)
	}
	return canary, rest
}

// allStepIndexes is the single-stage partition every `mode: all` apply uses:
// every step, in plan order. It exists so the all-at-once path and the
// staged path go through exactly the same executor entry point, rather than
// two implementations that could drift.
func allStepIndexes(plan Plan) []int {
	out := make([]int, len(plan.Steps))
	for i := range plan.Steps {
		out[i] = i
	}
	return out
}

// stageNodes returns, in plan.affectedNodes() order, the nodes the given
// step indexes touch. Keeping the affectedNodes ordering (rather than the
// index order) matters: rollback walks nodes in reverse plan order, and a
// staged rollback must reverse the same order the all-at-once one does.
func (p Plan) stageNodes(idxs []int) []string {
	in := map[string]bool{}
	for _, i := range idxs {
		if i >= 0 && i < len(p.Steps) && p.Steps[i].Node != "" {
			in[p.Steps[i].Node] = true
		}
	}
	out := make([]string, 0, len(in))
	for _, node := range p.affectedNodes() {
		if in[node] {
			out = append(out, node)
		}
	}
	return out
}

// restrictedToNodes returns a copy of p carrying only the steps that belong
// to one of nodes (cluster-scope steps, which have no node, are dropped).
// It is what makes "abort restores only the stages that ran" true without a
// second restore implementation: doRollbackScopedLocked hands the restricted
// plan to the same restoreAll/restoreAllDistributed the ordinary rollback
// uses, so a node that never appears in it is never contacted — the property
// AC2 asserts.
func (p Plan) restrictedToNodes(nodes []string) Plan {
	keep := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		keep[n] = true
	}
	out := Plan{Steps: make([]Step, 0, len(p.Steps))}
	for _, st := range p.Steps {
		if st.Node != "" && keep[st.Node] {
			out.Steps = append(out.Steps, st)
		}
	}
	return out
}

// --- the hold -------------------------------------------------------------

// holdAfterCanary records the paused state after a successful canary stage:
// persist the partial apply log and the stage row, arm the single timer that
// bounds the hold, audit, and return with the apply lock still held.
//
// The changeset stays in StatusApplying, which is the truth: the apply has
// neither succeeded nor failed. What it has done — and to which nodes — is
// in the stage row and on the `applyStage` read model, so nothing about the
// pause is inferred.
func (s *Service) holdAfterCanary(ctx context.Context, cs Changeset, plan Plan, author string, log *ApplyLog,
	deadline int64, strategy ApplyStrategy, applied, pending []string) (Changeset, error) {

	holdDeadline := clampHold(s.now(), strategy.holdFor(), deadline)

	s.applyMu.Lock()
	if s.nodeTimers != nil {
		var timers []NodeTimerLog
		for _, node := range applied {
			timers = append(timers, NodeTimerLog{Node: node, Status: NodeTimerStatusArmed, Deadline: deadline})
		}
		log.NodeTimers = mergeNodeTimerLogs(log.NodeTimers, timers)
	}
	logJSON, _ := json.Marshal(log)
	cs.ApplyLog = logJSON
	// The whole-sequence commit-confirm deadline is published now, not at
	// promotion: it is already running, every canary node's local timer is
	// already armed with it, and an operator staring at a paused apply needs
	// to see the clock that will roll it back.
	cs.ConfirmDeadline = &deadline
	cs.UpdatedAt = s.now().Unix()
	if err := s.persist(ctx, cs); err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	if err := s.putStage(ctx, store.ChangesetApplyStage{
		ChangesetID:     cs.ID,
		State:           store.StageCanaryHold,
		Author:          author,
		HoldStartedAt:   s.now().Unix(),
		HoldDeadline:    holdDeadline,
		ConfirmDeadline: deadline,
	}, strategy, applied, pending); err != nil {
		// The canary stage HAS run and its pause cannot be recorded. Undoing it
		// right here — while still holding the lock, with the node list we
		// already have in hand — is the only answer that leaves no unknown
		// state. Deliberately NOT routed through abortStagedApply: that reads
		// the very stage row this branch has just failed to write, so it would
		// refuse and leave the canary applied with nothing describing it.
		s.log.Error("change: could not record the canary hold; rolling the canary stage back rather than leaving it unrecorded",
			"changeset_id", cs.ID, "error", err)
		rbPlan, rbErr := s.doRollbackScopedLocked(ctx, &cs, systemRollbackActor, ChangeOpRollback, nil, applied)
		s.applyMu.Unlock()
		s.clearStage(ctx, cs.ID)
		s.appendAudit(ctx, author, "changeset.abort", abortResult(cs.Status), cs.ID, map[string]any{
			"reason": "the canary hold could not be recorded in the store", "restoredNodes": applied, "untouchedNodes": pending,
		})
		s.refreshAfterTerminal(ctx, rbPlan)
		if rbErr != nil {
			return cs, rbErr
		}
		return cs, fmt.Errorf("change: recording the canary hold for changeset %s: %w", cs.ID, err)
	}
	s.armHoldTimerLocked(cs.ID, holdDeadline)
	s.applyMu.Unlock()

	s.broadcastStatus(cs)
	s.appendAudit(ctx, author, "changeset.apply", "canary_hold", cs.ID, map[string]any{
		"canaryNodes": applied, "pendingNodes": pending, "gate": string(strategy.Gate),
		"holdDeadline": holdDeadline, "confirmDeadline": deadline,
	})
	s.log.Info("change: canary stage applied; holding before the rest of the cluster",
		"changeset_id", cs.ID, "canary_nodes", applied, "pending_nodes", pending,
		"gate", string(strategy.Gate), "hold_deadline", holdDeadline, "confirm_deadline", deadline)
	return cs, nil
}

// clampHold bounds a hold to the commit-confirm deadline. This is AC5 in one
// line: the window covers the whole staged sequence, so no hold may end
// after it.
func clampHold(now time.Time, hold time.Duration, confirmDeadline int64) int64 {
	holdDeadline := now.Add(hold).Unix()
	if holdDeadline > confirmDeadline {
		return confirmDeadline
	}
	return holdDeadline
}

// putStage persists one staged-apply row, marshaling the strategy and node
// lists.
func (s *Service) putStage(ctx context.Context, row store.ChangesetApplyStage, strategy ApplyStrategy, applied, pending []string) error {
	strategyJSON, err := json.Marshal(strategy)
	if err != nil {
		return fmt.Errorf("change: marshaling apply strategy for changeset %s: %w", row.ChangesetID, err)
	}
	appliedJSON, err := json.Marshal(nonNil(applied))
	if err != nil {
		return fmt.Errorf("change: marshaling applied nodes for changeset %s: %w", row.ChangesetID, err)
	}
	pendingJSON, err := json.Marshal(nonNil(pending))
	if err != nil {
		return fmt.Errorf("change: marshaling pending nodes for changeset %s: %w", row.ChangesetID, err)
	}
	row.StrategyJSON = string(strategyJSON)
	row.AppliedNodesJSON = string(appliedJSON)
	row.PendingNodesJSON = string(pendingJSON)
	return s.stages.Upsert(ctx, row)
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// decodeStage turns a stored row into the typed read model.
func decodeStage(row store.ChangesetApplyStage) (StagedApplyState, error) {
	out := StagedApplyState{
		State: row.State, Author: row.Author, HoldStartedAt: row.HoldStartedAt,
		HoldDeadline: row.HoldDeadline, ConfirmDeadline: row.ConfirmDeadline,
	}
	if err := json.Unmarshal([]byte(row.StrategyJSON), &out.Strategy); err != nil {
		return StagedApplyState{}, fmt.Errorf("change: decoding recorded apply strategy for changeset %s: %w", row.ChangesetID, err)
	}
	if err := json.Unmarshal([]byte(row.AppliedNodesJSON), &out.AppliedNodes); err != nil {
		return StagedApplyState{}, fmt.Errorf("change: decoding applied nodes for changeset %s: %w", row.ChangesetID, err)
	}
	if err := json.Unmarshal([]byte(row.PendingNodesJSON), &out.PendingNodes); err != nil {
		return StagedApplyState{}, fmt.Errorf("change: decoding pending nodes for changeset %s: %w", row.ChangesetID, err)
	}
	return out, nil
}

// StagedApplyState returns the paused staged-apply state for id, and whether
// there is one at all. A changeset applied the ordinary all-at-once way
// (every changeset, by default) has none.
//
// This is the read T-2603 makes before deciding whether a rollback it wants
// to trigger must interrupt a staged sequence rather than an ordinary
// confirm window.
func (s *Service) StagedApplyState(ctx context.Context, id string) (StagedApplyState, bool, error) {
	if s.stages == nil {
		return StagedApplyState{}, false, nil
	}
	row, err := s.stages.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return StagedApplyState{}, false, nil
		}
		return StagedApplyState{}, false, err
	}
	state, err := decodeStage(row)
	if err != nil {
		return StagedApplyState{}, false, err
	}
	return state, true, nil
}

// clearStage deletes id's staged-apply row. Every path out of the pause
// calls it unconditionally, including the failure paths: a stale pause row
// would make a terminal changeset look resumable.
func (s *Service) clearStage(ctx context.Context, id string) {
	if s.stages == nil {
		return
	}
	if err := s.stages.Delete(ctx, id); err != nil {
		s.log.Error("change: clearing staged-apply state", "changeset_id", id, "error", err)
	}
}

// --- hold timers ----------------------------------------------------------
//
// The hold has its own timer map, separate from s.timers (the commit-confirm
// rollback timers), because the two can be armed for the same changeset at
// different instants and cancelling one must never cancel the other.

func (s *Service) armHoldTimerLocked(id string, deadlineUnix int64) {
	if t, ok := s.holdTimers[id]; ok {
		t.Stop()
		delete(s.holdTimers, id)
	}
	d := time.Until(time.Unix(deadlineUnix, 0))
	if d < 0 {
		d = 0
	}
	s.holdTimers[id] = s.newTimer(d, func() { s.onHoldExpired(context.Background(), id) })
}

func (s *Service) cancelHoldTimerLocked(id string) {
	if t, ok := s.holdTimers[id]; ok {
		t.Stop()
		delete(s.holdTimers, id)
	}
}

// StopHoldTimers cancels every armed canary-hold timer for graceful daemon
// shutdown, without touching any stage row — ArmPendingRollbacks re-arms
// them from the store on the next start, exactly like StopTimers does for
// the commit-confirm timers.
func (s *Service) StopHoldTimers() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	for id, t := range s.holdTimers {
		t.Stop()
		delete(s.holdTimers, id)
	}
}

// onHoldExpired is the canary hold's deadline callback. It resolves the hold
// exactly one way, never leaving it pending:
//
//   - the commit-confirm window has run out -> abort (AC5);
//   - gate auto and the canary is clean    -> promote;
//   - gate auto and the canary is not      -> abort, naming the findings;
//   - gate manual                          -> abort (nobody continued in time).
func (s *Service) onHoldExpired(ctx context.Context, id string) {
	s.applyMu.Lock()
	delete(s.holdTimers, id)
	// T-1704 single-writer fence, for the same reason autoRollback has one: a
	// demoted former-active must not drive an unattended promotion or abort
	// the current lease-term holder is responsible for.
	if !s.mayLead() {
		s.applyMu.Unlock()
		s.log.Info("change: canary hold expiry skipped — not the current HA leader", "changeset_id", id)
		return
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		s.log.Error("change: canary hold expiry: loading changeset", "changeset_id", id, "error", err)
		return
	}
	state, ok, err := s.StagedApplyState(ctx, id)
	s.applyMu.Unlock()
	if err != nil {
		s.log.Error("change: canary hold expiry: reading staged state", "changeset_id", id, "error", err)
		return
	}
	if !ok || state.State != store.StageCanaryHold || cs.Status != StatusApplying {
		return // already resolved (continued, aborted, or rolled back)
	}
	s.resolveHold(ctx, cs, state)
}

// resolveHold applies onHoldExpired's decision table. Split out so the
// startup recovery path (recoverStagedApply) can reuse it for a hold whose
// deadline elapsed while the daemon was down.
func (s *Service) resolveHold(ctx context.Context, cs Changeset, state StagedApplyState) {
	now := s.now().Unix()
	if now >= state.ConfirmDeadline {
		// AC5. The commit-confirm window covers the whole sequence; a hold
		// that outlasted it rolls back everything applied so far.
		if _, err := s.abortStagedApply(ctx, cs.ID, systemRollbackActor,
			"the commit-confirm window expired during the canary hold", nil); err != nil {
			s.log.Error("change: rolling back an expired canary hold", "changeset_id", cs.ID, "error", err)
		}
		return
	}

	if state.Strategy.Gate != ApplyGateAuto {
		if _, err := s.abortStagedApply(ctx, cs.ID, systemRollbackActor,
			"the canary hold elapsed with no continue", nil); err != nil {
			s.log.Error("change: rolling back an un-continued canary hold", "changeset_id", cs.ID, "error", err)
		}
		return
	}

	verdict := s.checkCanary(ctx, state)
	if !verdict.Clean() {
		reason := verdict.Reason
		if reason == "" {
			reason = "the canary nodes did not come through the hold clean"
		}
		s.appendAudit(ctx, systemRollbackActor, "changeset.apply", "canary_gate_failed", cs.ID, map[string]any{
			"canaryNodes": state.AppliedNodes, "findings": verdict.Findings, "reason": reason,
		})
		if _, err := s.abortStagedApply(ctx, cs.ID, systemRollbackActor, reason, nil); err != nil {
			s.log.Error("change: rolling back a failed canary gate", "changeset_id", cs.ID, "error", err)
		}
		return
	}

	s.appendAudit(ctx, systemRollbackActor, "changeset.apply", "canary_gate_passed", cs.ID, map[string]any{
		"canaryNodes": state.AppliedNodes,
	})
	if _, err := s.continueStaged(ctx, cs.ID, systemRollbackActor, nil); err != nil {
		s.log.Error("change: promoting a clean canary", "changeset_id", cs.ID, "error", err)
	}
}

// checkCanary asks the configured checker about the canary nodes. Every
// failure mode — no checker, a read error — yields an un-clean verdict: an
// unassessable canary is not a proof of safety, so the auto gate declines to
// promote rather than promoting blind. (validateApplyStrategy already
// refuses `gate: auto` with no checker at all, so the nil branch here is a
// belt-and-braces fail-closed, not the expected path.)
func (s *Service) checkCanary(ctx context.Context, state StagedApplyState) CanaryVerdict {
	if s.canary == nil {
		return CanaryVerdict{Healthy: false, Reason: "no canary health checker is wired on this daemon"}
	}
	verdict, err := s.canary.CheckCanary(ctx, state.AppliedNodes, state.HoldStartedAt)
	if err != nil {
		s.log.Warn("change: canary health check failed; declining to promote", "error", err)
		return CanaryVerdict{Healthy: false, Reason: fmt.Sprintf("the canary health check could not be completed: %v", err)}
	}
	return verdict
}

// --- continue -------------------------------------------------------------

// ContinueStagedApply is docs/api.md's POST /changesets/{id}/continue: the
// manual gate's promotion. It runs the remaining stage and, on success,
// moves the changeset into the ordinary commit-confirm window with the SAME
// deadline the whole sequence started with.
//
// pveGW carries the requesting user's own PVE ticket, needed only when the
// remaining stage has steps that require one.
func (s *Service) ContinueStagedApply(ctx context.Context, id, author string, pveGW PVEGateway) (Changeset, error) {
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	return s.continueStaged(ctx, id, author, pveGW)
}

func (s *Service) continueStaged(ctx context.Context, id, author string, pveGW PVEGateway) (Changeset, error) {
	// Claim the pause under the lock and flip it to `promoting` BEFORE
	// releasing: the remaining stage runs unlocked (exactly as the first
	// stage did), so the persisted state transition is what makes a second
	// continue — or a concurrent abort — refuse rather than race.
	s.applyMu.Lock()
	cs, state, err := s.claimPauseLocked(ctx, id, store.StagePromoting)
	if err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	s.cancelHoldTimerLocked(id)
	s.applyMu.Unlock()

	plan := decodePlan(cs.Plan)
	pre, err := s.loadPreSnapshot(ctx, id)
	if err != nil {
		s.log.Error("change: continue: loading pre-snapshot", "changeset_id", id, "error", err)
		return s.finishFailedStaged(ctx, cs, plan, author, decodeApplyLogPtr(cs.ApplyLog),
			fmt.Errorf("change: continuing changeset %s: %w", id, err))
	}

	_, rest := canaryStageIndexes(plan, state.Strategy.CanaryNodes)
	deadline := state.ConfirmDeadline

	ex := s.newExecutor(cs, plan, pre, pveGW, deadline)
	// Seed the canary stage's own log so the second executor reports ONE
	// apply log for the whole sequence, and — load-bearing — so a failure in
	// the remaining stage rolls the canary nodes back too: undoNode reads
	// each node's reload-step status out of this seeded log to decide whether
	// that node was committed.
	ex.seedLog(decodeApplyLog(cs.ApplyLog))
	// Rollback on failure now covers every node touched by EITHER stage.
	ex.rollbackNodes = unionInPlanOrder(plan, state.AppliedNodes, plan.stageNodes(rest))

	if runErr := ex.runSteps(ctx, rest); runErr != nil {
		return s.finishFailedStaged(ctx, cs, plan, author, ex.log, runErr)
	}

	s.clearStage(ctx, id)
	s.appendAudit(ctx, author, "changeset.continue", "promoted", id, map[string]any{
		"canaryNodes": state.AppliedNodes, "promotedNodes": plan.stageNodes(rest),
	})
	// The revert-ticket coverage report is recomputed from the sealed
	// ticket's own persisted expiry rather than re-sealed: the ticket was
	// sealed once, at the start of the sequence, and continuing does not
	// extend it.
	return s.finishAwaitingConfirm(ctx, cs, plan, author, ex.log, deadline, revertCoverage(plan, deadline, cs.RevertTicketExpiresAt))
}

// claimPauseLocked loads id's changeset and its paused stage row, verifies
// the pause is real, and atomically moves the row to next. Caller must hold
// applyMu.
func (s *Service) claimPauseLocked(ctx context.Context, id, next string) (Changeset, StagedApplyState, error) {
	cs, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, StagedApplyState{}, err
	}
	state, ok, err := s.StagedApplyState(ctx, id)
	if err != nil {
		return Changeset{}, StagedApplyState{}, err
	}
	if !ok || state.State != store.StageCanaryHold || cs.Status != StatusApplying {
		stateName := ""
		if ok {
			stateName = state.State
		}
		return Changeset{}, StagedApplyState{}, &ErrNotResumable{ID: id, Status: cs.Status, State: stateName}
	}
	if next != state.State {
		if err := s.putStage(ctx, store.ChangesetApplyStage{
			ChangesetID: id, State: next, Author: state.Author,
			HoldStartedAt: state.HoldStartedAt, HoldDeadline: state.HoldDeadline, ConfirmDeadline: state.ConfirmDeadline,
		}, state.Strategy, state.AppliedNodes, state.PendingNodes); err != nil {
			return Changeset{}, StagedApplyState{}, err
		}
	}
	return cs, state, nil
}

// finishFailedStaged is finishFailedApply for a staged sequence: the same
// terminal bookkeeping, plus clearing the pause row (so nothing can later
// mistake a failed changeset for a resumable one) and both timers.
func (s *Service) finishFailedStaged(ctx context.Context, cs Changeset, plan Plan, author string, log *ApplyLog, cause error) (Changeset, error) {
	s.clearStage(ctx, cs.ID)
	s.applyMu.Lock()
	s.cancelHoldTimerLocked(cs.ID)
	s.cancelTimerLocked(cs.ID)
	s.applyMu.Unlock()
	return s.finishFailedApply(ctx, cs, plan, author, log, cause)
}

// --- abort ----------------------------------------------------------------

// AbortStagedApply rolls a paused staged apply back, restoring EXACTLY the
// nodes whose stages ran and contacting no others (AC2). It is the operator's
// POST /changesets/{id}/rollback during a hold, the auto gate's refusal, and
// the interruption point T-2603 calls when a new error finding lands mid-hold.
//
// reason is recorded on the audit entry so the rollback is never merely "it
// rolled back".
func (s *Service) AbortStagedApply(ctx context.Context, id, actor, reason string, pveGW PVEGateway) (Changeset, error) {
	if !s.applyConfigured() {
		return Changeset{}, &ErrApplyNotConfigured{}
	}
	return s.abortStagedApply(ctx, id, actor, reason, pveGW)
}

func (s *Service) abortStagedApply(ctx context.Context, id, actor, reason string, pveGW PVEGateway) (Changeset, error) {
	s.applyMu.Lock()
	cs, err := s.Get(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	state, ok, err := s.StagedApplyState(ctx, id)
	if err != nil {
		s.applyMu.Unlock()
		return Changeset{}, err
	}
	if !ok || cs.Status != StatusApplying {
		s.applyMu.Unlock()
		stateName := ""
		if ok {
			stateName = state.State
		}
		return Changeset{}, &ErrNotResumable{ID: id, Status: cs.Status, State: stateName}
	}
	s.cancelHoldTimerLocked(id)
	s.cancelTimerLocked(id)

	plan, rbErr := s.doRollbackScopedLocked(ctx, &cs, actor, ChangeOpRollback, pveGW, state.AppliedNodes)
	s.applyMu.Unlock()

	s.clearStage(ctx, id)
	s.appendAudit(ctx, actor, "changeset.abort", abortResult(cs.Status), id, map[string]any{
		"reason": reason, "restoredNodes": state.AppliedNodes, "untouchedNodes": state.PendingNodes,
	})
	s.log.Info("change: staged apply aborted; restored only the stages that ran",
		"changeset_id", id, "reason", reason, "restored_nodes", state.AppliedNodes, "untouched_nodes", state.PendingNodes)
	s.refreshAfterTerminal(ctx, plan)
	return cs, rbErr
}

func abortResult(status Status) string {
	if status == StatusFailed {
		return "rollback_incomplete"
	}
	return "rolled_back"
}

// --- restart recovery (AC4) -----------------------------------------------

// recoverStagedApply resolves one staged apply found paused at daemon
// startup. It never leaves the changeset where it found it:
//
//   - `promoting`: the daemon died while the remaining stage was executing,
//     so how far it got is unknowable. Everything the plan touches is
//     restored and the changeset fails — the same stance
//     recoverInterruptedApply takes for an interrupted ordinary apply.
//   - `canary_hold` past its hold deadline: the hold's decision is taken now
//     (resolveHold), which for an expired commit-confirm window is an abort
//     and for a clean auto gate is a promotion.
//   - `canary_hold` still within its deadline: the hold RESUMES — the timer
//     is re-armed from the recorded deadline, so a manual gate's operator
//     still has the time they were promised.
func (s *Service) recoverStagedApply(ctx context.Context, cs Changeset, state StagedApplyState) {
	if state.State == store.StagePromoting {
		s.log.Warn("change: recovering a staged apply interrupted mid-promotion; restoring every node the plan touches",
			"changeset_id", cs.ID)
		s.clearStage(ctx, cs.ID)
		s.recoverInterruptedApply(ctx, cs)
		return
	}

	if s.now().Unix() >= state.HoldDeadline {
		s.log.Info("change: canary hold expired while the daemon was down; resolving it now",
			"changeset_id", cs.ID, "hold_deadline", state.HoldDeadline)
		s.appendAudit(ctx, systemRollbackActor, "changeset.recover", "canary_hold_expired", cs.ID, map[string]any{
			"gate": string(state.Strategy.Gate), "appliedNodes": state.AppliedNodes,
		})
		s.resolveHold(ctx, cs, state)
		return
	}

	// Only the hold timer is re-armed, and that is deliberate: clampHold
	// guarantees the hold deadline is never later than the commit-confirm
	// deadline, so the hold timer IS the window's enforcement point while a
	// sequence is paused (resolveHold aborts when it fires at or after the
	// confirm deadline). Arming a second timer for the same instant would
	// give two callbacks the right to resolve one changeset.
	s.applyMu.Lock()
	s.lockHeldBy = cs.ID
	s.armHoldTimerLocked(cs.ID, state.HoldDeadline)
	s.applyMu.Unlock()
	s.log.Info("change: re-armed a canary hold from the store", "changeset_id", cs.ID,
		"hold_deadline", state.HoldDeadline, "confirm_deadline", state.ConfirmDeadline, "gate", string(state.Strategy.Gate))
	s.appendAudit(ctx, systemRollbackActor, "changeset.recover", "canary_hold_resumed", cs.ID, map[string]any{
		"gate": string(state.Strategy.Gate), "appliedNodes": state.AppliedNodes, "holdDeadline": state.HoldDeadline,
	})
}

// unionInPlanOrder merges node lists, de-duplicated, in plan.affectedNodes()
// order — the order rollback walks in reverse.
func unionInPlanOrder(plan Plan, lists ...[]string) []string {
	want := map[string]bool{}
	for _, l := range lists {
		for _, n := range l {
			want[n] = true
		}
	}
	out := make([]string, 0, len(want))
	for _, node := range plan.affectedNodes() {
		if want[node] {
			out = append(out, node)
		}
	}
	return out
}

// decodeApplyLogPtr is decodeApplyLog returning a pointer, for the failure
// paths that must hand finishFailedApply a log they did not build.
func decodeApplyLogPtr(raw json.RawMessage) *ApplyLog {
	l := decodeApplyLog(raw)
	return &l
}
