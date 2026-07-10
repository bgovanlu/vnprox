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
import type { Op, Plan, PlanStep } from "../api/types";
import { refNode } from "./opSummary";

/** The op families that mutate a node's /etc/network/interfaces — mirrors
 * internal/change/apply_plan.go's nodeFileOpTypes exactly. */
const NODE_FILE_OP_TYPES = new Set<Op["op"]>([
  "iface.update",
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

export interface PlanPreview {
  plan: Plan;
  /** Op types in the changeset the apply engine cannot execute yet
   * (T-205's documented executable-op scope: node-file ops + sdn.apply;
   * guest/SDN-write/fw/ipam families are refused at apply with 422
   * unsupported_op). Surfaced so the review screen can warn *before* the
   * user clicks Apply, matching BuildPlan's own up-front rejection. */
  unsupportedOps: string[];
}

/** Mirrors BuildPlan: node-file ops grouped by node in first-appearance
 * order, an adjacent stage->reload pair per node, sdn.apply last. */
export function buildPlanPreview(ops: Op[]): PlanPreview {
  const nodeOrder: string[] = [];
  const byNode = new Map<string, number[]>();
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
    } else if (op.op === "sdn.apply") {
      sdnApply = true;
    } else {
      unsupported.add(op.op);
    }
  }

  const steps: PlanStep[] = [];
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
  if (sdnApply) {
    steps.push({ kind: "sdn_apply", summary: "Apply pending cluster SDN configuration" });
  }

  return { plan: { steps }, unsupportedOps: [...unsupported] };
}
