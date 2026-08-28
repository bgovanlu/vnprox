// SPDX-License-Identifier: Apache-2.0

// Client-side preview of the apply plan for the review screen's Plan tab
// (docs/features/change-management.md §3: "Plan (the exact ordered steps:
// which PVE API calls, which nodes reload, in what order). Nothing applies
// until the user has seen this screen"). The authoritative plan is built
// server-side at apply time (internal/change/apply_plan.go's BuildPlan,
// persisted into plan_json) — this module mirrors that function's exact
// grouping/ordering rules for the pre-apply case, where the user must see
// the steps *before* clicking Apply. Once the server has applied, the
// review/drawer UI prefers the persisted plan; this preview is only shown
// while `changeset.plan` is absent. Framework-free, directly Vitest-able.
//
// It MUST track BuildPlan's op-family classification: the executor gained the
// SDN-write (sdn.zone/vnet/subnet.*, T-402), IPAM (ipam.alloc.*, T-405),
// firewall (fw.*, T-502), and WireGuard (wg.*, T-1401) families after this
// preview was first written, so they are executable — only the guest.*
// family is still refused at apply.
import type { Op, Plan, PlanStep } from "../api/types";
import { refNode, summarizeOp } from "./opSummary";

/** Ops that mutate a node's /etc/network/interfaces — mirrors
 * internal/change/apply_plan.go's nodeFileOpTypes exactly. */
const NODE_FILE_OP_TYPES = new Set<Op["op"]>([
  "iface.update",
  "iface.rename",
  "iface.raw.replace",
  "bond.create",
  "bond.update",
  "bond.delete",
  "bridge.create",
  "bridge.update",
  "bridge.delete",
  "bridge.port.add",
  "bridge.port.remove",
  "vlan.create",
  "vlan.update",
  "vlan.delete",
]);

/** Cluster-scope SDN-write ops (executable since T-402) — mirrors
 * apply_plan.go's sdnStageOpTypes. */
const SDN_STAGE_OP_TYPES = new Set<Op["op"]>([
  "sdn.zone.create",
  "sdn.zone.update",
  "sdn.zone.delete",
  "sdn.vnet.create",
  "sdn.vnet.update",
  "sdn.vnet.delete",
  "sdn.subnet.create",
  "sdn.subnet.update",
  "sdn.subnet.delete",
]);

/** WireGuard ops (executable since T-1401) — mirrors apply_plan.go's
 * wgOpTypes. Each becomes its own wg_apply step (one op per step, unlike
 * the grouped node-file/firewall steps), matching BuildPlan's own
 * one-Step-per-op wgSteps construction. */
const WG_OP_TYPES = new Set<Op["op"]>(["wg.tunnel.create", "wg.tunnel.update", "wg.tunnel.delete", "wg.peer.add", "wg.peer.remove"]);

/** Firewall ops (executable since T-502) — mirrors apply_plan.go's fwOpTypes. */
const FW_OP_TYPES = new Set<Op["op"]>([
  "fw.rule.create",
  "fw.rule.update",
  "fw.rule.delete",
  "fw.rule.move",
  "fw.options.update",
  "fw.alias.create",
  "fw.alias.update",
  "fw.alias.delete",
  "fw.ipset.create",
  "fw.ipset.update",
  "fw.ipset.delete",
  "fw.group.create",
  "fw.group.update",
  "fw.group.delete",
]);

export interface PlanPreview {
  plan: Plan;
  /** Op types in the changeset the apply engine cannot execute yet. Only the
   * guest.* family remains (its pve.Client write methods are a follow-up —
   * see apply_seams.go's PVEGateway doc comment); it is refused at apply with
   * 422 unsupported_op. Surfaced so the review screen can warn *before* the
   * user clicks Apply, matching BuildPlan's own up-front rejection. Empty for
   * an all-SDN/IPAM/firewall/node-file changeset (the common case). */
  unsupportedOps: string[];
}

/** Mirrors BuildPlan's classification and ordering: cluster-scope SDN-stage
 * steps first, then IPAM alloc steps, then an adjacent stage->reload pair per
 * node (first-appearance order), then firewall apply(+verify) steps, then
 * sdn.apply last. */
export function buildPlanPreview(ops: Op[]): PlanPreview {
  const nodeOrder: string[] = [];
  const byNode = new Map<string, number[]>();
  const sdnStageSteps: PlanStep[] = [];
  const ipamSteps: PlanStep[] = [];
  const wgSteps: PlanStep[] = [];
  const fwTargetOrder: string[] = [];
  const byFwTarget = new Map<string, number[]>();
  let sdnApply = false;
  const unsupported = new Set<string>();

  for (const [i, op] of ops.entries()) {
    if (NODE_FILE_OP_TYPES.has(op.op)) {
      const node = op.target ? refNode(op.target) : "";
      const existing = byNode.get(node);
      if (existing) {
        existing.push(i);
      } else {
        nodeOrder.push(node);
        byNode.set(node, [i]);
      }
    } else if (SDN_STAGE_OP_TYPES.has(op.op)) {
      sdnStageSteps.push({ kind: "sdn_stage", opIdx: [i], summary: summarizeOp(op) });
    } else if (op.op === "ipam.alloc.create" || op.op === "ipam.alloc.delete") {
      ipamSteps.push({ kind: "ipam_alloc", opIdx: [i], summary: summarizeOp(op) });
    } else if (op.op === "sdn.apply") {
      sdnApply = true;
    } else if (WG_OP_TYPES.has(op.op)) {
      wgSteps.push({ kind: "wg_apply", node: op.target ? refNode(op.target) : undefined, opIdx: [i], summary: summarizeOp(op) });
    } else if (FW_OP_TYPES.has(op.op)) {
      const key = op.target ?? "";
      const existing = byFwTarget.get(key);
      if (existing) {
        existing.push(i);
      } else {
        fwTargetOrder.push(key);
        byFwTarget.set(key, [i]);
      }
    } else {
      unsupported.add(op.op);
    }
  }

  const steps: PlanStep[] = [...sdnStageSteps, ...ipamSteps];
  for (const node of nodeOrder) {
    const idxs = byNode.get(node) ?? [];
    steps.push(
      {
        kind: "stage_file",
        node,
        opIdx: idxs,
        summary: `Stage /etc/network/interfaces on ${node} (${String(idxs.length)} op(s))`,
      },
      { kind: "reload", node, summary: `Reload network on ${node} (ifreload)` },
    );
  }
  // WireGuard steps come after the per-node interface stage/reload pairs
  // (the carrier interface must exist first) and before firewall/sdn.apply
  // — mirrors BuildPlan's exact placement.
  steps.push(...wgSteps);
  for (const target of fwTargetOrder) {
    const idxs = byFwTarget.get(target) ?? [];
    const node = target ? refNode(target) : "";
    steps.push({
      kind: "fw_apply",
      node: node || undefined,
      opIdx: idxs,
      summary: `Apply ${String(idxs.length)} firewall op(s) to ${target}`,
    });
    if (node) {
      steps.push({ kind: "fw_verify", node, summary: `Verify firewall compiled cleanly on ${node}` });
    }
  }
  if (sdnApply) {
    steps.push({ kind: "sdn_apply", summary: "Apply pending cluster SDN configuration" });
  }

  return { plan: { steps }, unsupportedOps: [...unsupported] };
}
