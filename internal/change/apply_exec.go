// SPDX-License-Identifier: Apache-2.0

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
	// fwPre caches each fw target's pre-mutation snapshot — the executor's
	// same-request rollback source, keyed by Ref.String(). Since T-1805 it is
	// seeded from the persisted pre-apply snapshot (fwStateSnapshotPath), which
	// is also what the unattended commit-confirm-timeout and crash-recovery
	// reverts restore from; execFwApply's lazy capture remains as a fallback
	// for a snapshot that carries no firewall file.
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
	// tcMirrorPre is the per-node tc.mirror pre-apply state (TcMirrorGateway.
	// SnapshotTcMirror), the restore target for a mid-apply failure that had
	// already run a tc.mirror step (T-4014). Populated from the pre-snapshot's
	// tcMirrorStateSnapshotPath file; hasTcMirrorPre is false for a changeset
	// with no tc.mirror.* ops.
	tcMirrorPre map[string]string
	// wgPre is the per-node WireGuard pre-apply state (WGGateway.SnapshotWg),
	// the restore target for a mid-apply failure that had already run a wg
	// step (T-1401). Populated from the pre-snapshot's wgStateSnapshotPath
	// file; hasWgPre is false for a changeset with no wg.* ops.
	wgPre map[string]string
	// switchPre is the per-switch-port pre-image (SwitchGateway.SnapshotSwitchPort),
	// the restore target for a mid-apply failure that had already run a switch
	// step (T-1205). Populated from the pre-snapshot's switchStateSnapshotPath
	// file; hasSwitchPre is false for a changeset with no switch.port.* ops.
	switchPre map[string]string
	// rollbackNodes is the node set rollbackAfterFailure converges back to the
	// pre-apply state, in plan.affectedNodes() order. For an ordinary
	// all-at-once apply it IS plan.affectedNodes() — every node the plan
	// touches — which is what newExecutor sets it to, so nothing about the
	// pre-T-2602 rollback changes.
	//
	// A staged (canary) apply narrows it to the nodes whose stages actually
	// ran. That narrowing is the whole of T-2602's AC2: a node that was never
	// contacted must not be contacted by the rollback either — not even for a
	// DiscardStaged of a file that was never staged on it.
	rollbackNodes  []string
	plan           Plan
	cs             Changeset
	deadline       int64 // unix; the commit-confirm deadline every node's local timer (T-304) is armed with
	hasSDNPre      bool
	hasQosPre      bool
	hasTcMirrorPre bool
	hasWgPre       bool
	hasSwitchPre   bool
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
	// T-1805: the firewall pre-image now lives in the persisted pre-snapshot,
	// so seed the executor's own same-request rollback cache from it rather
	// than re-reading each scope from PVE at step time. execFwApply's lazy
	// capture stays as the fallback for a plan whose snapshot has no firewall
	// file (nothing produces one today, but the guard costs nothing and keeps
	// that method self-sufficient).
	fwPre, _ := fwStateFromSnapshot(pre)
	if fwPre == nil {
		fwPre = map[string]string{}
	}
	qosPre, hasQosPre := qosStateFromSnapshot(pre)
	tcMirrorPre, hasTcMirrorPre := tcMirrorStateFromSnapshot(pre)
	wgPre, hasWgPre := wgStateFromSnapshot(pre)
	switchPre, hasSwitchPre := switchStateFromSnapshot(pre)
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
		qosPre: qosPre, hasQosPre: hasQosPre, tcMirrorPre: tcMirrorPre, hasTcMirrorPre: hasTcMirrorPre, wgPre: wgPre, hasWgPre: hasWgPre,
		switchPre: switchPre, hasSwitchPre: hasSwitchPre, pveGW: pveGW, deadline: deadline,
		log:           &ApplyLog{Steps: steps},
		rollbackNodes: plan.affectedNodes(),
		stageIx:       stageIx, loadIx: loadIx, fwPre: fwPre,
	}
}

// seedLog folds a previous stage's apply log into this executor's fresh one
// (T-2602): step statuses/timings/errors and the rollback actions already
// recorded. It is what makes a staged sequence produce ONE apply log rather
// than one per stage, and — load-bearing — what lets undoNode see that a
// canary node's reload already committed, so a failure in the remaining
// stage restores the canary nodes too rather than only discarding their
// (long-gone) staged file.
func (e *executor) seedLog(prev ApplyLog) {
	for i := range prev.Steps {
		if i >= len(e.log.Steps) {
			break
		}
		if prev.Steps[i].Status == StepPending {
			continue
		}
		e.log.Steps[i] = prev.Steps[i]
	}
	e.log.Rollback = append(e.log.Rollback, prev.Rollback...)
	e.log.NodeTimers = mergeNodeTimerLogs(e.log.NodeTimers, prev.NodeTimers)
}

