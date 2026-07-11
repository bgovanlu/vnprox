package change

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// executor runs one apply attempt for a changeset and, on any step failure,
// rolls back every step that had already taken effect so the system
// converges back to the pre-apply state (docs/features/change-management.md
// §4). It is a small value bundling the per-apply inputs so the step methods
// don't each need a long parameter list; Service.applyPlan constructs one.
type executor struct {
	pveGW   PVEGateway
	svc     *Service
	pre     map[string]string
	log     *ApplyLog
	stageIx map[string]int
	loadIx  map[string]int
	// fwPre caches each fw target's pre-mutation snapshot (T-502's
	// same-request rollback source — see PVEGateway's doc comment on the
	// unattended-rollback limitation), keyed by Ref.String(), populated
	// lazily the first time that target's StepFwApply step runs.
	// undoFwTargets restores every key present here regardless of whether
	// its StepFwApply step fully completed: the snapshot is taken before
	// the step's *first* op, so a step that errored partway through still
	// needs the same restore as one that ran to completion.
	fwPre    map[string]string
	plan     Plan
	cs       Changeset
	deadline int64 // unix; the commit-confirm deadline every node's local timer (T-304) is armed with
}

// newExecutor builds an executor with a fresh apply log (all steps pending)
// and the per-node stage/reload step-index maps rollback needs. deadline is
// the commit-confirm deadline (computed once, up front, by Apply) that every
// node's local rollback timer (T-304) is armed with as its reload step runs
// — the same absolute instant the coordinator's own bookkeeping timer uses,
// so every node's safety net expires at the same wall-clock time regardless
// of how long earlier nodes' steps took.
func (s *Service) newExecutor(cs Changeset, plan Plan, pre []snapshotFile, pveGW PVEGateway, deadline int64) *executor {
	preByNode := make(map[string]string, len(pre))
	for _, f := range pre {
		preByNode[f.Node] = f.Content
	}
	steps := make([]StepLog, len(plan.Steps))
	stageIx := map[string]int{}
	loadIx := map[string]int{}
	for i, st := range plan.Steps {
		steps[i] = StepLog{Index: i, Kind: st.Kind, Node: st.Node, Summary: st.Summary, Status: StepPending}
		switch st.Kind {
		case StepStageFile:
			stageIx[st.Node] = i
		case StepReload:
			loadIx[st.Node] = i
		}
	}
	return &executor{
		svc: s, cs: cs, plan: plan, pre: preByNode, pveGW: pveGW, deadline: deadline,
		log:     &ApplyLog{Steps: steps},
		stageIx: stageIx, loadIx: loadIx, fwPre: map[string]string{},
	}
}

// run executes every plan step in order. On the first step error it records
// the failed step, marks the remainder skipped, rolls back what completed,
// and returns the error. On full success it returns a nil error and an apply
// log with every step StepOK.
func (e *executor) run(ctx context.Context) error {
	for i := range e.plan.Steps {
		e.log.Steps[i].StartedAt = e.svc.now().Unix()
		err := e.execStep(ctx, e.plan.Steps[i])
		e.log.Steps[i].EndedAt = e.svc.now().Unix()
		if err != nil {
			e.log.Steps[i].Status = StepFailed
			e.log.Steps[i].Error = err.Error()
			fi := i
			e.log.FailedStep = &fi
			for j := i + 1; j < len(e.log.Steps); j++ {
				e.log.Steps[j].Status = StepSkipped
			}
			e.rollbackAfterFailure(ctx)
			return fmt.Errorf("change: apply step %d (%s) failed: %w", i, e.plan.Steps[i].Kind, err)
		}
		e.log.Steps[i].Status = StepOK
	}
	return nil
}

