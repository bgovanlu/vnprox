package change

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/host"
)

// executor runs one apply attempt for a changeset and, on any step failure,
// rolls back every step that had already taken effect so the system
// converges back to the pre-apply state (docs/features/change-management.md
// §4). It is a small value bundling the per-apply inputs so the step methods
// don't each need a long parameter list; Service.applyPlan constructs one.
type executor struct {
	pveGW     PVEGateway
	svc       *Service
	pre       map[string]string
	sdnPre    SDNConfig // valid iff hasSDNPre
	log       *ApplyLog
	stageIx   map[string]int
	loadIx    map[string]int
	plan      Plan
	cs        Changeset
	deadline  int64 // unix; the commit-confirm deadline every node's local timer (T-304) is armed with
	hasSDNPre bool
}

// newExecutor builds an executor with a fresh apply log (all steps pending)
// and the per-node stage/reload step-index maps rollback needs. deadline is
// the commit-confirm deadline (computed once, up front, by Apply) that every
// node's local rollback timer (T-304) is armed with as its reload step runs
// — the same absolute instant the coordinator's own bookkeeping timer uses,
// so every node's safety net expires at the same wall-clock time regardless
// of how long earlier nodes' steps took. pre may additionally carry T-402's
// synthetic SDN config snapshot entries (sdn*SnapshotPath) alongside the
// per-node interfaces files; sdnConfigFromSnapshot recovers them.
func (s *Service) newExecutor(cs Changeset, plan Plan, pre []snapshotFile, pveGW PVEGateway, deadline int64) *executor {
	preByNode := make(map[string]string, len(pre))
	for _, f := range pre {
		if f.Node != "" {
			preByNode[f.Node] = f.Content
		}
	}
	sdnPre, hasSDNPre := sdnConfigFromSnapshot(pre)
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
		svc: s, cs: cs, plan: plan, pre: preByNode, sdnPre: sdnPre, hasSDNPre: hasSDNPre, pveGW: pveGW, deadline: deadline,
		log:     &ApplyLog{Steps: steps},
		stageIx: stageIx, loadIx: loadIx,
	}
}

// run executes every plan step in order. On the first step error it records
// the failed step, marks the remainder skipped, rolls back what completed,
// and returns the error. On full success it returns a nil error and an apply
// log with every step StepOK.
func (e *executor) run(ctx context.Context) error {
	for i := range e.plan.Steps {
		e.log.Steps[i].StartedAt = e.svc.now().Unix()
		err := e.execStep(ctx, i)
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

// execStep dispatches the step at plan index i to its concrete action. It
// takes an index (rather than the Step value alone, as it did before T-402)
// so StepSDNApply can record its task's UPID/Node onto e.log.Steps[i] as
// soon as the task starts, for the task-log deep link even on failure.
func (e *executor) execStep(ctx context.Context, i int) error {
	st := e.plan.Steps[i]
	switch st.Kind {
	case StepSDNStage:
		if e.pveGW == nil {
			return fmt.Errorf("no PVE gateway available for sdn stage op (no user session)")
		}
		if len(st.OpIdx) != 1 {
			return fmt.Errorf("sdn_stage step must reference exactly one op, got %d", len(st.OpIdx))
		}
		op := e.cs.Ops[st.OpIdx[0]]
		vnet := ""
		if op.Type == OpSdnSubnetUpdate || op.Type == OpSdnSubnetDelete {
			vnet = resolveSubnetVnet(e.cs.Ops, st.OpIdx[0], e.svc.inventorySnapshot(), op.Target.ID)
		}
		return e.pveGW.SDNStageOp(ctx, op, vnet)

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
		zones := sdnAffectedZones(e.cs.Ops)
		result, err := e.pveGW.ApplySDN(ctx, zones)
		// Record the task identity on this step's log entry as soon as it's
		// known, even on failure (docs/features/sdn.md §4: "failures link
		// straight to the failing node's task log") — a post-apply health
		// failure still ran a real (successful) PVE task worth linking to.
		e.log.Steps[i].TaskUPID = result.UPID
		if result.Node != "" {
			e.log.Steps[i].Node = result.Node
		}
		if err != nil {
			return err
		}
		if zone, node, unhealthy := result.firstUnhealthy(); unhealthy {
			return &ErrSDNZoneUnhealthy{
				Zone: zone, Node: node.Node, Status: node.Status, Detail: node.Detail,
				UPID: result.UPID, TaskNode: result.Node,
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown step kind %q", st.Kind)
	}
}

// rollbackAfterFailure converges every affected node back to its pre-apply
// file state after a mid-apply failure: a node whose reload had already
// committed a change is restored from the pre-snapshot and reloaded; a node
// that was only staged (or whose reload/stage was the failing step) has its
// staged file discarded. It also reverts any SDN mutation this same apply
// attempt already made (T-402's restoreSDN) — this runs synchronously,
// still within the same Apply call that holds the live pveGW, exactly the
// design note in apply_seams.go/PVEGateway's doc comment: SDN's rollback
// has no daemon-level (ticket-less) path the way node-file rollback does,
// so it can only ever happen here or in a manual/auto rollback that itself
// has a gateway (see doRollbackLocked). Rollback is best-effort — an error
// restoring one node/the SDN config is logged but does not abort restoring
// the rest.
func (e *executor) rollbackAfterFailure(ctx context.Context) {
	if e.hasSDNPre && e.anySDNStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreSDN(ctx, e.pveGW, e.sdnPre))
	}

	nodes := e.plan.affectedNodes()
	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		reloadIdx, hasReload := e.loadIx[node]
		committed := hasReload && e.log.Steps[reloadIdx].Status == StepOK
		e.undoNode(ctx, node, committed)
	}
}

// anySDNStepSucceeded reports whether any StepSDNStage/StepSDNApply step in
// this apply attempt reached StepOK — the gate for whether restoreSDN has
// anything to actually undo (if every SDN step was skipped/failed before
// ever mutating PVE, current == pre already and restoreSDN's own PVE
// round-trips would be pure overhead).
func (e *executor) anySDNStepSucceeded() bool {
	for _, s := range e.log.Steps {
		if (s.Kind == StepSDNStage || s.Kind == StepSDNApply) && s.Status == StepOK {
			return true
		}
	}
	return false
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
