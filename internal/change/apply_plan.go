package change

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// StepKind classifies one apply step per docs/architecture.md §4 /
// docs/data-model.md §3's ordering: cluster-scope PVE API calls, then
// per-node interface-file staging, then per-node reload, then sdn.apply
// last.
type StepKind string

const (
	// StepStageFile writes one node's new /etc/network/interfaces to its
	// staged interfaces.new (host writer), realizing that node's node-file
	// ops. It activates nothing on its own.
	StepStageFile StepKind = "stage_file"
	// StepReload applies one node's staged file and reloads the network
	// (ifreload). This is the connectivity-affecting step the commit-confirm
	// window guards.
	StepReload StepKind = "reload"
	// StepSDNApply applies pending cluster SDN config (PUT /cluster/sdn),
	// always last (docs/data-model.md §3).
	StepSDNApply StepKind = "sdn_apply"

	// StepSDNStage realizes one sdn.zone/vnet/subnet create/update/delete
	// op as a cluster-scope PVE API call (docs/data-model.md §3's "(1)
	// cluster-scope PVE API calls", which orders before the per-node file
	// steps and before the trailing sdn.apply). One step per op — unlike
	// StepStageFile, which batches every node-file op for one node into a
	// single staged-file write — because each is its own independent PVE
	// API call with its own success/failure and its own rollback inverse
	// (apply_sdn.go's sdnRestoreOps), not a file render that can only be
	// meaningfully staged as a whole.
	StepSDNStage StepKind = "sdn_stage"

	// StepFwApply executes every fw.* op targeting one firewall ruleset
	// (cluster/node/guest scope) against the PVE firewall API, in the
	// order those ops appear in the changeset (T-502). One step per
	// distinct target ruleset, mirroring StepStageFile/StepReload's
	// "one step per distinct node" grouping.
	StepFwApply StepKind = "fw_apply"
	// StepFwVerify is docs/features/firewall.md §3's post-apply
	// verification: after a node- or guest-scope ruleset's StepFwApply
	// step runs, confirm that node's pve-firewall compiled the change
	// cleanly (surfacing the compile error otherwise). Cluster-scope fw
	// changes have no single node to check this way — see BuildPlan's
	// doc comment — so they get no StepFwVerify.
	StepFwVerify StepKind = "fw_verify"

	// StepWgApply realizes one wg.* op (T-1401): a node-local WireGuard
	// mutation — generate the keypair on the owning node and seal it, write
	// the on-node wg config, and exec wg/wg-quick with a fixed argv array —
	// executed by the daemon-level WGGateway (like the interfaces-file steps,
	// and unlike the ticket-scoped PVEGateway steps, so its rollback works on
	// the unattended commit-confirm-timeout path too). One step per op, in the
	// changeset's own op order, placed after the per-node interface stage/
	// reload pairs (the carrier interface must exist before the tunnel rides
	// on it) and before any trailing sdn.apply.
	StepWgApply StepKind = "wg_apply"
	// StepSwitchApply realizes one switch.port.update op (T-1205): a write to
	// a physical switch port (VLAN membership / description / LACP) via the
	// daemon-level SwitchGateway. Like the interfaces-file steps (and unlike
	// the ticket-scoped PVEGateway steps) it needs no live user session, so its
	// rollback works on the unattended commit-confirm-timeout path too. One
	// step per op. It is placed FIRST — before the per-node interface
	// stage/reload pairs — because switch-side additions (adding a VLAN to a
	// trunk) are additive and don't remove existing connectivity, so applying
	// them ahead of the corresponding node-side change is safe, whereas the
	// reverse ordering could strand the node step behind a not-yet-configured
	// trunk (T-1205 safety analysis: "the plan always applies switch-port steps
	// before node-network steps within one changeset").
	StepSwitchApply StepKind = "switch_apply"

	// StepIpamAlloc realizes one ipam.alloc.create/delete op (T-405): a
	// cluster-scope PVE IPAM plugin write, category (1) in
	// docs/data-model.md §3's ordering — emitted before any per-node file
	// step, one step per op (unlike the grouped-by-node file steps, IPAM
	// writes have no natural per-node grouping and no inter-op ordering
	// requirement, so each op gets its own step for a precise per-op apply
	// log entry).
	StepIpamAlloc StepKind = "ipam_alloc"

	// StepQosApply realizes one qos.shape.create/update/delete op (T-1505):
	// a node-local tc/HTB mutation, executed by the daemon-level QosGateway
	// — like StepReload (and unlike the ticket-scoped PVEGateway steps),
	// this needs no live user session, so its rollback works on the
	// unattended commit-confirm-timeout path too (T-205's existing
	// inverse-order rollback contract). One step per op, in the changeset's
	// own op order, placed after the per-node interface stage/reload pairs
	// (a shape's bridge must already exist) and before any trailing
	// sdn.apply — "a new apply-step kind ordered alongside the existing
	// per-node ifreload step", per this card's task text.
	StepQosApply StepKind = "qos_apply"
)