// execStep dispatches one step to its concrete action.
func (e *executor) execStep(ctx context.Context, st Step) error {
	switch st.Kind {
	case StepStageFile:
		// Reuse the pre-apply snapshot's content already captured for this
		// node instead of a second ReadInterfaces call: they're the same
		// read (nothing has mutated node yet), and for a peer node routed
		// over HTTP a second identical GET within the same signing-clock
		// second would collide with the first in the HMAC replay cache
		// (internal/peer's replay protection keys purely on the signed
		// request being byte-identical) — T-304 exposed this by making
		// ReadInterfaces sometimes a real network call instead of always a
		// free in-process one.
		content, err := e.svc.computeStagedFile(e.pre[st.Node], st.Node, e.cs.Ops, st.OpIdx, e.cs.ID)
		if err != nil {
			return err
		}
		return e.svc.nodes.StageInterfaces(ctx, st.Node, content)
	case StepReload:
		// T-304: arm this node's own local rollback timer *before* the one
		// mutating call in its stage->reload pair (docs/features/
		// change-management.md §4: "each node arms its own local timer at
		// step start"), so the node's safety never depends on the
		// coordinator (or the network to it) surviving past this point. If
		// arming fails — most likely because the node is unreachable right
		// now — the reload never runs: an un-armed node must never be
		// reloaded, exactly the "peer dies before its steps start" abort
		// path (T-304 card AC3).
		if e.svc.nodeTimers != nil {
			if _, err := e.svc.nodeTimers.ArmTimer(ctx, e.cs.ID, st.Node, e.pre[st.Node], e.deadline); err != nil {
				return fmt.Errorf("arming local rollback timer on %s: %w", st.Node, err)
			}
		}
		return e.svc.nodes.ReloadInterfaces(ctx, st.Node)
	case StepSDNApply:
		if e.pveGW == nil {
			return fmt.Errorf("no PVE gateway available for sdn.apply (no user session)")
		}
		return e.pveGW.ApplySDN(ctx)
	case StepFwApply:
		return e.execFwApply(ctx, st)
	case StepFwVerify:
		return e.execFwVerify(ctx, st)
	default:
		return fmt.Errorf("unknown step kind %q", st.Kind)
	}
}

// execFwApply runs every op targeting one firewall ruleset (T-502). Before
// the first mutating call it snapshots the target's current content
// (e.fwPre) for same-request rollback (see PVEGateway's doc comment on why
// this can't also cover the unattended commit-confirm-timeout/crash-restart
// paths), then executes each op in order. An fw.rule.move op with a
// non-nil Expect is revalidated against a live re-fetch of its FromPos
// immediately before moving — acceptance criterion 3's "no silent
// misplacement" guarantee.
func (e *executor) execFwApply(ctx context.Context, st Step) error {
	if e.pveGW == nil {
		return fmt.Errorf("no PVE gateway available for firewall ops (no user session)")
	}
	target, err := inventory.ParseRef(st.Target)
	if err != nil {
		return fmt.Errorf("change: parsing firewall step target %q: %w", st.Target, err)
	}
	if _, captured := e.fwPre[st.Target]; !captured {
		pre, err := e.pveGW.SnapshotFirewallScope(ctx, target)
		if err != nil {
			return fmt.Errorf("change: snapshotting firewall scope %s before apply: %w", target, err)
		}
		e.fwPre[st.Target] = pre
	}
	for _, idx := range st.OpIdx {
		op := e.cs.Ops[idx]
		if move, ok := op.Params.(*FwRuleMoveParams); ok && move.Expect != nil {
			live, err := e.pveGW.FirewallRuleFields(ctx, target, move.FromPos)
			if err != nil {
				return fmt.Errorf("change: revalidating firewall rule position before move: %w", err)
			}
			if !live.Equal(*move.Expect) {
				return &ErrFwPositionChanged{Ref: target, Pos: move.FromPos, Want: *move.Expect, Got: live}
			}
		}
		if err := e.pveGW.ApplyFwOp(ctx, op); err != nil {
			return fmt.Errorf("change: applying %s to %s: %w", op.Type, target, err)
		}
	}
	return nil
}

