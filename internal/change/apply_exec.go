package change

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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
	sdnPre  SDNConfig // valid iff hasSDNPre
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
	fwPre map[string]string
	// qosPre is the per-node QoS pre-apply state (QosGateway.SnapshotQos),
	// the restore target for a mid-apply failure that had already run a
	// qos step (T-1505). Populated from the pre-snapshot's
	// qosStateSnapshotPath file; hasQosPre is false for a changeset with no
	// qos.* ops.
	qosPre map[string]string
	// wgPre is the per-node WireGuard pre-apply state (WGGateway.SnapshotWg),
	// the restore target for a mid-apply failure that had already run a wg
	// step (T-1401). Populated from the pre-snapshot's wgStateSnapshotPath
	// file; hasWgPre is false for a changeset with no wg.* ops.
	wgPre     map[string]string
	plan      Plan
	cs        Changeset
	deadline  int64 // unix; the commit-confirm deadline every node's local timer (T-304) is armed with
	hasSDNPre bool
	hasQosPre bool
	hasWgPre  bool
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
	qosPre, hasQosPre := qosStateFromSnapshot(pre)
	wgPre, hasWgPre := wgStateFromSnapshot(pre)
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
		svc: s, cs: cs, plan: plan, pre: preByNode, sdnPre: sdnPre, hasSDNPre: hasSDNPre,
		qosPre: qosPre, hasQosPre: hasQosPre, wgPre: wgPre, hasWgPre: hasWgPre, pveGW: pveGW, deadline: deadline,
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

	case StepIpamAlloc:
		if e.pveGW == nil {
			return fmt.Errorf("no PVE gateway available for ipam.alloc (no user session)")
		}
		return e.execIpamAlloc(ctx, st)

	case StepWgApply:
		if e.svc.wg == nil {
			return fmt.Errorf("no WireGuard gateway available for wg op (WireGuard not wired on this daemon)")
		}
		if len(st.OpIdx) != 1 {
			return fmt.Errorf("wg_apply step must reference exactly one op, got %d", len(st.OpIdx))
		}
		return e.svc.wg.ApplyWgOp(ctx, e.cs.Ops[st.OpIdx[0]])

	case StepFwApply:
		return e.execFwApply(ctx, st)
	case StepFwVerify:
		return e.execFwVerify(ctx, st)

	case StepQosApply:
		if e.svc.qos == nil {
			return fmt.Errorf("no QoS gateway available for qos op (QoS not wired on this daemon)")
		}
		if len(st.OpIdx) != 1 {
			return fmt.Errorf("qos_apply step must reference exactly one op, got %d", len(st.OpIdx))
		}
		return e.svc.qos.ApplyQosOp(ctx, e.cs.Ops[st.OpIdx[0]])
	default:
		return fmt.Errorf("unknown step kind %q", st.Kind)
	}
}

// execIpamAlloc realizes one ipam.alloc.create/delete op's step: it
// resolves the owning vnet from the target subnet's inventory entity (the
// op only carries the subnet's CIDR, per params_ipam.go's doc comment —
// PVEGateway's IPAM methods are vnet-scoped, mirroring real PVE's own
// vnet-scoped IPAM write route) and dispatches to the matching PVEGateway
// method.
func (e *executor) execIpamAlloc(ctx context.Context, st Step) error {
	op := e.cs.Ops[st.OpIdx[0]]
	vnet, err := e.svc.subnetVnet(op.Target)
	if err != nil {
		return err
	}
	switch p := op.Params.(type) {
	case *IpamAllocCreateParams:
		return e.pveGW.AllocateIPAMAddress(ctx, vnet, op.Target.ID, *p)
	case *IpamAllocDeleteParams:
		return e.pveGW.ReleaseIPAMAddress(ctx, vnet, op.Target.ID, p.CIDR)
	default:
		return fmt.Errorf("change: step %q has unexpected params type %T", StepIpamAlloc, op.Params)
	}
}

// subnetVnet resolves subnetRef (an sdn-subnet Ref, ID == CIDR) to its
// owning vnet name from the current inventory snapshot.
func (s *Service) subnetVnet(subnetRef inventory.Ref) (string, error) {
	ent, ok := s.inventorySnapshot().Get(subnetRef)
	if !ok {
		return "", fmt.Errorf("change: subnet %s not found in inventory", subnetRef)
	}
	sub, ok := ent.(*inventory.SdnSubnet)
	if !ok {
		return "", fmt.Errorf("change: entity %s is not an sdn-subnet", subnetRef)
	}
	return sub.Vnet, nil
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
// staged file discarded. It also reverts any SDN mutation this same apply
// attempt already made (T-402's restoreSDN) — this runs synchronously,
// still within the same Apply call that holds the live pveGW, exactly the
// design note in apply_seams.go/PVEGateway's doc comment: SDN's rollback
// has no daemon-level (ticket-less) path the way node-file rollback does,
// so it can only ever happen here or in a manual/auto rollback that itself
// has a gateway (see doRollbackLocked). It also reverts any firewall
// ruleset whose StepFwApply step had already completed (T-502's
// same-request rollback — see PVEGateway's doc comment). Rollback is
// best-effort across nodes/targets/SDN config — an error restoring one is
// logged but does not abort restoring the rest.
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
	e.rollbackIpamSteps(ctx)
	e.undoFwTargets(ctx)
	if e.hasQosPre && e.anyQosStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreQosState(ctx, e.qosPre)...)
	}
	if e.hasWgPre && e.anyWgStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreWgState(ctx, e.wgPre)...)
	}
}

