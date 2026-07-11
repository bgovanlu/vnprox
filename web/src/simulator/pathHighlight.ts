// Turns a SimulateResult's hop list into the node/edge id sets
// TopologyPage's toFlowElements.ts already knows how to paint with a
// status color (see its `pathHighlight` param) — the same "highlight a
// specific path with a status color" mechanism the hover chain-highlight
// (topology/projection.ts's computeHoverHighlight) and the VLAN filter
// (computeVlanMatch) already established, reused rather than duplicated
// (docs/features/firewall.md §5: "the hop-by-hop path rendered on the
// topology map"; T-504 AC2's "missing link renders at the correct edge").
// Deliberately framework-free (no React, no @xyflow/react) so it's
// exhaustively Vitest-able on its own, same rationale as toFlowElements.ts.
import type { SimHop, SimMissing, SimVerdict, TopologyEdge } from "../api/types";
import { edgeId } from "../topology/toFlowElements";

export interface PathHighlight {
  /** Every hop's ref that names a real inventory entity (synthetic hops —
   * "external", a fabric segment — never match a topology node, so they're
   * simply absent here rather than needing a sentinel). */
  nodeIds: Set<string>;
  /** Topology edges connecting two *consecutive* hops, keyed by
   * toFlowElements.ts's `edgeId` convention so the lookup in that module is
   * a plain Set.has. */
  edgeIds: Set<string>;
  /** The enforcement-point endpoint a deny verdict stopped at (the
   * blocking rule's own guest/nic ref) — marked distinctly from the rest
   * of the path so "where it was blocked" reads at a glance. */
  blockingNodeId?: string;
  /** The break point of an unreachable verdict (Missing.atRef), rendered
   * with a distinct "missing link" marker (T-504 AC2). */
  missingNodeIds: Set<string>;
  verdict: SimVerdict;
}

/**
 * Computes the map overlay for one simulation result.
 *
 * `edges` is the full topology edge list (whatever layers/filters are
 * currently active on the embedding canvas) — consecutive hops are matched
 * against it in either direction, since a Hop pair's L2/L3 relationship
 * doesn't dictate which TopologyEdge.from/to order the backend chose for
 * the underlying inventory edge.
 */
export function computePathHighlight(
  hops: readonly SimHop[],
  edges: readonly TopologyEdge[],
  verdict: SimVerdict,
  missing?: SimMissing,
  blockingNodeId?: string,
): PathHighlight {
  const nodeIds = new Set<string>();
  for (const hop of hops) {
    if (hop.ref) {
      nodeIds.add(hop.ref);
    }
  }

  const edgeIds = new Set<string>();
  for (let i = 0; i < hops.length - 1; i++) {
    const a = hops[i]?.ref;
    const b = hops[i + 1]?.ref;
    if (!a || !b) continue;
    for (const e of edges) {
      if ((e.from === a && e.to === b) || (e.from === b && e.to === a)) {
        edgeIds.add(edgeId(e));
      }
    }
  }

  const missingNodeIds = new Set<string>();
  if (missing?.atRef) {
    missingNodeIds.add(missing.atRef);
    // Ensure the break point renders even when it wasn't itself a traced
    // hop (e.g. the unreachable bridge on the far side of a VLAN mismatch).
    nodeIds.add(missing.atRef);
  }

  return { nodeIds, edgeIds, blockingNodeId, missingNodeIds, verdict };
}
