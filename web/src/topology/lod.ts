// SPDX-License-Identifier: Apache-2.0

// T-902: zoom-driven level-of-detail for the v2 canvas renderer
// (TopologyCanvasV2.tsx). Deliberately framework-free and canvas-free (like
// canvasScene.ts/a11yBridge.ts) — a pure `FlowElements -> FlowElements`
// transform, so the "what does band X actually render" question is
// exhaustively Vitest-able without a browser (lod.test.ts).
//
// This resolves the T-607-flagged gap in docs/features/topology.md §4:
// "physical layer collapses to per-node summary ... has no collapse/
// summarization logic anywhere ... renders every NIC/bond node uncollapsed
// regardless of scale." The lowest zoom band now collapses each cluster
// node's physical layer (NICs + bonds — the exact scope that paragraph
// names) into one per-node summary capsule. See that section for the
// updated text.
//
// Three named zoom bands (docs/features/topology.md §4):
//   - "full"       — every entity renders at full faceplate detail (today's
//                     v1/v2 parity behavior, unchanged).
//   - "simplified" — dense guest-NIC edge groups bundle into one edge with a
//                     count badge (edge bundling, below); the physical layer
//                     stays uncollapsed.
//   - "capsule"     — additionally, each node's physical layer (NICs + bonds)
//                     collapses into one summary capsule.
// Bands are zoom-threshold-driven (crossing a threshold on zoom-in restores
// detail automatically — "unbundles on zoom-in"), with a per-group/per-node
// manual override so a click can force-expand one capsule/bundle regardless
// of the current band ("unbundles on ... click") until the topology data
// itself changes.
//
// Design choice, flagged in the T-902 report: the per-node summary capsule
// and the bundled-edge group are represented as ordinary synthetic
// FlowElements nodes/edges (`data.isGuestGroup`-style pill shape for the
// edge bundle, a plain box for the capsule) rather than new dedicated React
// components — EntityNode.tsx (v1) and canvasDraw.ts (v2) already render any
// entity generically off `EntityNodeData`, including the collapsed-pill look
// keyed only off the `isGuestGroup` boolean (not off a real guest-group id).
// Reusing that existing, already-parity-tested rendering path means zero
// edits to EntityNode.tsx/canvasDraw.ts/a11yBridge.ts were needed to get a
// fully drawn, accessibly-labeled, click-to-expand capsule/bundle — the "per-
// node summary capsule component" and "count badge" deliverables are
// satisfied by this data shape, not a new render component.
import type { Edge as FlowEdge, Node as FlowNode } from "@xyflow/react";
import type { EntityStatus } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import type { XYPosition } from "./layout";
import type { FlowElements } from "./toFlowElements";

export type LodBand = "full" | "simplified" | "capsule";

export interface LodBandDef {
  id: LodBand;
  label: string;
  /** Inclusive lower bound on viewport.zoom for this band to be active —
   * bands are ordered capsule < simplified < full over [MIN_ZOOM, MAX_ZOOM)
   * (canvasScene.ts's zoom clamp range). Chosen so a typical scale-lab-sized
   * cluster's initial fit-to-view zoom (docs/performance.md §3a: comfortably
   * sub-1 at 8 nodes/203 post-collapse elements) lands in "simplified" by
   * default — collapsed-by-default at real scale, full detail one zoom step
   * in, matching the guest-layer collapse-by-default precedent
   * (internal/topology/collapse.go) this card mirrors client-side. */
  minZoom: number;
}

export const LOD_BANDS: readonly LodBandDef[] = [
  { id: "capsule", label: "Capsule (collapsed)", minZoom: 0 },
  { id: "simplified", label: "Simplified", minZoom: 0.2 },
  { id: "full", label: "Full detail", minZoom: 0.5 },
];

/** Resolves a viewport zoom factor to its named band — the single source of
 * truth every consumer (the LOD transform below, TopologyCanvasV2's render
 * loop, tests) uses so "which band is this zoom in" never drifts. */
export function zoomBandFor(zoom: number): LodBand {
  let band: LodBand = "capsule";
  for (const b of LOD_BANDS) {
    if (zoom >= b.minZoom) band = b.id;
  }
  return band;
}

// Mirrors internal/topology/collapse.go's DefaultCollapseThreshold (8): the
// same "more than N children collapse into one pill" rule the backend
// already applies to guest-group pills, reapplied here client-side to any
// guest-NIC edge group that becomes dense again after a pill is expanded
// (expand.ts) or that never collapsed server-side in the first place. This
// repo has no Go<->TS shared-codegen mechanism (docs/development.md) — a
// change to the Go constant without updating this one is a latent bug, not
// caught by either language's compiler. Flagged in the T-902 report.
export const EDGE_BUNDLE_THRESHOLD = 8;

