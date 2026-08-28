// SPDX-License-Identifier: Apache-2.0

// Pure projection logic: turns a raw TopologyResponse (docs/features/
// topology.md §3's rendering contract) plus UI state (active layers, VLAN
// filter, hover/selection, saved positions) into the props React Flow needs.
// Deliberately framework-free (no React, no @xyflow/react imports) so it's
// exhaustively Vitest-able without rendering anything — see projection.test.ts.
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";

export const GUEST_GROUP_PREFIX = "guest-group:";

/** Guest-collapse synthetic node ids look like "guest-group:<node>:<targetRef>"
 * (internal/topology/collapse.go) and are NOT valid inventory.Ref strings —
 * clicking one must expand/toggle rather than open the inspector. */
export function isGuestGroupId(id: string): boolean {
  return id.startsWith(GUEST_GROUP_PREFIX);
}

export interface ParsedGuestGroup {
  node: string;
  targetRef: string;
}

/** Parses a guest-group id. targetRef is itself a "kind:node:id" Ref string
 * that may contain further ':' characters (inventory.Ref.String's doc
 * comment), so this only splits on the *first* remaining ':' after the
 * prefix and node — mirroring Go's ParseRef/collapseGuests SplitN(s, ":", 3)
 * approach — rather than a naive split on every ':'. */
export function parseGuestGroupId(id: string): ParsedGuestGroup | undefined {
  if (!isGuestGroupId(id)) return undefined;
  const rest = id.slice(GUEST_GROUP_PREFIX.length);
  const firstColon = rest.indexOf(":");
  if (firstColon === -1) return undefined;
  const node = rest.slice(0, firstColon);
  const targetRef = rest.slice(firstColon + 1);
  if (!node || !targetRef) return undefined;
  return { node, targetRef };
}

export const PHYS_GROUP_PREFIX = "phys-group:";

/** Physical-layer-collapse synthetic node ids look like "phys-group:<node>"
 * (internal/topology/collapse_physical.go, T-1907) and are NOT valid
 * inventory.Ref strings — clicking one must expand/toggle rather than open
 * the inspector, exactly like a guest-group id. Unlike a guest-group id,
 * there is no further ":<targetRef>" suffix to parse: physical-layer
 * collapse groups strictly per node (docs/features/topology.md §4's "a
 * per-node summary"), not per attachment point, so the node name is the
 * entire remainder of the id. */
export function isPhysGroupId(id: string): boolean {
  return id.startsWith(PHYS_GROUP_PREFIX);
}

export interface ParsedPhysGroup {
  node: string;
}

/** Parses a phys-group id, returning its node name (undefined if malformed
 * or empty). */
export function parsePhysGroupId(id: string): ParsedPhysGroup | undefined {
  if (!isPhysGroupId(id)) return undefined;
  const node = id.slice(PHYS_GROUP_PREFIX.length);
  if (!node) return undefined;
  return { node };
}

// --- Layer visibility ------------------------------------------------------

/** Splits nodeGroup === "" (the cluster-spanning SDN band) out of a node's
 * effective layer visibility question: an SDN node is only ever gated by
 * the "sdn" layer toggle, per-node columns by their own layer. This
 * function only needs the node's own `layer` field either way — nodeGroup
 * doesn't change which toggle governs it — but is documented here since the
 * caller (toFlowNodes) also uses nodeGroup for column placement. */
export function isLayerVisible(node: TopologyNode, activeLayers: ReadonlySet<Layer>): boolean {
  return activeLayers.has(node.layer);
}

/** Filters nodes/edges to the active layer set, dropping edges whose either
 * endpoint is no longer present (dangling edges are never rendered). */
export function filterByLayers(
  nodes: TopologyNode[],
  edges: TopologyEdge[],
  activeLayers: ReadonlySet<Layer>,
): { nodes: TopologyNode[]; edges: TopologyEdge[] } {
  const visibleNodes = nodes.filter((n) => isLayerVisible(n, activeLayers));
  const visibleIds = new Set(visibleNodes.map((n) => n.id));
  const visibleEdges = edges.filter((e) => visibleIds.has(e.from) && visibleIds.has(e.to));
  return { nodes: visibleNodes, edges: visibleEdges };
}

// --- Hover chain-highlight --------------------------------------------------

