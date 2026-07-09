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
	pveGW   PVEGateway
	svc     *Service
	pre     map[string]string
	log     *ApplyLog
	stageIx map[string]int
	loadIx  map[string]int
	plan    Plan
	cs      Changeset
}

// newExecutor builds an executor with a fresh apply log (all steps pending)
// and the per-node stage/reload step-index maps rollback needs.
func (s *Service) newExecutor(cs Changeset, plan Plan, pre []snapshotFile, pveGW PVEGateway) *executor {
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
		svc: s, cs: cs, plan: plan, pre: preByNode, pveGW: pveGW,
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
		content, err := e.svc.computeStagedFile(ctx, st.Node, e.cs.Ops, st.OpIdx, e.cs.ID)
		if err != nil {
			return err
		}
		return e.svc.nodes.StageInterfaces(ctx, st.Node, content)
	case StepReload:
		return e.svc.nodes.ReloadInterfaces(ctx, st.Node)
	case StepSDNApply:
		if e.pveGW == nil {
			return fmt.Errorf("no PVE gateway available for sdn.apply (no user session)")
		}
		return e.pveGW.ApplySDN(ctx)
	default:
		return fmt.Errorf("unknown step kind %q", st.Kind)
	}
}

// rollbackAfterFailure converges every affected node back to its pre-apply
// file state after a mid-apply failure: a node whose reload had already
// committed a change is restored from the pre-snapshot and reloaded; a node
// that was only staged (or whose reload/stage was the failing step) has its
// staged file discarded. Rollback is best-effort across nodes — an error
// restoring one node is logged but does not abort restoring the others.
func (e *executor) rollbackAfterFailure(ctx context.Context) {
	nodes := e.plan.affectedNodes()
	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		reloadIdx, hasReload := e.loadIx[node]
		committed := hasReload && e.log.Steps[reloadIdx].Status == StepOK
		e.undoNode(ctx, node, committed)
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

// computeStagedFile reads node's current interfaces file, applies that node's
// ops (opIdx into ops) via the T-204 ifaces mutators, and returns the new
// rendered file content to stage — the same parse→mutate→render path
// ifaces.DiffChangeset uses, so the staged file is byte-identical to the diff
// the user reviewed.
func (s *Service) computeStagedFile(ctx context.Context, node string, ops []Op, opIdx []int, changesetID string) (string, error) {
	before, err := s.nodes.ReadInterfaces(ctx, node)
	if err != nil {
		return "", fmt.Errorf("change: reading %s on node %s: %w", interfacesPath, node, err)
	}
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
