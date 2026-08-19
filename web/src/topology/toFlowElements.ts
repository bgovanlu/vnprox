// Glue between the pure projection logic (projection.ts — deliberately
// framework-free) and React Flow's Node/Edge shapes. Kept as its own module
// so projection.ts's hover/VLAN/layer logic stays exhaustively Vitest-able
// without ever importing @xyflow/react, while this file is still a plain
// function (no rendering) and just as unit-testable on its own.
import type { Edge as FlowEdge, Node as FlowNode } from "@xyflow/react";
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import { computeHoverHighlight, computeVlanMatch, filterByLayers, isGuestGroupId, isPhysGroupId } from "./projection";
import { resolveEdgeUtilizationRef } from "./trafficMode";
import type { XYPosition } from "./layout";
import type { PathHighlight } from "../simulator/pathHighlight";

export interface ToFlowElementsParams {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  /** Extra nodes/edges synthesized for currently-expanded guest-group pills
   * (see expand.ts); the pill itself is dropped once its group is expanded. */
  extraNodes?: TopologyNode[];
  extraEdges?: TopologyEdge[];
  expandedGroups: ReadonlySet<string>;
  activeLayers: ReadonlySet<Layer>;
  vlanFilter?: number;
  hoveredId?: string;
  selectedId?: string;
  /** Node bands (TopologyNode.nodeGroup values) whose collector data is
   * stale — those nodes render greyed from last-known data
   * (docs/features/topology.md §5; see staleness.ts's summarizeStaleness).
   * The "" (cluster-spanning SDN band) group never matches a node-scoped
   * source, so SDN entities are only ever greyed by cluster-wide staleness,
   * which the banner covers instead. */
  staleNodeGroups?: ReadonlySet<string>;
  layoutPositions: ReadonlyMap<string, XYPosition>;
  manualPositions: Readonly<Record<string, XYPosition>>;
  /** "Traffic" paint mode (docs/features/monitoring.md §1). false (the
   * default) leaves every edge on its normal status-driven style. */
  trafficMode?: boolean;
  /** Ref -> current utilizationPct (see internal/metrics.LiveMetric),
   * consulted only when trafficMode is true. Missing/undefined when no
   * live metrics have arrived yet — edges then render at trafficMode's
   * "idle" look (see trafficEdgeStyle) rather than reverting to status
   * colors, so the paint mode itself is always visually distinguishable
   * once toggled on. */
  utilizationByRef?: ReadonlyMap<string, number>;
  /** Path simulator overlay (T-504, docs/features/firewall.md §5's "the
   * hop-by-hop path rendered on the topology map"): paints the traced
   * path's nodes/edges with the verdict's color and marks the blocking
   * point / missing-link edge distinctly. Reuses this same "highlight a
   * specific path with a status color" mechanism the hover chain-highlight
   * above already established, rather than a second overlay system —
   * see web/src/simulator/pathHighlight.ts for how hops become node/edge
   * id sets. undefined (the default) leaves every node/edge unaffected.
   * T-806: `verifyNodeId`/`verifyOutcome`/`verifyDiverges` (set via
   * pathHighlight.ts's `withVerifyHighlight`) additionally mark the probed
   * source with a distinct observed-outcome/divergence indicator once a
   * live "Verify live" result has come back. */
  pathHighlight?: PathHighlight;
}

export interface FlowElements {
  nodes: FlowNode<EntityNodeData, "entity">[];
  edges: FlowEdge<EntityEdgeData, "entity">[];
}

/** The React Flow edge id convention this module and the path simulator's
 * hop->edge matching (web/src/simulator/pathHighlight.ts) both rely on —
 * exported so the latter never has to reinvent or drift from it. */
export function edgeId(e: TopologyEdge): string {
  return `${e.from}=>${e.to}::${e.kind}`;
}