// nodeFileOpTypes is the subset of the v1 op vocabulary that mutates a node's
// /etc/network/interfaces file — exactly the ops internal/change/ifaces (T-204)
// knows how to turn into file edits.
var nodeFileOpTypes = map[OpType]bool{
	OpIfaceUpdate:      true,
	OpIfaceRename:      true,
	OpIfaceRawReplace:  true,
	OpBondCreate:       true,
	OpBondUpdate:       true,
	OpBondDelete:       true,
	OpBridgeCreate:     true,
	OpBridgeUpdate:     true,
	OpBridgeDelete:     true,
	OpBridgePortAdd:    true,
	OpBridgePortRemove: true,
	OpVlanCreate:       true,
	OpVlanUpdate:       true,
	OpVlanDelete:       true,

	// T-1403's nat.*/route.static.* ops (docs/data-model.md §3): applied via
	// the same interfaces-file post-up/post-down write path as every op
	// above, never a second mutation mechanism.
	OpNatMasqueradeCreate:  true,
	OpNatMasqueradeDelete:  true,
	OpNatPortForwardCreate: true,
	OpNatPortForwardUpdate: true,
	OpNatPortForwardDelete: true,
	OpRouteStaticCreate:    true,
	OpRouteStaticUpdate:    true,
	OpRouteStaticDelete:    true,
	// OpVFProvision (T-1506): applied via the same interfaces-file
	// post-up/post-down write path as every op above — internal/change/
	// ifaces/vfop.go appends a stanza pair to the PF's own existing iface
	// stanza (Target). No new StepKind is needed: the executor's
	// StepStageFile/StepReload machinery already renders+reloads the
	// whole node file for every node-file op targeting that node.
	OpVFProvision: true,
}

// sdnStageOpTypes is the subset of the v1 op vocabulary T-402 realizes as a
// StepSDNStage cluster-scope PVE API call — every sdn.zone/vnet/subnet
// create/update/delete op. sdn.apply itself is handled separately
// (StepSDNApply, always last).
var sdnStageOpTypes = map[OpType]bool{
	OpSdnZoneCreate:   true,
	OpSdnZoneUpdate:   true,
	OpSdnZoneDelete:   true,
	OpSdnVnetCreate:   true,
	OpSdnVnetUpdate:   true,
	OpSdnVnetDelete:   true,
	OpSdnSubnetCreate: true,
	OpSdnSubnetUpdate: true,
	OpSdnSubnetDelete: true,

	// T-1204: PVE stages and applies the SDN DNS plugin config exactly like
	// zones/vnets/subnets, so DNS ops are the same category (1) cluster-scope
	// StepSDNStage call, sharing the single trailing StepSDNApply — never a
	// separate apply path.
	OpSdnDnsZoneCreate:   true,
	OpSdnDnsZoneUpdate:   true,
	OpSdnDnsZoneDelete:   true,
	OpSdnDnsRecordCreate: true,
	OpSdnDnsRecordUpdate: true,
	OpSdnDnsRecordDelete: true,

	// T-3101: a fabric create/update/delete stages and applies through this
	// exact same StepSDNStage/StepSDNApply pair, deliberately — see
	// op.go's OpSdnFabricCreate doc comment on why this must not widen
	// planning/reports/T-3101-followup-01.md's filed gap with a bespoke
	// apply path.
	OpSdnFabricCreate: true,
	OpSdnFabricUpdate: true,
	OpSdnFabricDelete: true,

	// T-3102: a controller create/update/delete stages and applies through
	// this exact same StepSDNStage/StepSDNApply pair too — the same
	// discipline op.go's OpSdnControllerCreate doc comment states.
	OpSdnControllerCreate: true,
	OpSdnControllerUpdate: true,
	OpSdnControllerDelete: true,
}