// run executes every plan step in order. On the first step error it records
// the failed step, marks the remainder skipped, rolls back what completed,
// and returns the error. On full success it returns a nil error and an apply
// log with every step StepOK.
func (e *executor) run(ctx context.Context) error {
	return e.runSteps(ctx, allStepIndexes(e.plan))
}

// runSteps executes the given plan step indexes, in the order given. run
// above passes every index in plan order, which is the only thing that ever
// happened before T-2602; a staged apply passes one stage's indexes at a
// time.
//
// On the first step error it records the failed step, marks every step that
// has not run SKIPPED, rolls back what completed, and returns the error. The
// skip rule is written as "every step still pending" rather than "every
// index after the failing one" so it stays correct for a stage whose indexes
// are not contiguous; for the contiguous all-at-once case the two are the
// same set, which is why the pre-existing failure tests are unaffected.
func (e *executor) runSteps(ctx context.Context, idxs []int) error {
	for _, i := range idxs {
		e.log.Steps[i].StartedAt = e.svc.now().Unix()
		err := e.execStep(ctx, i)
		e.log.Steps[i].EndedAt = e.svc.now().Unix()
		if err != nil {
			e.log.Steps[i].Status = StepFailed
			e.log.Steps[i].Error = err.Error()
			fi := i
			e.log.FailedStep = &fi
			for j := range e.log.Steps {
				if e.log.Steps[j].Status == StepPending {
					e.log.Steps[j].Status = StepSkipped
				}
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
	case StepSwitchApply:
		if e.svc.switches == nil {
			return fmt.Errorf("no switch gateway available for switch op (switch push not wired on this daemon)")
		}
		if len(st.OpIdx) != 1 {
			return fmt.Errorf("switch_apply step must reference exactly one op, got %d", len(st.OpIdx))
		}
		return e.svc.switches.ApplySwitchOp(ctx, e.cs.Ops[st.OpIdx[0]])

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

	case StepTcMirrorApply:
		if e.svc.tcMirror == nil {
			return fmt.Errorf("no tc.mirror gateway available for tc.mirror op (tc.mirror not wired on this daemon)")
		}
		if len(st.OpIdx) != 1 {
			return fmt.Errorf("tc_mirror_apply step must reference exactly one op, got %d", len(st.OpIdx))
		}
		return e.svc.tcMirror.ApplyTcMirrorOp(ctx, e.cs.Ops[st.OpIdx[0]])
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
// the first mutating call it ensures the target's pre-mutation content is
// cached in e.fwPre for same-request rollback — normally already seeded from
// the persisted pre-apply snapshot (T-1805), captured live here otherwise —
// then executes each op in order. An fw.rule.move op with a
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
// same-request rollback; the unattended paths restore the same content from
// the persisted pre-apply snapshot instead, see restoreFwState). Rollback is
// best-effort across nodes/targets/SDN config — an error restoring one is
// logged but does not abort restoring the rest.
func (e *executor) rollbackAfterFailure(ctx context.Context) {
	if e.hasSDNPre && e.anySDNStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreSDN(ctx, e.pveGW, e.sdnPre))
	}

	// e.rollbackNodes is plan.affectedNodes() for an ordinary apply and the
	// executed stages' nodes for a staged one — see its field doc.
	nodes := e.rollbackNodes
	inScope := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		inScope[n] = true
	}
	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		reloadIdx, hasReload := e.loadIx[node]
		committed := hasReload && e.log.Steps[reloadIdx].Status == StepOK
		e.undoNode(ctx, node, committed)
	}
	e.rollbackIpamSteps(ctx)
	e.undoFwTargets(ctx, inScope)
	if e.hasQosPre && e.anyQosStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreQosState(ctx, scopedToNodes(e.qosPre, inScope))...)
	}
	if e.hasTcMirrorPre && e.anyTcMirrorStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreTcMirrorState(ctx, scopedToNodes(e.tcMirrorPre, inScope))...)
	}
	if e.hasWgPre && e.anyWgStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreWgState(ctx, scopedToNodes(e.wgPre, inScope))...)
	}
	if e.hasSwitchPre && e.anySwitchStepSucceeded() {
		e.log.Rollback = append(e.log.Rollback, e.svc.restoreSwitchState(ctx, e.switchPre)...)
	}
}

