// SPDX-License-Identifier: Apache-2.0

// Pure summarizer for the walkthrough's step 1 ("What we found" —
// docs/user-guide.md §1.1: "your cluster's network, drawn. Nothing was
// changed; vnprox only read."). Framework-free and Vitest-able, same split
// as onboardingMachine.ts: this file computes the counts, the component
// only renders them.
import { ALL_LAYERS } from "../api/types";
import type { Layer, TopologyResponse } from "../api/types";

export interface FoundSummary {
  /** Distinct cluster node names the topology response's nodes reference
   * (nodeGroup "" is the cluster-spanning SDN band, excluded here — see
   * TopologyNode's doc comment in api/types.ts). */
  clusterNodes: string[];
  /** Entity count per layer (docs/features/topology.md §1's four bands). */
  byLayer: Record<Layer, number>;
  totalEntities: number;
  totalEdges: number;
}

const EMPTY_BY_LAYER: Record<Layer, number> = { phys: 0, l2: 0, sdn: 0, guest: 0 };

/** Summarizes a GET /topology response into the read-only counts step 1
 * shows. Never mutates or re-fetches anything — the walkthrough's first
 * step is explicitly non-destructive ("Nothing was changed; vnprox only
 * read."). */
export function summarizeFound(topology: TopologyResponse | undefined): FoundSummary {
  if (!topology) {
    return { clusterNodes: [], byLayer: EMPTY_BY_LAYER, totalEntities: 0, totalEdges: 0 };
  }
  const nodeSet = new Set<string>();
  const byLayer: Record<Layer, number> = { ...EMPTY_BY_LAYER };
  for (const n of topology.nodes) {
    if (n.nodeGroup) nodeSet.add(n.nodeGroup);
    byLayer[n.layer] += 1;
  }
  return {
    clusterNodes: [...nodeSet].sort(),
    byLayer,
    totalEntities: topology.nodes.length,
    totalEdges: topology.edges.length,
  };
}

/** Re-exported only so callers importing from this module for a typed
 * layer iteration (e.g. the summary grid) don't need a second import from
 * api/types.ts just for this one constant. */
export { ALL_LAYERS };
