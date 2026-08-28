// SPDX-License-Identifier: Apache-2.0

// Resolves a PVE node name to a stable "whole node" anchor id for the
// node-to-node overlay layers (Latency/T-1303, MTU/T-1306, WireGuard/
// T-1402) that want to draw one edge per cluster node rather than per
// specific interface.
//
// Found while wiring T-1402's WireGuard overlay onto the same
// "node:<name>:<name>" convention TopologyPage.tsx's own `nodeIdForName`
// already used for Latency/MTU: internal/topology.Project deliberately
// NEVER renders a KindNode entity of its own (project.go's `layerOf` doc
// comment — "Kinds with no entry (KindNode, KindFwRuleset) are never
// rendered directly as topology nodes ... cluster membership ... surfaced
// by other routes ... not the topology map"). A real `GET /topology`
// response therefore never contains a "node:<name>:<name>" id — the
// previous `nodeIdForName` implementation resolved against exactly that
// nonexistent id, so `computeLatencyOverlayEdges`/`computeMTUOverlayEdges`
// silently produced zero edges against any real backend (their own unit
// tests never caught this: both inject their own `nodeIdForName` fixture
// directly, and neither overlay had a component/e2e test exercising a real
// GET /topology response). This resolver fixes the anchor for all three
// layers at once: it picks the first rendered entity in that node's own
// band (`TopologyNode.nodeGroup`), sorted by id for determinism — any node
// with at least one physical/L2 entity (true for every real cluster node)
// has a stable, always-resolvable anchor.
import type { TopologyNode } from "../api/types";

/** Builds a `nodeName -> map node id` resolver from a projected topology's
 * node list. `nodeGroup === ""` is the cluster-spanning SDN band sentinel
 * (docs/features/topology.md §3) and never names a real cluster node, so
 * it's excluded. Deterministic: ties are broken by the lexicographically
 * smallest entity id in that node's band. */
export function buildNodeAnchorResolver(nodes: readonly TopologyNode[]): (nodeName: string) => string | undefined {
  const byGroup = new Map<string, string>();
  const sorted = [...nodes].sort((a, b) => a.id.localeCompare(b.id));
  for (const n of sorted) {
    if (!n.nodeGroup) continue;
    if (!byGroup.has(n.nodeGroup)) byGroup.set(n.nodeGroup, n.id);
  }
  return (nodeName: string): string | undefined => byGroup.get(nodeName);
}