// execFwVerify implements docs/features/firewall.md §3's post-apply
// verification: confirm st.Node's pve-firewall compiled the just-applied
// change cleanly, surfacing the compile error as the step's failure
// otherwise (rather than silently reporting a green apply for a ruleset
// pve-firewall itself rejected).
func (e *executor) execFwVerify(ctx context.Context, st Step) error {
	if e.pveGW == nil {
		return fmt.Errorf("no PVE gateway available for firewall verification (no user session)")
	}
	status, err := e.pveGW.FirewallCompileStatus(ctx, st.Node)
	if err != nil {
		return fmt.Errorf("change: checking firewall compile status on %s: %w", st.Node, err)
	}
	if !status.OK {
		msg := status.Message
		if msg == "" {
			msg = "pve-firewall reported a non-clean compile status"
		}
		return fmt.Errorf("change: firewall did not compile cleanly on %s: %s", st.Node, msg)
	}
	return nil
}

// rollbackAfterFailure converges every affected node back to its pre-apply
// file state after a mid-apply failure: a node whose reload had already
// committed a change is restored from the pre-snapshot and reloaded; a node
// that was only staged (or whose reload/stage was the failing step) has its
// staged file discarded. It also reverts any firewall ruleset whose
// StepFwApply step had already completed (T-502's same-request rollback —
// see PVEGateway's doc comment). Rollback is best-effort across
// nodes/targets — an error restoring one is logged but does not abort
// restoring the others.
func (e *executor) rollbackAfterFailure(ctx context.Context) {
	nodes := e.plan.affectedNodes()
	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		reloadIdx, hasReload := e.loadIx[node]
		committed := hasReload && e.log.Steps[reloadIdx].Status == StepOK
		e.undoNode(ctx, node, committed)
	}
	e.undoFwTargets(ctx)
}

// undoFwTargets reverts every fw ruleset target whose StepFwApply step
// reached StepOK, restoring the content execFwApply snapshotted just
// before its first mutating call. Targets never reached (or whose apply
// step itself is the one that failed, in which case PVE-side state may be
// partially mutated by whichever op within that step errored — the mock
// and real PVE both apply each rule/alias/ipset/group op atomically per
// call, so "this step failed" means zero or more of its ops already took
// effect; restoring from the pre-step snapshot converges either way) are
// still restored as long as e.fwPre captured something for them, since a
// snapshot is taken before the *first* op in the step runs, not after the
// whole step succeeds.
func (e *executor) undoFwTargets(ctx context.Context) {
	if e.pveGW == nil {
		return
	}
	for _, target := range e.plan.fwTargets() {
		key := target.String()
		pre, captured := e.fwPre[key]
		if !captured {
			continue
		}
		rb := RollbackLog{
			Node:    target.Node,
			At:      e.svc.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore firewall scope %s from pre-apply snapshot", target),
		}
		if err := e.pveGW.RestoreFirewallScope(ctx, target, pre); err != nil {
			rb.Status = StepFailed
			rb.Error = err.Error()
		}
		e.log.Rollback = append(e.log.Rollback, rb)
	}
}

// undoNode restores one node. If committed is true, the node's live config
// was changed by a successful reload, so its pre-apply file is re-staged and
// reloaded (byte-identical restore); otherwise only its staged-but-unapplied
// file is discarded.
func (e *executor) undoNode(ctx context.Context, node string, committed bool) {
	rb := RollbackLog{Node: node, At: e.svc.now().Unix(), Status: StepOK}
	if committed {
		rb.Summary = fmt.Sprintf("Restore %s on %s from pre-apply snapshot and reload", interfacesPath, node)
		if err := e.svc.restoreNode(ctx, node, e.pre[node]); err != nil {
			rb.Status = StepFailed
			rb.Error = err.Error()
		}
	} else {
		rb.Summary = fmt.Sprintf("Discard staged %s on %s", interfacesPath, node)
		if err := e.svc.nodes.DiscardStaged(ctx, node); err != nil {
			rb.Status = StepFailed
			rb.Error = err.Error()
		}
	}
	e.log.Rollback = append(e.log.Rollback, rb)
}