// The exact kind set docs/features/topology.md §4's T-607 gap paragraph
// names: "the physical layer (NICs + bonds)". Deliberately excludes
// lldp-neighbor (also LayerPhysical per internal/topology/project.go's
// layerOf) — those are external-switch annotations on a NIC, not this
// node's own physical-layer entities, and are comparatively few (at most
// one per physnic) — and excludes bridge/ovs-bridge/vlan (LayerL2 but not
// named by the gap text; a bridge is the thing the physical layer attaches
// *to*, not part of it).
const CAPSULE_NODE_KINDS = new Set(["physnic", "bond", "ovs-bond"]);

const LOD_CAPSULE_PREFIX = "lod-capsule:";
const LOD_BUNDLE_PREFIX = "lod-bundle:";

/** Synthetic id for a node-group's collapsed physical-layer capsule. Exported
 * so TopologyCanvasV2 can recognize a click on one without re-deriving the
 * prefix convention. */
export function capsuleId(nodeGroup: string): string {
  return `${LOD_CAPSULE_PREFIX}${nodeGroup}`;
}

/** Synthetic id for a bundled guest-NIC edge group. `groupKey` is
 * `"<targetId>|<sourceNodeGroup>"` — the same key `unbundledGroups` (below)
 * is keyed by, so a click-to-unbundle can add this id's suffix straight into
 * that override set. */
export function bundleId(groupKey: string): string {
  return `${LOD_BUNDLE_PREFIX}${groupKey}`;
}

export interface ParsedLodId {
  kind: "capsule" | "bundle";
  /** For a capsule: the nodeGroup. For a bundle: the "<targetId>|<sourceNodeGroup>" group key. */
  key: string;
}

/** Recognizes and decomposes a synthetic LOD id (capsule or bundle) —
 * undefined for any real inventory ref or guest-group id, so callers can
 * branch "is this a click on a LOD-only entity, or a real one" in one place. */
export function parseLodId(id: string): ParsedLodId | undefined {
  if (id.startsWith(LOD_CAPSULE_PREFIX)) return { kind: "capsule", key: id.slice(LOD_CAPSULE_PREFIX.length) };
  if (id.startsWith(LOD_BUNDLE_PREFIX)) return { kind: "bundle", key: id.slice(LOD_BUNDLE_PREFIX.length) };
  return undefined;
}

export interface LodOptions {
  /** Bundle group keys ("<targetId>|<sourceNodeGroup>") the user has clicked
   * to force-expand — stays expanded regardless of band until the element
   * set changes (new topology data) or the user re-collapses it. */
  unbundledGroups?: ReadonlySet<string>;
  /** Node-group names (cluster node columns) whose physical-layer capsule
   * the user has clicked to force-expand — same override semantics. */
  expandedCapsules?: ReadonlySet<string>;
}

/** A ref/synthetic id's node segment, per inventory.Ref's "kind:node:id"
 * encoding (ParseRef splits on only the first two ':'). Duplicated from
 * expand.ts's private `refNode` rather than importing it — both are the
 * same ~6-line pure parse of a documented, stable wire convention, and this
 * module is deliberately free of cross-file coupling to stay independently
 * testable (same rationale as a11yBridge.ts/canvasScene.ts). */
function refNode(id: string): string {
  const first = id.indexOf(":");
  if (first === -1) return "";
  const second = id.indexOf(":", first + 1);
  if (second === -1) return "";
  return id.slice(first + 1, second);
}

const STATUS_PRIORITY: Record<EntityStatus, number> = { down: 3, degraded: 2, unknown: 1, ok: 0 };

/** Worst-of aggregation for a collapsed group's status (down > degraded >
 * unknown > ok) — the same "one bad member taints the summary" rule
 * collapse.go's own collapseGuests uses (downAny -> StatusDegraded). */
function aggregateStatus(statuses: readonly EntityStatus[]): EntityStatus {
  let worst: EntityStatus = "ok";
  for (const s of statuses) {
    if (STATUS_PRIORITY[s] > STATUS_PRIORITY[worst]) worst = s;
  }
  return worst;
}