// wgOpTypes is T-1401's WireGuard op vocabulary: each becomes a StepWgApply
// step executed by the node-local WGGateway.
var wgOpTypes = map[OpType]bool{
	OpWgTunnelCreate: true,
	OpWgTunnelUpdate: true,
	OpWgTunnelDelete: true,
	OpWgPeerAdd:      true,
	OpWgPeerRemove:   true,
}

// switchOpTypes is T-1205's switch op vocabulary: each becomes a
// StepSwitchApply step executed by the daemon-level SwitchGateway.
var switchOpTypes = map[OpType]bool{
	OpSwitchPortUpdate: true,
}

// fwOpTypes is the full T-502 firewall op vocabulary: every one of these
// executes as a PVE firewall API call grouped into a StepFwApply step by
// its target ruleset (see BuildPlan).
var fwOpTypes = map[OpType]bool{
	OpFwRuleCreate:    true,
	OpFwRuleUpdate:    true,
	OpFwRuleDelete:    true,
	OpFwRuleMove:      true,
	OpFwOptionsUpdate: true,
	OpFwAliasCreate:   true,
	OpFwAliasUpdate:   true,
	OpFwAliasDelete:   true,
	OpFwIpsetCreate:   true,
	OpFwIpsetUpdate:   true,
	OpFwIpsetDelete:   true,
	OpFwGroupCreate:   true,
	OpFwGroupUpdate:   true,
	OpFwGroupDelete:   true,
}

// qosOpTypes is T-1505's full QoS op vocabulary: each becomes a
// StepQosApply step executed by the node-local QosGateway.
var qosOpTypes = map[OpType]bool{
	OpQosShapeCreate: true,
	OpQosShapeUpdate: true,
	OpQosShapeDelete: true,
}

// Step is one entry in a rendered apply Plan — the shape the review screen's
// Plan tab (docs/features/change-management.md §3) renders and the executor
// runs, persisted verbatim into changesets.plan_json before apply.
type Step struct {
	Kind    StepKind `json:"kind"`
	Node    string   `json:"node,omitempty"`
	Summary string   `json:"summary"`
	Target  string   `json:"target,omitempty"`
	OpIdx   []int    `json:"opIdx,omitempty"`
}

// Plan is a changeset's full ordered apply plan (changesets.plan_json).
type Plan struct {
	Steps []Step `json:"steps"`
}