// restoreNode re-stages content as node's interfaces file and reloads,
// returning the node to a known-good state. Used by both the mid-apply
// failure path and the confirm-timeout / manual rollback of an applied
// changeset.
func (s *Service) restoreNode(ctx context.Context, node, content string) error {
	if err := s.nodes.StageInterfaces(ctx, node, content); err != nil {
		return fmt.Errorf("change: staging restore for %s on node %s: %w", interfacesPath, node, err)
	}
	if err := s.nodes.ReloadInterfaces(ctx, node); err != nil {
		return fmt.Errorf("change: reloading restore for %s on node %s: %w", interfacesPath, node, err)
	}
	return nil
}

// restoreAll converges every node the plan touched back to the pre-apply
// snapshot (the confirm-timeout and manual-rollback path, where every reload
// had succeeded). It is best-effort: it attempts every node and returns the
// per-node rollback log plus whether any node failed to restore.
func (s *Service) restoreAll(ctx context.Context, plan Plan, pre []snapshotFile) ([]RollbackLog, bool) {
	preByNode := make(map[string]string, len(pre))
	for _, f := range pre {
		preByNode[f.Node] = f.Content
	}
	nodes := plan.affectedNodes()
	logs := make([]RollbackLog, 0, len(nodes))
	anyFailed := false
	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		rb := RollbackLog{
			Node:    node,
			At:      s.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore %s on %s from pre-apply snapshot and reload", interfacesPath, node),
		}
		if err := s.restoreNode(ctx, node, preByNode[node]); err != nil {
			rb.Status = StepFailed
			rb.Error = err.Error()
			anyFailed = true
		}
		logs = append(logs, rb)
	}
	return logs, anyFailed
}

// computeStagedFile applies node's ops (opIdx into ops) via the T-204 ifaces
// mutators to before (its current interfaces file content — the caller
// passes the pre-apply snapshot already captured for node rather than this
// function re-reading it: they're the same read, and issuing it twice would
// be both wasteful and, for a peer node routed over HTTP, liable to collide
// with itself in internal/peer's HMAC replay cache if both calls land in the
// same signing-clock second) and returns the new rendered file content to
// stage — the same parse→mutate→render path ifaces.DiffChangeset uses, so
// the staged file is byte-identical to the diff the user reviewed.
func (s *Service) computeStagedFile(before, node string, ops []Op, opIdx []int, changesetID string) (string, error) {
	f, err := host.ParseInterfaces([]byte(before))
	if err != nil {
		return "", fmt.Errorf("change: parsing %s on node %s: %w", interfacesPath, node, err)
	}
	sub := make([]Op, 0, len(opIdx))
	for _, i := range opIdx {
		sub = append(sub, ops[i])
	}
	ifOps, err := changeOpsToIfaces(sub)
	if err != nil {
		return "", err
	}
	if err := ifaces.MutateAll(f, ifOps, changesetID); err != nil {
		return "", fmt.Errorf("change: mutating %s on node %s: %w", interfacesPath, node, err)
	}
	return f.Render(), nil
}

// changeOpsToIfaces adapts internal/change ops into internal/change/ifaces
// ops by re-marshaling through the shared {op,target,params} wire shape
// (docs/api.md) and decoding with ifaces.DecodeOps — the adapter T-204's
// op.go documents ("a internal/change.Op -> ifaces.Op adapter only needs to
// re-marshal"). Field names align (camelCase) across both packages'
// param structs, so the round trip is lossless for the fields the file
// mutators consume. (The one asymmetry — change's pointer-clear semantics vs.
// ifaces' explicit Remove* flags — is noted in the T-205 report; it does not
// affect the create/set-heavy ops the engine executes today.)
func changeOpsToIfaces(ops []Op) ([]ifaces.Op, error) {
	raw, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("change: marshaling ops for file mutation: %w", err)
	}
	ifOps, err := ifaces.DecodeOps(raw)
	if err != nil {
		return nil, fmt.Errorf("change: adapting ops to file mutators: %w", err)
	}
	return ifOps, nil
}