function averagePosition(members: readonly { position: XYPosition }[]): XYPosition {
  if (members.length === 0) return { x: 0, y: 0 };
  let sx = 0;
  let sy = 0;
  for (const m of members) {
    sx += m.position.x;
    sy += m.position.y;
  }
  return { x: sx / members.length, y: sy / members.length };
}

type FlowN = FlowNode<EntityNodeData, "entity">;
type FlowE = FlowEdge<EntityEdgeData, "entity">;

interface CapsuleCollapseResult {
  nodes: FlowN[];
  /** Every original node id absorbed into a capsule. */
  collapsedIds: Set<string>;
  /** Original node id -> the capsule id it now maps to (for edge redirect). */
  idToCapsule: Map<string, string>;
}

/** Collapses each cluster node's physical layer (NICs + bonds) into one
 * summary capsule per node-group, skipping any group in `expandedCapsules`.
 * Cluster-scoped entities (nodeGroup === "", never true for physnic/bond in
 * practice, but guarded) never collapse — a capsule is inherently per-node. */
function collapsePhysicalCapsules(nodes: readonly FlowN[], expandedCapsules: ReadonlySet<string>): CapsuleCollapseResult {
  const byGroup = new Map<string, FlowN[]>();
  for (const n of nodes) {
    if (!CAPSULE_NODE_KINDS.has(n.data.kind)) continue;
    const group = refNode(n.id);
    if (group === "") continue;
    const list = byGroup.get(group);
    if (list) list.push(n);
    else byGroup.set(group, [n]);
  }

  const collapsedIds = new Set<string>();
  const idToCapsule = new Map<string, string>();
  const capsules: FlowN[] = [];
  for (const [group, members] of byGroup) {
    if (members.length === 0 || expandedCapsules.has(group)) continue;
    const id = capsuleId(group);
    for (const m of members) {
      collapsedIds.add(m.id);
      idToCapsule.set(m.id, id);
    }
    const nicCount = members.filter((m) => m.data.kind === "physnic").length;
    const bondCount = members.length - nicCount;
    const label = `${String(nicCount)} NIC${nicCount === 1 ? "" : "s"}, ${String(bondCount)} bond${bondCount === 1 ? "" : "s"}`;
    capsules.push({
      id,
      type: "entity",
      position: averagePosition(members),
      selected: false,
      data: {
        label,
        kind: "phys-capsule",
        status: aggregateStatus(members.map((m) => m.data.status)),
        badges: [`count=${String(members.length)}`],
        dimmed: false,
        highlighted: false,
        isGuestGroup: false,
        collapsedCount: members.length,
      },
    });
  }

  if (collapsedIds.size === 0) return { nodes: [...nodes], collapsedIds, idToCapsule };
  const kept = nodes.filter((n) => !collapsedIds.has(n.id));
  return { nodes: [...kept, ...capsules], collapsedIds, idToCapsule };
}

/** Rewrites edges that touched a now-collapsed node: an edge whose both ends
 * collapsed away (e.g. physnic--enslaved-by-->bond, when both are absorbed
 * into the same node's capsule) is dropped; an edge with exactly one
 * collapsed end (e.g. bond--port-of-->bridge) is redirected to the capsule,
 * merging any resulting duplicate (source,target) pairs into one edge with a
 * `links=N` badge rather than drawing N overlapping lines. A no-op copy when
 * nothing collapsed. */
function redirectEdgesForCollapse(edges: readonly FlowE[], collapsedIds: ReadonlySet<string>, idToCapsule: ReadonlyMap<string, string>): FlowE[] {
  if (collapsedIds.size === 0) return [...edges];
  const kept: FlowE[] = [];
  const grouped = new Map<string, FlowE[]>();
  for (const e of edges) {
    const srcCollapsed = collapsedIds.has(e.source);
    const tgtCollapsed = collapsedIds.has(e.target);
    if (!srcCollapsed && !tgtCollapsed) {
      kept.push(e);
      continue;
    }
    if (srcCollapsed && tgtCollapsed) continue; // both ends absorbed — nothing left to draw
    const source = srcCollapsed ? (idToCapsule.get(e.source) ?? e.source) : e.source;
    const target = tgtCollapsed ? (idToCapsule.get(e.target) ?? e.target) : e.target;
    if (source === target) continue; // degenerate self-loop after collapse
    const key = `${source}=>${target}`;
    const list = grouped.get(key);
    if (list) list.push(e);
    else grouped.set(key, [e]);
  }
  for (const [key, group] of grouped) {
    const [source, target] = key.split("=>") as [string, string];
    const first = group[0];
    if (!first?.data) continue;
    const data = first.data;
    kept.push({
      ...first,
      id: `lod-capsule-edge:${key}`,
      source,
      target,
      data: {
        ...data,
        badges: group.length > 1 ? [`links=${String(group.length)}`] : data.badges,
      },
    });
  }
  return kept;
}