// scopedToNodes narrows a per-node pre-apply state map to the nodes this
// rollback is allowed to touch. For an ordinary apply every node in the map
// is in scope (rollbackNodes is the plan's whole node set), so the returned
// map has exactly the same entries and the pre-T-2602 behaviour is
// unchanged. For a staged apply it drops the nodes that were never
// contacted.
func scopedToNodes(state map[string]string, inScope map[string]bool) map[string]string {
	out := make(map[string]string, len(state))
	for node, v := range state {
		if inScope[node] {
			out[node] = v
		}
	}
	return out
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

// anyTcMirrorStepSucceeded reports whether any StepTcMirrorApply step in
// this apply attempt reached StepOK - the gate for whether
// restoreTcMirrorState has anything to undo (T-4014, mirroring
// anyQosStepSucceeded exactly).
func (e *executor) anyTcMirrorStepSucceeded() bool {
	for _, s := range e.log.Steps {
		if s.Kind == StepTcMirrorApply && s.Status == StepOK {
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

// anySwitchStepSucceeded reports whether any StepSwitchApply step in this apply
// attempt reached StepOK — the gate for whether restoreSwitchState has anything
// to undo (if every switch step failed before writing anything, live already
// matches the pre-image).
func (e *executor) anySwitchStepSucceeded() bool {
	for _, s := range e.log.Steps {
		if s.Kind == StepSwitchApply && s.Status == StepOK {
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

// restoreTcMirrorState reconciles every node in state back to its captured
// tc.mirror pre-apply state via the daemon-level TcMirrorGateway (callable
// unattended - no user ticket). Best-effort per node: an error restoring
// one is recorded but does not abort the rest (T-4014, mirroring
// restoreQosState exactly).
func (s *Service) restoreTcMirrorState(ctx context.Context, state map[string]string) []RollbackLog {
	if s.tcMirror == nil {
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
			Summary: fmt.Sprintf("Restore tc.mirror session state on %s from pre-apply snapshot", node),
		}
		if err := s.tcMirror.RestoreTcMirror(ctx, node, state[node]); err != nil {
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

// restoreFwState restores every captured firewall ruleset back to its
// pre-apply pre-image via pveGW (T-1805). Unlike the QoS/WireGuard/switch
// restores above, this one needs a PVE gateway — firewall writes are performed
// with the *user's* ticket (docs/architecture.md §6) and there is no
// daemon-level equivalent. On the unattended paths that gateway comes from the
// ticket sealed at apply time (reverticket.go); a nil gateway there means no
// ticket was available or it had already expired, which is reported as a
// failed rollback action naming the un-reverted scopes rather than passed over
// in silence — the changeset then lands in the distinguishable
// "rollback incomplete" state, the same stance T-1205 takes for an
// unreachable switch.
//
// Best-effort per ruleset: an error restoring one is recorded but does not
// abort the rest.
func (s *Service) restoreFwState(ctx context.Context, pveGW PVEGateway, state map[string]string) []RollbackLog {
	targets := make([]string, 0, len(state))
	for target := range state {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	logs := make([]RollbackLog, 0, len(targets))
	for _, target := range targets {
		rb := RollbackLog{
			At:      s.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore firewall scope %s from pre-apply snapshot", target),
		}
		ref, err := inventory.ParseRef(target)
		switch {
		case err != nil:
			rb.Status, rb.Error = StepFailed, fmt.Sprintf("parsing firewall target %q: %v", target, err)
		case pveGW == nil:
			rb.Status = StepFailed
			rb.Error = "no PVE credential available to restore this firewall scope (no live user session, and no usable sealed revert ticket)"
		default:
			rb.Node = ref.Node
			if rerr := pveGW.RestoreFirewallScope(ctx, ref, state[target]); rerr != nil {
				rb.Status, rb.Error = StepFailed, rerr.Error()
			}
		}
		logs = append(logs, rb)
	}
	return logs
}

// restoreSwitchState re-pushes every captured switch-port pre-image via the
// daemon-level SwitchGateway (callable unattended — no user ticket). Best-effort
// per port: an error restoring one (e.g. the switch is unreachable) is recorded
// as a failed rollback action, which the caller escalates to a distinguishable
// "rollback incomplete" changeset state rather than a clean rolled_back
// (T-1205 AC6).
func (s *Service) restoreSwitchState(ctx context.Context, state map[string]string) []RollbackLog {
	if s.switches == nil {
		return nil
	}
	portRefs := make([]string, 0, len(state))
	for portRef := range state {
		portRefs = append(portRefs, portRef)
	}
	sort.Strings(portRefs)
	logs := make([]RollbackLog, 0, len(portRefs))
	for _, portRef := range portRefs {
		rb := RollbackLog{
			At:      s.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore switch port %s from pre-apply pre-image", portRef),
		}
		if err := s.switches.RestoreSwitchPort(ctx, portRef, state[portRef]); err != nil {
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
//
// inScope narrows the node-scoped targets to the nodes this rollback may
// touch (T-2602). Cluster-scope targets (Node == "") are always in scope:
// they are a PVE-side ruleset with no owning node, so restoring one contacts
// no node at all, and restoring it to its own pre-image is a no-op when its
// step never ran.
func (e *executor) undoFwTargets(ctx context.Context, inScope map[string]bool) {
	if e.pveGW == nil {
		return
	}
	for _, target := range e.plan.fwTargets() {
		if target.Node != "" && !inScope[target.Node] {
			continue
		}
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