// Edge kinds mirror internal/inventory/link.go's EdgeKind constants exactly
// (this module reads the same string values the backend emits, never its
// own vocabulary).
const STRUCTURAL_CHILD_EDGE_KINDS = new Set(["enslaved-by", "port-of", "tagged-on"]);
const HUB_KINDS = new Set(["bridge", "ovs-bridge", "sdn-vnet", "sdn-zone", "bond", "ovs-bond"]);

interface EdgeClassification {
  /** child ref -> its parent refs one level up the stack (e.g. a slave NIC's
   * parent is its bond; a bridge's parent is the VNet it realizes). */
  parentsOf: Map<string, Set<string>>;
  /** hub ref -> its structural children (bond slaves, bridge ports, VLAN
   * sub-interfaces) — never guest attachments, so a sibling guest never
   * gets pulled in just because it shares a bridge with the hovered one. */
  structuralChildrenOf: Map<string, Set<string>>;
  /** attachment target ref -> guest NICs attached to it. Only consulted when
   * the *hovered* entity is itself a hub (see computeHoverHighlight). */
  attachedChildrenOf: Map<string, Set<string>>;
}

function addToSetMap(map: Map<string, Set<string>>, key: string, value: string): void {
  let set = map.get(key);
  if (!set) {
    set = new Set();
    map.set(key, set);
  }
  set.add(value);
}

function classifyEdges(edges: TopologyEdge[]): EdgeClassification {
  const parentsOf = new Map<string, Set<string>>();
  const structuralChildrenOf = new Map<string, Set<string>>();
  const attachedChildrenOf = new Map<string, Set<string>>();

  for (const e of edges) {
    if (e.kind === "realizes") {
      // From = VNet (the more abstract/aggregating side), To = bridge (the
      // concrete realization on one node) — so the bridge's "parent" in the
      // upward-the-stack sense is the VNet, the reverse of every other kind
      // below (see internal/inventory/link.go's EdgeRealizes doc comment).
      addToSetMap(parentsOf, e.to, e.from);
      continue;
    }
    addToSetMap(parentsOf, e.from, e.to);
    if (STRUCTURAL_CHILD_EDGE_KINDS.has(e.kind)) {
      addToSetMap(structuralChildrenOf, e.to, e.from);
    }
    if (e.kind === "attached-to") {
      addToSetMap(attachedChildrenOf, e.to, e.from);
    }
  }
  return { parentsOf, structuralChildrenOf, attachedChildrenOf };
}

/**
 * Computes the full connectivity chain to highlight when hovering `hoveredId`
 * (docs/features/topology.md §2: "highlight the full connectivity chain
 * (guest NIC → bridge → bond → NIC → switch port) and dim the rest").
 *
 * Deliberately NOT a full transitive closure over the whole adjacency graph:
 * a busy bridge can have dozens of guest NICs and several ports, and an SDN
 * VNet can span every node in the cluster, so naively flood-filling from one
 * guest NIC would end up highlighting most of the map — the opposite of
 * "dim the rest". Instead:
 *
 *   1. Walk *upward* from the hovered entity along its structural parent
 *      (guest NIC → bridge/VNet, slave → bond, bridge port → bridge, VLAN
 *      sub-if → parent link, bridge → realized VNet, physNIC → LLDP
 *      neighbor), transitively.
 *   2. For every entity reached in step 1 (plus the hovered one), also pull
 *      in its *structural* children (bond slaves, bridge ports, VLAN
 *      sub-interfaces) — this is what completes "→ bond → NIC" once the walk
 *      reaches a bridge/bond, without ever following a bridge's *guest*
 *      attachments (that would re-introduce the sibling-fan-out problem).
 *   3. Only if the hovered entity is itself a hub (bridge/bond/VNet/zone) —
 *      i.e. the user hovered the switch/bridge directly, not a guest — also
 *      reveal its direct guest attachments (one level), since "what's
 *      plugged into this" is the natural reading of hovering a hub itself.
 */