/** Bundles dense guest-NIC edge groups (docs/features/topology.md §4's edge
 * bundling deliverable): groups guest-nic-source "attached-to" edges by
 * (target, source's own nodeGroup) — the exact grouping
 * internal/topology/collapse.go's `groups` map already uses server-side —
 * and, for any group over EDGE_BUNDLE_THRESHOLD not present in
 * `unbundledGroups`, replaces the individual source nodes/edges with one
 * bundle node + one edge carrying a `count=N` badge. No-op at the "full"
 * band (bundling only applies once zoomed out) or when nothing exceeds the
 * threshold. */
function bundleGuestEdges(nodes: readonly FlowN[], edges: readonly FlowE[], band: LodBand, unbundledGroups: ReadonlySet<string>): { nodes: FlowN[]; edges: FlowE[] } {
  if (band === "full") return { nodes: [...nodes], edges: [...edges] };

  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  const groups = new Map<string, string[]>();
  for (const e of edges) {
    const src = nodeById.get(e.source);
    if (src?.data.kind !== "guest-nic") continue;
    const key = `${e.target}|${refNode(e.source)}`;
    const list = groups.get(key);
    if (list) list.push(e.source);
    else groups.set(key, [e.source]);
  }

  const toBundle = new Map<string, string[]>();
  for (const [key, ids] of groups) {
    if (ids.length > EDGE_BUNDLE_THRESHOLD && !unbundledGroups.has(key)) toBundle.set(key, ids);
  }
  if (toBundle.size === 0) return { nodes: [...nodes], edges: [...edges] };

  const bundledSourceIds = new Set<string>();
  for (const ids of toBundle.values()) for (const id of ids) bundledSourceIds.add(id);

  const keptNodes = nodes.filter((n) => !bundledSourceIds.has(n.id));
  const keptEdges = edges.filter((e) => !bundledSourceIds.has(e.source));

  const bundleNodes: FlowN[] = [];
  const bundleEdges: FlowE[] = [];
  for (const [key, ids] of toBundle) {
    const [target] = key.split("|") as [string, string];
    const members = ids.map((id) => nodeById.get(id)).filter((m): m is FlowN => m !== undefined);
    const id = bundleId(key);
    bundleNodes.push({
      id,
      type: "entity",
      position: averagePosition(members),
      selected: false,
      data: {
        label: `${String(ids.length)} guests`,
        kind: "guest-nic-bundle",
        status: aggregateStatus(members.map((m) => m.data.status)),
        badges: [`count=${String(ids.length)}`],
        dimmed: false,
        highlighted: false,
        isGuestGroup: true,
        collapsedCount: ids.length,
      },
    });
    bundleEdges.push({
      id: `lod-bundle-edge:${key}`,
      source: id,
      target,
      type: "entity",
      data: {
        status: aggregateStatus(members.map((m) => m.data.status)),
        badges: [`count=${String(ids.length)}`],
        dimmed: false,
        highlighted: false,
      },
    });
  }

  return { nodes: [...keptNodes, ...bundleNodes], edges: [...keptEdges, ...bundleEdges] };
}

/** The LOD transform: `FlowElements` (already the exact v1/v2-shared
 * toFlowElements output) in, band-adjusted `FlowElements` out. Pure and
 * total — never throws, degrades to a pass-through copy when nothing in
 * the given band/override combination applies. */
export function applyLod(elements: FlowElements, band: LodBand, opts: LodOptions = {}): FlowElements {
  const unbundledGroups = opts.unbundledGroups ?? new Set<string>();
  const expandedCapsules = opts.expandedCapsules ?? new Set<string>();

  let nodes: readonly FlowN[] = elements.nodes;
  let edges: readonly FlowE[] = elements.edges;

  if (band === "capsule") {
    const collapsed = collapsePhysicalCapsules(nodes, expandedCapsules);
    nodes = collapsed.nodes;
    edges = redirectEdgesForCollapse(edges, collapsed.collapsedIds, collapsed.idToCapsule);
  }

  const bundled = bundleGuestEdges(nodes, edges, band, unbundledGroups);
  return { nodes: bundled.nodes, edges: bundled.edges };
}