export function toFlowElements(params: ToFlowElementsParams): FlowElements {
  const {
    nodes,
    edges,
    extraNodes = [],
    extraEdges = [],
    expandedGroups,
    activeLayers,
    vlanFilter,
    hoveredId,
    selectedId,
    staleNodeGroups,
    layoutPositions,
    manualPositions,
    trafficMode = false,
    utilizationByRef,
    pathHighlight,
  } = params;

  // Expanded guest-group pills are superseded by their synthesized members:
  // drop the pill node and its single synthetic attachment edge.
  const mergedNodes = [...nodes.filter((n) => !expandedGroups.has(n.id)), ...extraNodes];
  const mergedEdges = [...edges.filter((e) => !expandedGroups.has(e.from)), ...extraEdges];

  const { nodes: visibleNodes, edges: visibleEdges } = filterByLayers(mergedNodes, mergedEdges, activeLayers);

  const vlanMatch =
    vlanFilter !== undefined && vlanFilter > 0 ? computeVlanMatch(visibleNodes, visibleEdges, vlanFilter) : undefined;

  const nodesById = new Map(visibleNodes.map((n) => [n.id, n]));
  const hoverSet =
    hoveredId !== undefined && (nodesById.has(hoveredId) || isGuestGroupId(hoveredId))
      ? computeHoverHighlight(nodesById, visibleEdges, hoveredId)
      : undefined;

  const flowNodes: FlowNode<EntityNodeData, "entity">[] = visibleNodes.map((n) => {
    const position = manualPositions[n.id] ?? layoutPositions.get(n.id) ?? { x: 0, y: 0 };
    const highlighted = hoverSet ? hoverSet.has(n.id) : false;
    const onPath = pathHighlight?.nodeIds.has(n.id) ?? false;
    const isMissing = pathHighlight?.missingNodeIds.has(n.id) ?? false;
    const isBlocking = pathHighlight?.blockingNodeId === n.id;
    return {
      id: n.id,
      type: "entity",
      position,
      selected: n.id === selectedId,
      // A reasonable placeholder box (see EntityNode.tsx's own `minWidth:
      // 140` and typical badge-row height) so React Flow considers the
      // node "measured" immediately (@xyflow/system's nodeHasDimensions
      // accepts initialWidth/initialHeight as a fallback ahead of its own
      // ResizeObserver-driven `measured` value) rather than rendering it
      // `visibility: hidden` until a real measurement arrives — which, in
      // at least one observed headless-browser environment, never fires
      // at all (see this task's report). A real measurement, when it does
      // arrive, still overrides this via `measured`.
      initialWidth: 160,
      initialHeight: 64,
      data: {
        label: n.label,
        kind: n.kind,
        status: n.status,
        badges: n.badges,
        findings: n.findings,
        dimmed: vlanMatch ? !vlanMatch.nodes.has(n.id) : false,
        stale: staleNodeGroups?.has(n.nodeGroup) ?? false,
        highlighted,
        isGuestGroup: isGuestGroupId(n.id),
        isPhysGroup: isPhysGroupId(n.id),
        collapsedCount: n.collapsedCount,
        simVerdict: onPath || isMissing ? pathHighlight?.verdict : undefined,
        simRole: isMissing ? "missing" : isBlocking ? "blocking" : onPath ? "path" : undefined,
        verifyOutcome: pathHighlight?.verifyNodeId === n.id ? pathHighlight.verifyOutcome : undefined,
        verifyDiverges: pathHighlight?.verifyNodeId === n.id ? (pathHighlight.verifyDiverges ?? false) : false,
      },
    };
  });

  const flowEdges: FlowEdge<EntityEdgeData, "entity">[] = visibleEdges.map((e) => {
    const highlighted = hoverSet ? hoverSet.has(e.from) && hoverSet.has(e.to) : false;
    const utilizationRef =
      trafficMode && utilizationByRef
        ? resolveEdgeUtilizationRef(e.from, e.to, (ref) => nodesById.get(ref)?.kind, utilizationByRef)
        : undefined;
    const onPathEdge = pathHighlight?.edgeIds.has(edgeId(e)) ?? false;
    return {
      id: edgeId(e),
      source: e.from,
      target: e.to,
      type: "entity",
      data: {
        status: e.status,
        badges: e.badges,
        findings: e.findings,
        dimmed: vlanMatch ? !vlanMatch.edges.has(e) : false,
        highlighted,
        trafficMode,
        utilizationPct: utilizationRef !== undefined ? utilizationByRef?.get(utilizationRef) : undefined,
        simVerdict: onPathEdge ? pathHighlight?.verdict : undefined,
      },
    };
  });

  return { nodes: flowNodes, edges: flowEdges };
}
