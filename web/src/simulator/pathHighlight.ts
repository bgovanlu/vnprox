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
import type { SimHop, SimMissing, SimVerdict, TopologyEdge, VerifyOutcome } from "../api/types";
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
  /** T-806 "Verify live": the probed source's own ref, marked with an
   * observed-outcome indicator distinct from the simulated-verdict marker
   * above (docs/features/firewall.md §5) — undefined until a live probe
   * result has actually been returned for this simulation. */
  verifyNodeId?: string;
  verifyOutcome?: VerifyOutcome;
  /** True iff the live probe's outcome disagrees with the simulated
   * verdict — renders a further distinct "diverges" marker (never a silent
   * correction of the simulated verdict, per the honesty contract). */
  verifyDiverges?: boolean;
}

/** Merges a T-806 verify result onto an already-computed PathHighlight
 * (computePathHighlight's own contract stays exactly as it was for T-504's
 * callers — the map overlay for a plain, unverified simulate result — so
 * this is a separate, additive step rather than a new parameter growing
 * that function's signature). `verifySrcNodeId` is the resolved src
 * endpoint's own ref (result.src.ref from the *simulated* half of the
 * verify response, which is byte-identical to a plain /simulate/path
 * result's own src). */
export function withVerifyHighlight(
  base: PathHighlight,
  verifySrcNodeId: string | undefined,
  verifyOutcome: VerifyOutcome,
  verifyDiverges: boolean,
): PathHighlight {
  if (!verifySrcNodeId) {
    return base;
  }
  return { ...base, verifyNodeId: verifySrcNodeId, verifyOutcome, verifyDiverges };
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
