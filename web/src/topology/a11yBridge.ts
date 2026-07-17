// The topology map's accessibility bridge (T-901 deliverable): the pure,
// framework-free half of the "parallel DOM roving-focus layer kept in sync
// with the canvas" that canvas v2 must carry so it is NOT a pixel blob with
// zero DOM a11y surface. TopologyA11yLayer.tsx renders these descriptors as
// real focusable DOM; this module computes them.
//
// This is a deliberately small, documented, reusable seam — it is the thing
// T-905 (WCAG pass) and T-903 (palette-/keyboard-driven map navigation)
// build on rather than reinventing:
//
//   - buildA11yProxies(nodes) turns the *exact same* FlowElements the canvas
//     draws into one proxy per visible entity, each carrying its GRAPH-space
//     box and an aria-label naming kind/identity/status/badges (so screen
//     readers and the command palette read the same names the canvas shows).
//     Proxies are viewport-independent: pan/zoom is applied once, as a single
//     CSS transform on the layer container (TopologyA11yLayer), so tracking
//     the canvas costs one transform update per frame, not N DOM writes.
//   - entityAriaLabel(data) is that label builder, exported on its own so
//     T-905's badge/label snapshot tests and T-903's palette can produce
//     identical text without duplicating the format.
//   - rovingOrder / nextRovingId give a stable visual-adjacency traversal
//     order for arrow-key roving focus (T-903 AC4, T-905 AC3) — stable across
//     pan/zoom precisely because it is computed in graph space.
//   - proxyScreenRect(proxy, viewport) resolves a proxy to screen px for any
//     consumer that needs it (e.g. T-903 scrolling an entity into view).
//
// Nothing here imports React or a canvas — plain data in, plain data out — so
// the whole a11y contract is unit-testable (a11yBridge.test.ts).
import type { Node as FlowNode } from "@xyflow/react";
import type { EntityNodeData } from "./EntityNode";
import type { Viewport, Size, Rect } from "./canvasScene";
import { nodeRect, graphToScreen, DEFAULT_NODE_SIZE } from "./canvasScene";

/** The badge tokens that get an amber "management path" treatment on the map
 * (docs/features/topology.md §3) — mirrored here so the accessible label
 * spells them out in words rather than leaving a screen-reader user with the
 * bare token. Mirrors EntityNode.tsx's MGMT_BADGE_LABEL. */
const MGMT_BADGE_PHRASE: Record<string, string> = {
  mgmt: "carries the management IP",
  corosync: "carries a corosync link",
  "mgmt-path": "on the management path",
};

/**
 * Builds the human/screen-reader label for one map entity, naming its kind,
 * identity, live status, and badge list (T-905 AC4's requirement, produced
 * here so every consumer — the a11y proxies, the command palette, snapshot
 * tests — reads identically). The management-path trio is spelled out in
 * words; every other badge is listed verbatim.
 *
 * Examples:
 *   "bridge vmbr0, status ok, carries the management IP"
 *   "bond bond0, status degraded, badges: mode=802.3ad"
 *   "guest group, 23 guests, status ok" (collapsed pill)
 */
export function entityAriaLabel(data: EntityNodeData): string {
  const parts: string[] = [];
  if (data.isGuestGroup) {
    parts.push("guest group");
    parts.push(typeof data.collapsedCount === "number" ? `${String(data.collapsedCount)} guests` : data.label);
  } else {
    parts.push(`${data.kind} ${data.label}`);
  }
  parts.push(`status ${data.status}`);

  const otherBadges = data.badges.filter((b) => !(b in MGMT_BADGE_PHRASE) && b !== "drift");
  for (const b of data.badges) {
    const phrase = MGMT_BADGE_PHRASE[b];
    if (phrase !== undefined) parts.push(phrase);
  }
  if (otherBadges.length > 0) parts.push(`badges: ${otherBadges.join(", ")}`);
  if (data.badges.includes("drift")) parts.push("has configuration drift");
  return parts.join(", ");
}

/** One accessible proxy element's descriptor: everything TopologyA11yLayer
 * needs to render a focusable, correctly-labeled DOM node over the entity's
 * on-canvas box. `graph` is the entity's GRAPH-space box; the layer applies
 * the shared viewport transform, so this is viewport-independent. */
export interface A11yProxy {
  /** Entity id (an inventory Ref, or a "guest-group:" synthetic id). */
  id: string;
  kind: string;
  label: string;
  ariaLabel: string;
  /** Graph-space rect (the same coordinates the canvas draws the entity at). */
  graph: Rect;
  selected: boolean;
  dimmed: boolean;
  isGuestGroup: boolean;
}

/**
 * Projects the canvas's FlowElements nodes into accessible proxies. Same
 * input the renderer draws => the DOM layer and the pixels never disagree.
 * Every entity gets a proxy (no viewport culling — a keyboard/SR user can
 * focus an off-screen entity, which T-905/T-903 use to pan it into view).
 */
export function buildA11yProxies(
  nodes: readonly FlowNode<EntityNodeData, "entity">[],
  size: Size = DEFAULT_NODE_SIZE,
): A11yProxy[] {
  return nodes.map((n) => ({
    id: n.id,
    kind: n.data.kind,
    label: n.data.label,
    ariaLabel: entityAriaLabel(n.data),
    graph: nodeRect(n.position, size),
    selected: n.selected ?? false,
    dimmed: n.data.dimmed,
    isGuestGroup: n.data.isGuestGroup,
  }));
}

/** Resolves a proxy's on-screen rect under the current viewport — for any
 * consumer that needs screen px (T-903 scroll-into-view, hit sync). */
export function proxyScreenRect(proxy: A11yProxy, vp: Viewport): Rect {
  const tl = graphToScreen({ x: proxy.graph.x, y: proxy.graph.y }, vp);
  return { x: tl.x, y: tl.y, width: proxy.graph.width * vp.zoom, height: proxy.graph.height * vp.zoom };
}

/**
 * Stable visual-adjacency traversal order for roving focus: top-to-bottom
 * then left-to-right (reading order over the band layout — guest band on top,
 * physical on the bottom, columns left-to-right), tie-broken by id so the
 * order is deterministic and pan/zoom-invariant. Arrow-key navigation
 * (T-903/T-905) advances through this order.
 */
export function rovingOrder(proxies: readonly A11yProxy[]): A11yProxy[] {
  return [...proxies].sort((a, b) => {
    if (a.graph.y !== b.graph.y) return a.graph.y - b.graph.y;
    if (a.graph.x !== b.graph.x) return a.graph.x - b.graph.x;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}

/**
 * Advances a roving index by `delta` (±1) through `order`, wrapping at both
 * ends, given the currently-active entity id (or undefined = "nothing focused
 * yet", which starts at the first/last entity depending on direction).
 * Returns the id to move focus to, or undefined for an empty order.
 */
export function nextRovingId(
  order: readonly A11yProxy[],
  currentId: string | undefined,
  delta: 1 | -1,
): string | undefined {
  if (order.length === 0) return undefined;
  const idx = currentId === undefined ? -1 : order.findIndex((p) => p.id === currentId);
  if (idx === -1) {
    const first = order[delta === 1 ? 0 : order.length - 1];
    return first?.id;
  }
  const nextIdx = (idx + delta + order.length) % order.length;
  return order[nextIdx]?.id;
}