// BuildPlan turns a changeset's ops into an ordered typed Plan per
// docs/architecture.md §4: node-file ops are grouped by node into a
// stage→reload pair per node (in first-appearance node order — the same
// deterministic ordering internal/change/ifaces.DiffChangeset uses so the
// plan and the diff agree), and a trailing sdn.apply step when the changeset
// carries one.
//
// The stage and reload steps for a node are emitted as an adjacent pair
// (stage nodeA, reload nodeA, stage nodeB, reload nodeB, ...) rather than all
// stages then all reloads: reload consumes the just-staged file, and pairing
// keeps each node transactional (PVE's own per-node ifupdown2 model,
// docs/architecture.md §4) and makes rollback a clean per-node file restore.
// This is a documented interpretation of the doc's category ordering for the
// multi-node case.
//
// It returns *ErrUnsupportedOp for any op the executor cannot run yet (the
// guest family — sdn.zone/vnet/subnet.* is executable as of T-402, fw.* is
// executable as of T-502, ipam.alloc.* is executable as of T-405), so an
// un-executable changeset is refused before any mutation rather than
// partially applied.
//
// T-402 note on ordering: sdn.zone/vnet/subnet.* ops become StepSDNStage
// steps, emitted *before* the per-node stage/reload pairs (docs/data-model.md
// §3's category order: "(1) cluster-scope PVE API calls, (2) per-node
// interface file staging, (3) per-node ifreload ..., (4) sdn.apply last").
// One step per op, in the changeset's own op order, mirroring the doc's
// same "documented interpretation of the category ordering" precedent the
// per-node stage/reload pairing already set below. T-405's ipam.alloc.*
// ops are the same category (1) cluster-scope call and are emitted
// alongside sdn.zone/vnet/subnet.* ops, ahead of the per-node stage/reload
// pairs, one step per op for the same reasons.
//
// fw.* ops are grouped by their target ruleset (first-appearance order,
// same convention as the per-node file grouping above) into one StepFwApply
// step per ruleset, placed after every node-file stage/reload pair and
// before a trailing sdn.apply — fw ops and node-file/SDN ops are
// independent op families with no documented relative ordering
// requirement beyond "sdn.apply last" (docs/data-model.md §3), so this is
// a defensible, documented placement rather than a spec-mandated one. A
// node- or guest-scope target additionally gets a StepFwVerify
// immediately after its StepFwApply (docs/features/firewall.md §3's
// post-apply verification); cluster-scope targets do not, since BuildPlan
// operates over ops alone with no cluster-node-list dependency to verify
// every node's compile status against — see docs/features/firewall.md's
// T-502 completion report for this flagged, deliberately narrow scope cut.
func BuildPlan(ops []Op) (Plan, error) {
	var nodeOrder []string
	byNode := map[string][]int{}
	var sdnStageSteps []Step
	var ipamSteps []Step
	sdnApply := false
	var fwTargetOrder []inventory.Ref
	byFwTarget := map[string][]int{}
	var qosSteps []Step
	var wgSteps []Step
	var switchSteps []Step

	for i, op := range ops {
		switch {
		case qosOpTypes[op.Type]:
			qosSteps = append(qosSteps, Step{
				Kind:    StepQosApply,
				Node:    op.Target.Node,
				Target:  op.Target.String(),
				OpIdx:   []int{i},
				Summary: qosStepSummary(op),
			})
		case wgOpTypes[op.Type]:
			wgSteps = append(wgSteps, Step{
				Kind:    StepWgApply,
				Node:    op.Target.Node,
				Target:  op.Target.String(),
				OpIdx:   []int{i},
				Summary: wgStepSummary(op),
			})
		case switchOpTypes[op.Type]:
			switchSteps = append(switchSteps, Step{
				Kind:    StepSwitchApply,
				Target:  op.Target.String(),
				OpIdx:   []int{i},
				Summary: switchStepSummary(op),
			})
		case nodeFileOpTypes[op.Type]:
			node := op.Target.Node
			if _, seen := byNode[node]; !seen {
				nodeOrder = append(nodeOrder, node)
			}
			byNode[node] = append(byNode[node], i)
		case op.Type == OpIpamAllocCreate:
			ipamSteps = append(ipamSteps, Step{
				Kind:    StepIpamAlloc,
				OpIdx:   []int{i},
				Summary: fmt.Sprintf("Reserve %s in subnet %s", allocCIDR(op), op.Target.ID),
			})
		case op.Type == OpIpamAllocDelete:
			ipamSteps = append(ipamSteps, Step{
				Kind:    StepIpamAlloc,
				OpIdx:   []int{i},
				Summary: fmt.Sprintf("Release %s in subnet %s", allocCIDR(op), op.Target.ID),
			})
		case sdnStageOpTypes[op.Type]:
			sdnStageSteps = append(sdnStageSteps, Step{
				Kind:    StepSDNStage,
				OpIdx:   []int{i},
				Summary: sdnStageSummary(op),
			})
		case op.Type == OpSdnApply:
			sdnApply = true
		case fwOpTypes[op.Type]:
			key := op.Target.String()
			if _, seen := byFwTarget[key]; !seen {
				fwTargetOrder = append(fwTargetOrder, op.Target)
			}
			byFwTarget[key] = append(byFwTarget[key], i)
		default:
			return Plan{}, &ErrUnsupportedOp{OpType: op.Type}
		}
	}

	// Category (1) cluster-scope PVE API calls (sdn.zone/vnet/subnet.* and
	// ipam.alloc.*) precede category (2)/(3) per-node file staging/reload,
	// per docs/data-model.md §3's ordering. sdn.zone/vnet/subnet.* first,
	// then ipam.alloc.* — both are category (1) with no documented relative
	// ordering requirement between the two families, so this is a stable,
	// deterministic (SDN-then-IPAM) placement rather than a spec-mandated
	// one.
	var steps []Step
	// Switch-port steps go first of all (T-1205): they must precede the
	// node-network stage/reload pairs so an additive switch trunk change lands
	// before the node step that relies on it — see StepSwitchApply's doc.
	steps = append(steps, switchSteps...)
	steps = append(steps, sdnStageSteps...)
	steps = append(steps, ipamSteps...)
	for _, node := range nodeOrder {
		idxs := byNode[node]
		steps = append(steps,
			Step{
				Kind:    StepStageFile,
				Node:    node,
				OpIdx:   idxs,
				Summary: fmt.Sprintf("Stage /etc/network/interfaces on %s (%d op(s))", node, len(idxs)),
			},
			Step{
				Kind:    StepReload,
				Node:    node,
				Summary: fmt.Sprintf("Reload network on %s (ifreload)", node),
			},
		)
	}
	// QoS steps come after the per-node interface stage/reload pairs (a
	// shape's bridge must already exist) and before firewall/sdn.apply —
	// see StepQosApply's doc comment.
	steps = append(steps, qosSteps...)
	// WireGuard steps come after the per-node interface stage/reload pairs
	// (the carrier interface must exist first) and before firewall/sdn.apply.
	steps = append(steps, wgSteps...)
	for _, target := range fwTargetOrder {
		idxs := byFwTarget[target.String()]
		steps = append(steps, Step{
			Kind:    StepFwApply,
			Node:    target.Node,
			Target:  target.String(),
			OpIdx:   idxs,
			Summary: fmt.Sprintf("Apply %d firewall op(s) to %s", len(idxs), describeFwTarget(target)),
		})
		if target.Node != "" {
			steps = append(steps, Step{
				Kind:    StepFwVerify,
				Node:    target.Node,
				Summary: fmt.Sprintf("Verify firewall compiled cleanly on %s", target.Node),
			})
		}
	}
	if sdnApply {
		steps = append(steps, Step{
			Kind:    StepSDNApply,
			Summary: "Apply pending cluster SDN configuration",
		})
	}

	return Plan{Steps: steps}, nil
}

