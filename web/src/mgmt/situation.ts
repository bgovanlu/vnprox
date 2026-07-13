// Derives, per node, what the management-redundancy wizard can offer —
// purely from T-702's GET /protected-interfaces/status (the carrier + its
// resolved physical path + redundancy) plus GET /topology (the node's full
// physnic list, for candidate NICs). Framework-free so it's directly
// Vitest-able.
import type { ManagementPathRef, TopologyNode } from "../api/types";
import { parseRef } from "./mgmtWizardOps";

export interface MgmtSituation {
  node: string;
  /** The management-role path this wizard operates on (the carrier that
   * carries the node's PVE management IP). undefined when the node has no
   * mgmt-role carrier at all (nothing to make redundant). */
  carrierRef?: string;
  carrierKind?: string;
  /** The physical NIC ids already in the carrier's path. */
  pathNics: string[];
  /** The bond ref in the path, if any (flow B operates on it). */
  bondRef?: string;
  /** Physical NIC ids on the node that are NOT already in the path — the
   * redundancy candidates flow A/B pick from. */
  candidateNics: string[];
  redundant: boolean;
  /** Flow availability. */
  canBondUplink: boolean; // flow A
  canAddSlave: boolean; // flow B
  canDedicatedVlan: boolean; // flow C
}

/** Picks the management-role ref for a node (a carrier whose roles include
 * "mgmt" — the one whose loss makes the node unreachable). Corosync-only
 * carriers are deliberately ignored: this wizard is about management
 * reachability. */
function mgmtRefOf(refs: ManagementPathRef[]): ManagementPathRef | undefined {
  return refs.find((r) => r.roles.includes("mgmt")) ?? refs.find((r) => r.roles.length > 0);
}

/** All physical-NIC ids on a node, from the topology projection. */
export function physnicsOfNode(nodes: TopologyNode[], node: string): string[] {
  const out: string[] = [];
  for (const n of nodes) {
    if (n.kind !== "physnic") continue;
    const parsed = parseRef(n.id);
    if (parsed?.node === node) out.push(parsed.id);
  }
  return out.sort((a, b) => a.localeCompare(b));
}

export function analyzeMgmtSituation(
  nodePaths: ManagementPathRef[],
  topologyNodes: TopologyNode[],
  node: string,
): MgmtSituation {
  const mgmt = mgmtRefOf(nodePaths);
  const base: MgmtSituation = {
    node,
    pathNics: [],
    candidateNics: [],
    redundant: false,
    canBondUplink: false,
    canAddSlave: false,
    canDedicatedVlan: false,
  };
  if (!mgmt) return base;

  const carrier = parseRef(mgmt.ref);
  const pathNics: string[] = [];
  let bondRef: string | undefined;
  for (const ref of mgmt.path) {
    const p = parseRef(ref);
    if (!p) continue;
    if (p.kind === "physnic") pathNics.push(p.id);
    else if (p.kind === "bond" || p.kind === "ovs-bond") bondRef = ref;
  }

  const allNics = physnicsOfNode(topologyNodes, node);
  const inPath = new Set(pathNics);
  const candidateNics = allNics.filter((n) => !inPath.has(n));

  const carrierIsBridge = carrier?.kind === "bridge" || carrier?.kind === "ovs-bridge";

  return {
    node,
    carrierRef: mgmt.ref,
    carrierKind: carrier?.kind,
    pathNics,
    bondRef,
    candidateNics,
    redundant: mgmt.redundant,
    // Flow A: the mgmt bridge's uplink is a single bare NIC (no bond yet) —
    // bond it with a candidate. Needs a candidate to bond to.
    canBondUplink: carrierIsBridge && !bondRef && pathNics.length >= 1 && candidateNics.length >= 1,
    // Flow B: there's already a bond in the path — add/replace a slave.
    // Needs a candidate to add.
    canAddSlave: bondRef !== undefined && candidateNics.length >= 1,
    // Flow C: move management onto a dedicated VLAN sub-interface. Always
    // available while there's a resolvable carrier (its address is carried
    // over verbatim, so no candidate NIC is needed).
    canDedicatedVlan: carrier !== undefined,
  };
}
