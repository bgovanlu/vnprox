// SPDX-License-Identifier: Apache-2.0

// autorollback.go implements T-2603's finding-triggered auto-rollback inside
// the commit-confirm window.
//
// WHY THIS EXISTS. Commit-confirm already handles one failure mode well: the
// operator lost their connection and never confirmed. The commoner one is
// "the change was wrong and the operator is still staring at the screen
// wondering why" — and in that case vnprox already KNOWS. The findings engine
// detects the breakage within a cycle and, before this card, did nothing with
// it.
//
// FIVE RULES, all load-bearing:
//
//  1. OFF BY DEFAULT, PER CHANGESET. ApplyOptions.AutoRollbackOnError is a
//     *bool: nil means "use the cluster default", which itself defaults to
//     off. An apply that does not ask for this gets byte-identical behaviour
//     to every pre-T-2603 apply — nothing observes it, nothing can trigger.
//     Arming it is audited (changeset.auto_rollback / armed) naming where the
//     decision came from.
//
//  2. PRE-EXISTING FINDINGS NEVER TRIGGER. The guard captures the findings
//     stream's ID set as of the cycle BEFORE the apply and treats every ID in
//     it as pre-existing for the whole window — not "older than N seconds",
//     which would make the property a race. A finding that was already firing
//     when the operator clicked apply cannot roll their change back, however
//     severe, no matter how the engine's cycles land around the apply.
//
//  3. ATTRIBUTION IS T-2404's Impact, NOTHING ELSE. The guard remembers the
//     nodes, carriers and guests ComputeImpact reported for this changeset's
//     ops. A finding outside that set never triggers, however severe. Impact
//     over-approximates by design (impact.go's rule 2), and this inherits that
//     stance: a rollback that was not needed costs one re-apply, a rollback
//     that never came costs the outage.
//
//  4. NO EVIDENCE MEANS NO TRIGGER. If the daemon has never seen a findings
//     cycle when a guard is armed, the first cycle it does see becomes that
//     guard's baseline and cannot trigger it. Everything looking new because
//     we were not watching before is not evidence that the apply broke
//     something. The same reasoning covers a daemon restart mid-window: an
//     in-memory guard is not re-armed, and the commit-confirm timer — which IS
//     persisted and re-armed — remains the safety net it always was.
//
//  5. EXACTLY ONE CALLBACK RESOLVES A CHANGESET. A guard is removed from the
//     map under the same lock that decides it matched, so a second findings
//     cycle arriving while the rollback is still running cannot start a second
//     one. Interrupting a T-2602 canary hold routes through AbortStagedApply
//     (restoring exactly the stages that ran), never through Rollback.

package change

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ObservedFinding is one entry of the unified findings stream as the change
// engine sees it — the subset of internal/findings.Finding this package needs
// to decide whether an apply broke something.
//
// It is a change-package type on purpose: internal/findings imports
// internal/change (for the fixing-changeset op patch), so the dependency can
// only ever run in that direction. cmd/vnproxd converts.
type ObservedFinding struct {
	ID       string
	Check    string
	Severity string
	Detail   string
	Nodes    []string
	Refs     []string
}

// attributableTo reports whether f names an entity in the changeset's blast
// radius: one of the nodes it touches, or one of the carriers/guests
// ComputeImpact attributed to it.
func (f ObservedFinding) attributableTo(g *autoRollbackGuard) bool {
	for _, n := range f.Nodes {
		if g.nodes[n] {
			return true
		}
	}
	for _, r := range f.Refs {
		if g.refs[r] {
			return true
		}
	}
	return false
}

// ApplyOptions carries the per-apply options that are NOT part of the T-2602
// fan-out strategy. It is a separate struct precisely so `applyStrategy`
// keeps meaning one thing (how the apply fans out across nodes) — this is a
// property of the confirm window that follows, and applies just as much to a
// plain `mode: all` apply.
type ApplyOptions struct {
	// AutoRollbackOnError arms T-2603's finding-triggered rollback for this
	// changeset. nil (the default, and every caller that never heard of this
	// field) means "use the cluster default", which is itself off unless an
	// admin opted in.
	AutoRollbackOnError *bool
}