// allocCIDR returns op's ipam.alloc.{create,delete} CIDR for a step summary,
// or "?" if op's params are unexpectedly not one of those two types (never
// happens for a caller that only reaches here via the two cases above).
func allocCIDR(op Op) string {
	switch p := op.Params.(type) {
	case *IpamAllocCreateParams:
		return p.CIDR
	case *IpamAllocDeleteParams:
		return p.CIDR
	default:
		return "?"
	}
}

// sdnStageSummary renders one StepSDNStage's human-readable Plan-tab summary
// (docs/features/change-management.md §3's Plan review screen).
func sdnStageSummary(op Op) string {
	verb := map[OpType]string{
		OpSdnZoneCreate: "Create", OpSdnZoneUpdate: "Update", OpSdnZoneDelete: "Delete",
		OpSdnVnetCreate: "Create", OpSdnVnetUpdate: "Update", OpSdnVnetDelete: "Delete",
		OpSdnSubnetCreate: "Create", OpSdnSubnetUpdate: "Update", OpSdnSubnetDelete: "Delete",
		OpSdnDnsZoneCreate: "Create", OpSdnDnsZoneUpdate: "Update", OpSdnDnsZoneDelete: "Delete",
		OpSdnDnsRecordCreate: "Create", OpSdnDnsRecordUpdate: "Update", OpSdnDnsRecordDelete: "Delete",
	}[op.Type]
	kind := map[OpType]string{
		OpSdnZoneCreate: "sdn zone", OpSdnZoneUpdate: "sdn zone", OpSdnZoneDelete: "sdn zone",
		OpSdnVnetCreate: "sdn vnet", OpSdnVnetUpdate: "sdn vnet", OpSdnVnetDelete: "sdn vnet",
		OpSdnSubnetCreate: "sdn subnet", OpSdnSubnetUpdate: "sdn subnet", OpSdnSubnetDelete: "sdn subnet",
		OpSdnDnsZoneCreate: "sdn dns zone", OpSdnDnsZoneUpdate: "sdn dns zone", OpSdnDnsZoneDelete: "sdn dns zone",
		OpSdnDnsRecordCreate: "sdn dns record", OpSdnDnsRecordUpdate: "sdn dns record", OpSdnDnsRecordDelete: "sdn dns record",
	}[op.Type]
	return fmt.Sprintf("%s %s %s", verb, kind, op.Target.ID)
}