// anyQosStepSucceeded reports whether any StepQosApply step in this apply
// attempt reached StepOK — the gate for whether restoreQosState has
// anything to undo (if every qos step failed before mutating anything,
// live already matches the pre-state).
func (e *executor) anyQosStepSucceeded() bool {
	for _, s := range e.log.Steps {
		if s.Kind == StepQosApply && s.Status == StepOK {
			return true
		}
	}
	return false
}

// anyWgStepSucceeded reports whether any StepWgApply step in this apply
// attempt reached StepOK — the gate for whether restoreWgState has anything
// to undo (if every wg step failed before mutating anything, live already
// matches the pre-state).
func (e *executor) anyWgStepSucceeded() bool {
	for _, s := range e.log.Steps {
		if s.Kind == StepWgApply && s.Status == StepOK {
			return true
		}
	}
	return false
}

// restoreQosState reconciles every node in state back to its captured QoS
// pre-apply state via the daemon-level QosGateway (callable unattended — no
// user ticket). Best-effort per node: an error restoring one is recorded
// but does not abort the rest (T-1505).
func (s *Service) restoreQosState(ctx context.Context, state map[string]string) []RollbackLog {
	if s.qos == nil {
		return nil
	}
	nodes := make([]string, 0, len(state))
	for node := range state {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	logs := make([]RollbackLog, 0, len(nodes))
	for _, node := range nodes {
		rb := RollbackLog{
			Node:    node,
			At:      s.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore QoS shape state on %s from pre-apply snapshot", node),
		}
		if err := s.qos.RestoreQos(ctx, node, state[node]); err != nil {
			rb.Status = StepFailed
			rb.Error = err.Error()
		}
		logs = append(logs, rb)
	}
	return logs
}

// restoreWgState reconciles every node in state back to its captured
// WireGuard pre-apply state via the daemon-level WGGateway (callable
// unattended — no user ticket). Best-effort per node: an error restoring one
// is recorded but does not abort the rest (T-1401).
func (s *Service) restoreWgState(ctx context.Context, state map[string]string) []RollbackLog {
	if s.wg == nil {
		return nil
	}
	nodes := make([]string, 0, len(state))
	for node := range state {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	logs := make([]RollbackLog, 0, len(nodes))
	for _, node := range nodes {
		rb := RollbackLog{
			Node:    node,
			At:      s.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore WireGuard state on %s from pre-apply snapshot", node),
		}
		if err := s.wg.RestoreWg(ctx, node, state[node]); err != nil {
			rb.Status = StepFailed
			rb.Error = err.Error()
		}
		logs = append(logs, rb)
	}
	return logs
}

// rollbackIpamSteps best-effort undoes every already-succeeded
// ipam.alloc.create step (releasing the just-reserved address) after a
// later step's failure — ipam.alloc.create steps always precede the
// per-node file steps (docs/data-model.md §3's category ordering), so any
// of them that ran are exactly the ones e.log.Steps already marks StepOK
// at this point. ipam.alloc.delete steps cannot be safely auto-reversed:
// the executor does not retain the released allocation's original
// metadata (hostname/MAC), so re-creating it would be a guess — these are
// logged as a failed rollback action naming the address, for manual
// reconciliation, rather than silently left unmentioned.
func (e *executor) rollbackIpamSteps(ctx context.Context) {
	for i := len(e.plan.Steps) - 1; i >= 0; i-- {
		st := e.plan.Steps[i]
		if st.Kind != StepIpamAlloc || e.log.Steps[i].Status != StepOK {
			continue
		}
		op := e.cs.Ops[st.OpIdx[0]]
		rb := RollbackLog{At: e.svc.now().Unix(), Status: StepOK, Summary: "Undo: " + st.Summary}
		switch p := op.Params.(type) {
		case *IpamAllocCreateParams:
			switch e.pveGW {
			case nil:
				rb.Status, rb.Error = StepFailed, "no PVE gateway available to roll back IPAM allocation"
			default:
				vnet, err := e.svc.subnetVnet(op.Target)
				if err != nil {
					rb.Status, rb.Error = StepFailed, err.Error()
				} else if err := e.pveGW.ReleaseIPAMAddress(ctx, vnet, op.Target.ID, p.CIDR); err != nil {
					rb.Status, rb.Error = StepFailed, err.Error()
				}
			}
		case *IpamAllocDeleteParams:
			rb.Status = StepFailed
			rb.Error = fmt.Sprintf("ipam.alloc.delete cannot be automatically rolled back; verify and re-reserve %s manually if still needed", p.CIDR)
		}
		e.log.Rollback = append(e.log.Rollback, rb)
	}
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