// AutoRollbackTrigger records WHICH finding rolled a changeset back, on the
// changeset itself (ApplyLog.AutoRollback) as well as in the audit entry — so
// the operator is never told only that "something went wrong".
type AutoRollbackTrigger struct {
	FindingID string `json:"findingId"`
	Check     string `json:"check,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Detail    string `json:"detail,omitempty"`
	// Findings names every new, attributable error finding this cycle
	// carried, not only the one reported as FindingID: telling an operator
	// about one of four simultaneous breakages would be its own kind of lie.
	Findings []string `json:"findings"`
	Nodes    []string `json:"nodes,omitempty"`
	Refs     []string `json:"refs,omitempty"`
	At       int64    `json:"at"`
}

// autoRollbackGuard is one armed changeset's watch: the pre-apply findings
// baseline (rule 2) and the Impact attribution set (rule 3).
type autoRollbackGuard struct {
	// baseline is the findings-stream ID set as of the cycle before the
	// apply. nil means "no cycle observed yet" — the next one becomes the
	// baseline and cannot trigger (rule 4).
	baseline    map[string]bool
	nodes       map[string]bool
	refs        map[string]bool
	changesetID string
	source      string
	armedAt     int64
}

// autoRollbackSourceRequest / ...Default record where the decision to arm
// came from, so the audit trail distinguishes an operator asking for this
// changeset from a cluster-wide default applying to it.
const (
	autoRollbackSourceRequest = "request"
	autoRollbackSourceDefault = "cluster_default"
)

// autoRollbackEnabled resolves the per-changeset flag against the cluster
// default, returning the decision and its provenance.
func (s *Service) autoRollbackEnabled(opts ApplyOptions) (enabled bool, source string) {
	if opts.AutoRollbackOnError != nil {
		return *opts.AutoRollbackOnError, autoRollbackSourceRequest
	}
	return s.autoRollbackDefault, autoRollbackSourceDefault
}

// armAutoRollback registers a guard for cs, capturing the pre-apply findings
// baseline and the Impact attribution set. Called from ApplyWithOptions after
// the changeset has transitioned to applying and BEFORE the first mutation,
// so the baseline is genuinely the state of the world before the change.
//
// A no-op when the flag resolves to off, which is the default: nothing is
// registered, nothing is audited, and no findings cycle can affect the
// changeset at all.
func (s *Service) armAutoRollback(ctx context.Context, cs Changeset, author string, opts ApplyOptions) {
	enabled, source := s.autoRollbackEnabled(opts)
	if !enabled {
		return
	}

	impact := ComputeImpact(cs.Ops, s.inventorySnapshot(), nil, nil, nil)
	g := &autoRollbackGuard{
		changesetID: cs.ID,
		nodes:       map[string]bool{},
		refs:        map[string]bool{},
		source:      source,
		armedAt:     s.now().Unix(),
	}
	for _, n := range impact.Nodes {
		g.nodes[n] = true
	}
	for _, c := range impact.Carriers {
		g.refs[c] = true
	}
	for _, guest := range impact.Guests {
		g.refs[guest.Ref] = true
	}

	s.findMu.Lock()
	if s.seenCycle {
		g.baseline = s.lastFindings
	}
	baselineSize, baselineKnown := len(g.baseline), g.baseline != nil
	s.guards[cs.ID] = g
	s.findMu.Unlock()

	// "Enabling it is audited": the entry records the provenance of the
	// decision and exactly what the guard will and will not react to, so a
	// later rollback can be reasoned about from the audit trail alone.
	s.appendAudit(ctx, author, "changeset.auto_rollback", "armed", cs.ID, map[string]any{
		"source": source, "nodes": impact.Nodes, "carriers": impact.Carriers,
		"baselineFindings": baselineSize, "baselineObserved": baselineKnown,
	})
	s.log.Info("change: arming finding-triggered auto-rollback for the confirm window",
		"changeset_id", cs.ID, "source", source, "nodes", impact.Nodes,
		"baseline_findings", baselineSize, "baseline_observed", baselineKnown)
}

// disarmAutoRollback drops id's guard. Every terminal path calls it, so a
// changeset that has left its confirm window cannot be rolled back a second
// time by a finding arriving afterwards (AC6). The status check in
// fireAutoRollback is the belt to this braces: neither alone is trusted.
func (s *Service) disarmAutoRollback(id string) {
	s.findMu.Lock()
	defer s.findMu.Unlock()
	delete(s.guards, id)
}

// ObserveFindings feeds one findings-engine cycle to the change engine. It is
// the seam T-2603 hangs off: cmd/vnproxd calls it from the engine's per-cycle
// hook with the whole unified stream, and this package decides — entirely on
// its own recorded state — whether any of it means an in-flight change broke
// something.
//
// It is safe to call before any changeset has ever been applied (it simply
// records the cycle), and safe to call concurrently with an apply.
func (s *Service) ObserveFindings(ctx context.Context, observed []ObservedFinding) {
	ids := make(map[string]bool, len(observed))
	for _, f := range observed {
		ids[f.ID] = true
	}

	type pending struct {
		guard   *autoRollbackGuard
		matched []ObservedFinding
	}
	var fire []pending

	s.findMu.Lock()
	for id, g := range s.guards {
		if g.baseline == nil {
			// Rule 4: the first cycle a guard sees with no recorded baseline
			// BECOMES its baseline. Everything looking new because nothing was
			// watching before is not evidence of anything.
			g.baseline = ids
			continue
		}
		var matched []ObservedFinding
		for _, f := range observed {
			if g.baseline[f.ID] {
				continue // rule 2: pre-existing, whatever its severity
			}
			if f.Severity != string(SeverityError) {
				continue // only an error finding is a reason to undo a change
			}
			if !f.attributableTo(g) {
				continue // rule 3: outside this changeset's Impact
			}
			matched = append(matched, f)
		}
		if len(matched) == 0 {
			continue
		}
		// Rule 5: claim the guard under the same lock that decided it matched,
		// so a cycle arriving while the rollback runs cannot start a second one.
		delete(s.guards, id)
		fire = append(fire, pending{guard: g, matched: matched})
	}
	// The newest cycle becomes the reference for nothing except a guard armed
	// from here on: an armed guard keeps the baseline it was armed with.
	s.lastFindings = ids
	s.seenCycle = true
	s.findMu.Unlock()

	// Deterministic order, so a cycle that trips two changesets at once
	// resolves them in a reproducible one.
	sort.Slice(fire, func(i, j int) bool { return fire[i].guard.changesetID < fire[j].guard.changesetID })
	for _, p := range fire {
		s.fireAutoRollback(ctx, p.guard, p.matched)
	}
}

// fireAutoRollback rolls one changeset back because of matched, recording the
// finding that caused it on the changeset and in the audit trail before the
// restore runs — so the evidence survives even a restore that itself fails.
//
// It runs with NO lock held: it takes applyMu (via the rollback paths) itself,
// and the guard is already claimed.
func (s *Service) fireAutoRollback(ctx context.Context, g *autoRollbackGuard, matched []ObservedFinding) {
	cs, err := s.Get(ctx, g.changesetID)
	if err != nil {
		s.log.Error("change: finding-triggered rollback: loading changeset", "changeset_id", g.changesetID, "error", err)
		return
	}

	// T-1704 single-writer fence, for the same reason autoRollback and the
	// canary hold have one: a demoted former-active must never drive an
	// unattended rollback the current lease-term holder owns.
	if !s.mayLead() {
		s.log.Info("change: finding-triggered rollback skipped — not the current HA leader", "changeset_id", g.changesetID)
		return
	}

	state, paused, serr := s.StagedApplyState(ctx, g.changesetID)
	if serr != nil {
		s.log.Error("change: finding-triggered rollback: reading staged-apply state", "changeset_id", g.changesetID, "error", serr)
		paused = false
	}
	// AC6: the window has to still be open. An `applying` changeset with no
	// recorded pause is a genuine mid-flight apply — the executor owns it and
	// will roll it back itself if a step fails; a committed/rolled_back/failed
	// one is history.
	inWindow := cs.Status == StatusAwaitingConfirm || (cs.Status == StatusApplying && paused)
	if !inWindow {
		s.log.Info("change: a finding matched a changeset that has already left its confirm window; not rolling anything back",
			"changeset_id", g.changesetID, "status", string(cs.Status), "finding_id", matched[0].ID)
		return
	}

	trigger := newAutoRollbackTrigger(matched, s.now().Unix())
	s.recordAutoRollbackTrigger(ctx, &cs, trigger)
	s.appendAudit(ctx, systemRollbackActor, "changeset.auto_rollback", "finding_triggered", g.changesetID, map[string]any{
		"findingId": trigger.FindingID, "findings": trigger.Findings, "check": trigger.Check,
		"severity": trigger.Severity, "detail": trigger.Detail, "nodes": trigger.Nodes, "refs": trigger.Refs,
		"source": g.source, "armedAt": g.armedAt, "stagedApply": paused,
	})
	s.log.Warn("change: a new error finding on an entity this changeset touched; rolling it back inside the confirm window",
		"changeset_id", g.changesetID, "finding_id", trigger.FindingID, "check", trigger.Check,
		"staged_apply", paused, "detail", trigger.Detail)

	reason := autoRollbackReason(trigger)
	if paused {
		// T-2602 interaction (AC7): a trigger during a canary hold ABORTS the
		// sequence — restoring exactly the stages that ran and contacting no
		// pending node — rather than rolling back a plan half of which was
		// never applied. AbortStagedApply is the only entry point that knows
		// that difference.
		if _, aerr := s.AbortStagedApply(ctx, g.changesetID, systemRollbackActor, reason, nil); aerr != nil {
			s.log.Error("change: aborting a staged apply on a finding trigger", "changeset_id", g.changesetID,
				"applied_nodes", state.AppliedNodes, "error", aerr)
		}
		return
	}

	s.applyMu.Lock()
	s.cancelTimerLocked(g.changesetID)
	// The findings cycle carries no user session (it is a background loop), so
	// no PVEGateway is passed: doRollbackLocked falls back to the ticket sealed
	// at apply time exactly as the commit-confirm-timeout path does.
	plan, rbErr := s.doRollbackLocked(ctx, &cs, systemRollbackActor, ChangeOpUnattendedRevert, nil)
	s.applyMu.Unlock()
	if rbErr != nil {
		s.log.Error("change: finding-triggered rollback did not fully restore", "changeset_id", g.changesetID, "error", rbErr)
	}
	s.refreshAfterTerminal(ctx, plan)
}

// newAutoRollbackTrigger picks the reported finding deterministically (the
// lowest stable ID — the findings stream's own canonical order) while naming
// every match.
func newAutoRollbackTrigger(matched []ObservedFinding, at int64) AutoRollbackTrigger {
	sorted := append([]ObservedFinding(nil), matched...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	ids := make([]string, 0, len(sorted))
	for _, f := range sorted {
		ids = append(ids, f.ID)
	}
	primary := sorted[0]
	return AutoRollbackTrigger{
		FindingID: primary.ID, Check: primary.Check, Severity: primary.Severity, Detail: primary.Detail,
		Nodes: primary.Nodes, Refs: primary.Refs, Findings: ids, At: at,
	}
}

func autoRollbackReason(t AutoRollbackTrigger) string {
	if t.Detail != "" {
		return fmt.Sprintf("a new error finding (%s) appeared on an entity this changeset touched: %s", t.FindingID, t.Detail)
	}
	return fmt.Sprintf("a new error finding (%s) appeared on an entity this changeset touched", t.FindingID)
}

// recordAutoRollbackTrigger persists the trigger onto the changeset's apply
// log BEFORE the restore runs, so "which finding caused this" survives even a
// rollback that itself fails. It updates cs IN PLACE as well as the store:
// doRollbackScopedLocked re-marshals the apply log from the Changeset value it
// is handed, so a caller holding a pre-write copy would otherwise overwrite
// the record it just made. Best-effort otherwise — a store hiccup here must
// not stop the rollback, and the audit entry carries the same evidence anyway.
func (s *Service) recordAutoRollbackTrigger(ctx context.Context, cs *Changeset, trigger AutoRollbackTrigger) {
	log := decodeApplyLog(cs.ApplyLog)
	t := trigger
	log.AutoRollback = &t
	logJSON, err := json.Marshal(log)
	if err != nil {
		s.log.Error("change: marshaling the auto-rollback trigger", "changeset_id", cs.ID, "error", err)
		return
	}
	cs.ApplyLog = logJSON
	if err := s.updateApplyLog(ctx, cs.ID, log); err != nil {
		s.log.Error("change: recording the auto-rollback trigger on the changeset", "changeset_id", cs.ID, "error", err)
	}
}

// AutoRollbackTriggerFor returns the finding-triggered rollback recorded on
// id's apply log, if any — the read side of "the operator is never told only
// that something went wrong".
func (s *Service) AutoRollbackTriggerFor(ctx context.Context, id string) (AutoRollbackTrigger, bool, error) {
	cs, err := s.Get(ctx, id)
	if err != nil {
		return AutoRollbackTrigger{}, false, err
	}
	var log ApplyLog
	if len(cs.ApplyLog) > 0 {
		if err := json.Unmarshal(cs.ApplyLog, &log); err != nil {
			return AutoRollbackTrigger{}, false, fmt.Errorf("change: decoding apply log for changeset %s: %w", id, err)
		}
	}
	if log.AutoRollback == nil {
		return AutoRollbackTrigger{}, false, nil
	}
	return *log.AutoRollback, true, nil
}

// FindingNodes extracts the node names a finding's refs name, for a caller
// (cmd/vnproxd's canary health checker) that must decide whether a finding is
// attributable to a node set without re-implementing ref parsing.
func FindingNodes(f ObservedFinding) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(f.Nodes)+len(f.Refs))
	for _, n := range f.Nodes {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, r := range f.Refs {
		ref, err := inventory.ParseRef(r)
		if err != nil || ref.Node == "" || seen[ref.Node] {
			continue
		}
		seen[ref.Node] = true
		out = append(out, ref.Node)
	}
	sort.Strings(out)
	return out
}