export function computeHoverHighlight(
  nodesById: ReadonlyMap<string, TopologyNode>,
  edges: TopologyEdge[],
  hoveredId: string,
): Set<string> {
  const { parentsOf, structuralChildrenOf, attachedChildrenOf } = classifyEdges(edges);
  const highlighted = new Set<string>([hoveredId]);

  // A single combined worklist following both directions (parent-up and
  // structural-child-down) from every node discovered so far, to a
  // fixpoint. Doing both from *every* node (not just the original hoveredId)
  // is what lets a chain discovered only via the downward step (e.g. a
  // bond's slave NIC, reached through the bridge) still continue upward
  // from there (e.g. that NIC's own LLDP neighbor) — while remaining safe
  // against sibling fan-out, since structuralChildrenOf never includes
  // guest attachments.
  const queue = [hoveredId];
  while (queue.length > 0) {
    const current = queue.pop();
    if (current === undefined) continue;
    for (const parent of parentsOf.get(current) ?? []) {
      if (!highlighted.has(parent)) {
        highlighted.add(parent);
        queue.push(parent);
      }
    }
    for (const child of structuralChildrenOf.get(current) ?? []) {
      if (!highlighted.has(child)) {
        highlighted.add(child);
        queue.push(child);
      }
    }
  }

  const hoveredKind = nodesById.get(hoveredId)?.kind ?? (isGuestGroupId(hoveredId) ? "guest-group" : undefined);
  if (hoveredKind && HUB_KINDS.has(hoveredKind)) {
    for (const child of attachedChildrenOf.get(hoveredId) ?? []) {
      highlighted.add(child);
    }
  }

  return highlighted;
}

// --- VLAN filter -------------------------------------------------------

/** Parses a "vid=20", "tag=20" (SDN VNet/realizes badges use "tag=" instead
 * of "vid=" — see internal/topology/project.go's badgesOf/EdgeRealizes),
 * or "vlans=10-20,30" (VLAN-aware bridge trunk ranges) badge token,
 * returning true if it carries `vlan`. Exported so switchModel.ts's
 * switchCarriesVlan (the switch-faceplate view's equivalent of
 * computeVlanMatch below) reuses this exact parse instead of keeping its own
 * copy — the two views must dim identically on the same badge shapes. */
export function badgeCarriesVlan(badge: string, vlan: number): boolean {
  const [key, value] = badge.split("=", 2);
  if (value === undefined) return false;
  if (key === "vid" || key === "tag") {
    return Number(value) === vlan;
  }
  if (key === "vlans") {
    return value.split(",").some((range) => {
      const [loStr, hiStr] = range.split("-", 2);
      const lo = Number(loStr);
      const hi = hiStr === undefined ? lo : Number(hiStr);
      return Number.isFinite(lo) && Number.isFinite(hi) && vlan >= lo && vlan <= hi;
    });
  }
  return false;
}

function entityCarriesVlan(badges: string[], vlan: number): boolean {
  return badges.some((b) => badgeCarriesVlan(b, vlan));
}

export interface VlanMatch {
  nodes: Set<string>;
  edges: Set<TopologyEdge>;
}

/**
 * Computes the set of nodes/edges that "carry" `vlan`, for client-side
 * dimming (docs/features/topology.md §2: "the map dims everything not
 * carrying that VLAN"). Unlike the backend's `?vlan=` filter (which removes
 * non-matching entities — internal/topology/project.go's restrictToVLAN),
 * this never removes anything; callers render every node/edge and just
 * lower the opacity of what's absent from the returned sets.
 *
 * Mirrors restrictToVLAN's two-step shape: direct carriers (by badge) first,
 * then edges whose badge itself names the VID, or that connect a direct
 * carrier to its other endpoint (so a VLAN-tagged guest NIC's plain,
 * non-VLAN-aware bridge still renders undimmed — the edge is the thing
 * carrying the tag, not the bridge itself).
 *
 * Known limitation (documented, not silently swallowed): LLDP neighbors'
 * discovered VLAN membership (inventory.LldpNeighbor.VLAN) is never
 * exposed as a topology badge server-side (see project.go's badgesOf — it
 * only emits a "port=" badge for LLDP neighbors), so this client-side VLAN
 * filter cannot match on it. A physical-layer LLDP neighbor node is only
 * ever pulled in via the edge-adjacency step below, not as a direct carrier.
 */
export function computeVlanMatch(nodes: TopologyNode[], edges: TopologyEdge[], vlan: number): VlanMatch {
  const matchedNodes = new Set<string>();
  for (const n of nodes) {
    if (entityCarriesVlan(n.badges, vlan)) {
      matchedNodes.add(n.id);
    }
  }

  const matchedEdges = new Set<TopologyEdge>();
  for (const e of edges) {
    const edgeCarries = entityCarriesVlan(e.badges, vlan);
    if (edgeCarries || matchedNodes.has(e.from) || matchedNodes.has(e.to)) {
      matchedEdges.add(e);
      matchedNodes.add(e.from);
      matchedNodes.add(e.to);
    }
  }

  return { nodes: matchedNodes, edges: matchedEdges };
}