// hasSDN reports whether the plan carries any SDN step (a cluster-scope
// zone/vnet/subnet mutation, or the trailing sdn.apply) — the gate for
// whether the apply/rollback engine needs to snapshot and be able to
// restore SDN config alongside node interface files (apply_snapshot.go's
// captureSnapshotFull, apply_sdn.go's restoreSDN).
func (p Plan) hasSDN() bool {
	for _, s := range p.Steps {
		if s.Kind == StepSDNStage || s.Kind == StepSDNApply {
			return true
		}
	}
	return false
}

// qosStepSummary renders one StepQosApply's Plan-tab summary (T-1505).
func qosStepSummary(op Op) string {
	switch p := op.Params.(type) {
	case *QosShapeCreateParams:
		return fmt.Sprintf("Create QoS shape %s on bridge %s (%d Mbit)", op.Target.ID, p.Bridge, p.RateMbit)
	case *QosShapeUpdateParams:
		return fmt.Sprintf("Update QoS shape %s", op.Target.ID)
	case *QosShapeDeleteParams:
		return fmt.Sprintf("Delete QoS shape %s", op.Target.ID)
	default:
		return "QoS shape operation"
	}
}

// hasQos reports whether the plan carries any QoS step — the gate for
// whether the apply/rollback engine snapshots and restores shape state
// alongside node interface files (apply_snapshot.go's captureSnapshotFull,
// apply.go's doRollbackLocked).
func (p Plan) hasQos() bool {
	for _, s := range p.Steps {
		if s.Kind == StepQosApply {
			return true
		}
	}
	return false
}

// switchStepSummary renders one StepSwitchApply's Plan-tab summary (T-1205).
func switchStepSummary(op Op) string {
	return fmt.Sprintf("Push switch port config to %s", op.Target.ID)
}

// hasSwitch reports whether the plan carries any switch step — the gate for
// whether the apply/rollback engine snapshots and restores switch-port
// pre-images alongside node interface files (apply_snapshot.go's
// captureSnapshotFull, apply.go's doRollbackLocked).
func (p Plan) hasSwitch() bool {
	for _, s := range p.Steps {
		if s.Kind == StepSwitchApply {
			return true
		}
	}
	return false
}

