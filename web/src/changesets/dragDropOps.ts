// SPDX-License-Identifier: Apache-2.0

// Pure translation of a map drag-drop gesture into a drafted Op (docs/
// features/change-management.md §5 walkthrough: "create bond from two NICs
// via drag"; topology feature spec §2's drag-drop edits: "NIC->bond/bridge,
// guest-NIC edge retarget"). Framework-free (no React/@xyflow import) so
// it's directly Vitest-able against plain TopologyNode/TopologyEdge
// fixtures — the actual drag/intersection-detection glue lives in
// TopologyPage, this module only answers "given these two nodes the user
// just dropped one onto, what op (if any) does that mean?".
import type { Op, TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { refId } from "./opSummary";

/** Names already enslaved to `bondRef`, reconstructed from the topology's
 * own `enslaved-by` edges (no extra fetch needed — appending a slave to an
 * existing bond must not silently drop the others, since bond.update's
 * `slaves` field replaces the whole list, params_bond.go's documented
 * semantics). */
function currentBondSlaveNames(edges: TopologyEdge[], bondRef: string): string[] {
  return edges.filter((e) => e.kind === "enslaved-by" && e.to === bondRef).map((e) => refId(e.from));
}

/** The next unused "bondN" name on `node` (bond0, bond1, ... — the
 * conventional Proxmox naming), for the physnic-onto-physnic "create a new
 * bond from these two NICs" gesture, which has no existing target to name
 * itself after. */
function nextBondName(nodes: TopologyNode[], node: string): string {
  const used = new Set(
    nodes.filter((n) => n.nodeGroup === node && (n.kind === "bond" || n.kind === "ovs-bond")).map((n) => refId(n.id)),
  );
  let i = 0;
  while (used.has(`bond${String(i)}`)) i++;
  return `bond${String(i)}`;
}

/**
 * Given the topology-node the user just dragged (`dragged`) and the node
 * they dropped it onto (`target`), returns the Op that gesture should
 * draft, or `undefined` when the pair isn't a recognized drag-drop edit —
 * callers should treat `undefined` as "just a cosmetic reposition, no op".
 */
export function computeDragOp(dragged: TopologyNode, target: TopologyNode, topology: TopologyResponse): Op | undefined {
  if (dragged.id === target.id) return undefined;

  // physnic -> bond: append as a slave.
  if (dragged.kind === "physnic" && (target.kind === "bond" || target.kind === "ovs-bond")) {
    if (dragged.nodeGroup !== target.nodeGroup) return undefined;
    const current = currentBondSlaveNames(topology.edges, target.id);
    const draggedName = refId(dragged.id);
    if (current.includes(draggedName)) return undefined;
    return { op: "bond.update", target: target.id, params: { slaves: [...current, draggedName] } };
  }

  // physnic -> physnic: create a brand-new bond from the two.
  if (dragged.kind === "physnic" && target.kind === "physnic") {
    if (dragged.nodeGroup !== target.nodeGroup) return undefined;
    const bondName = nextBondName(topology.nodes, target.nodeGroup);
    return {
      op: "bond.create",
      target: `bond:${target.nodeGroup}:${bondName}`,
      params: { mode: "802.3ad", slaves: [refId(target.id), refId(dragged.id)] },
    };
  }

  // physnic/bond/vlan -> bridge: add as a port (bridge.port.add already
  // exists as its own op — no need to reconstruct the full port list).
  if (
    (dragged.kind === "physnic" || dragged.kind === "bond" || dragged.kind === "ovs-bond" || dragged.kind === "vlan") &&
    (target.kind === "bridge" || target.kind === "ovs-bridge")
  ) {
    if (dragged.nodeGroup !== target.nodeGroup) return undefined;
    return { op: "bridge.port.add", target: target.id, params: { port: refId(dragged.id) } };
  }

  // guest-nic -> bridge or SDN VNet: reattach (edge retarget). VNets are
  // cluster-scoped (nodeGroup ""), so the same-node check only applies
  // against a plain bridge target.
  if (dragged.kind === "guest-nic" && (target.kind === "bridge" || target.kind === "ovs-bridge" || target.kind === "sdn-vnet")) {
    if (target.kind !== "sdn-vnet" && dragged.nodeGroup !== target.nodeGroup) return undefined;
    return { op: "guest.nic.update", target: dragged.id, params: { bridgeOrVnet: refId(target.id) } };
  }

  return undefined;
}