// wgStepSummary renders one StepWgApply's Plan-tab summary (T-1401).
func wgStepSummary(op Op) string {
	switch op.Type {
	case OpWgTunnelCreate:
		return fmt.Sprintf("Create WireGuard tunnel %s on %s", op.Target.ID, op.Target.Node)
	case OpWgTunnelUpdate:
		return fmt.Sprintf("Update WireGuard tunnel %s on %s", op.Target.ID, op.Target.Node)
	case OpWgTunnelDelete:
		return fmt.Sprintf("Delete WireGuard tunnel %s on %s", op.Target.ID, op.Target.Node)
	case OpWgPeerAdd:
		return fmt.Sprintf("Add WireGuard peer to %s", op.Target.ID)
	case OpWgPeerRemove:
		return fmt.Sprintf("Remove WireGuard peer from %s", op.Target.ID)
	default:
		return "WireGuard operation"
	}
}

// hasWg reports whether the plan carries any WireGuard step — the gate for
// whether the apply/rollback engine snapshots and restores WireGuard state
// alongside node interface files (apply_snapshot.go's captureSnapshotFull,
// apply.go's doRollbackLocked).
func (p Plan) hasWg() bool {
	for _, s := range p.Steps {
		if s.Kind == StepWgApply {
			return true
		}
	}
	return false
}

// qosNodes returns, in first-appearance order, every node this plan's
// StepQosApply steps touch — the set whose shape state the pre-apply
// snapshot must capture and restore.
func (p Plan) qosNodes() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Kind == StepQosApply && s.Node != "" && !seen[s.Node] {
			seen[s.Node] = true
			out = append(out, s.Node)
		}
	}
	return out
}

// wgNodes returns, in first-appearance order, every node this plan's
// StepWgApply steps touch — the set whose WireGuard state the pre-apply
// snapshot must capture and restore.
func (p Plan) wgNodes() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Kind == StepWgApply && s.Node != "" && !seen[s.Node] {
			seen[s.Node] = true
			out = append(out, s.Node)
		}
	}
	return out
}

// switchPortTargets returns, in first-appearance order, every switch-port Ref
// string this plan's StepSwitchApply steps touch — the set whose pre-image the
// pre-apply snapshot must capture and rollback must restore.
func (p Plan) switchPortTargets() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Kind == StepSwitchApply && s.Target != "" && !seen[s.Target] {
			seen[s.Target] = true
			out = append(out, s.Target)
		}
	}
	return out
}

// describeFwTarget renders a firewall ruleset Ref as a short human string
// for Step.Summary, matching the (cluster|node|guest) scope naming the rest
// of this codebase's firewall surfaces use.
func describeFwTarget(target inventory.Ref) string {
	switch target.ID {
	case "cluster":
		return "the datacenter firewall"
	case "node":
		return fmt.Sprintf("node %s's firewall", target.Node)
	default:
		return fmt.Sprintf("%s's firewall", target)
	}
}

// affectedNodes returns, in first-appearance order, every node the plan's
// per-node steps touch — the set the pre-apply snapshot must capture and the
// post-terminal RefreshNow must refresh.
func (p Plan) affectedNodes() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Node != "" && !seen[s.Node] {
			seen[s.Node] = true
			out = append(out, s.Node)
		}
	}
	return out
}

// hasFw reports whether the plan carries any firewall step — the gate for
// whether the apply/rollback engine snapshots and restores firewall-ruleset
// pre-images alongside node interface files (apply_snapshot.go's
// captureSnapshotFull, apply.go's doRollbackLocked), and half of
// needsRevertTicket's answer (reverticket.go: firewall writes need the user's
// PVE ticket, so an unattended revert of them needs a sealed one).
func (p Plan) hasFw() bool {
	for _, s := range p.Steps {
		if s.Kind == StepFwApply {
			return true
		}
	}
	return false
}

// fwTargets returns, in first-appearance order, every firewall ruleset Ref
// this plan's StepFwApply steps touch — undoFwTargets' same-request
// rollback iteration set.
func (p Plan) fwTargets() []inventory.Ref {
	var out []inventory.Ref
	for _, s := range p.Steps {
		if s.Kind != StepFwApply {
			continue
		}
		if ref, err := inventory.ParseRef(s.Target); err == nil {
			out = append(out, ref)
		}
	}
	return out
}
