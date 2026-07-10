// Glue between the pure projection logic (projection.ts — deliberately
// framework-free) and React Flow's Node/Edge shapes. Kept as its own module
// so projection.ts's hover/VLAN/layer logic stays exhaustively Vitest-able
// without ever importing @xyflow/react, while this file is still a plain
// function (no rendering) and just as unit-testable on its own.
import type { Edge as FlowEdge, Node as FlowNode } from "@xyflow/react";
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import { computeHoverHighlight, computeVlanMatch, filterByLayers, isGuestGroupId } from "./projection";
import type { XYPosition } from "./layout";

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
}

export interface FlowElements {
  nodes: FlowNode<EntityNodeData, "entity">[];
  edges: FlowEdge<EntityEdgeData, "entity">[];
}

function edgeId(e: TopologyEdge): string {
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
    return {
      id: n.id,
      type: "entity",
      position,
      selected: n.id === selectedId,
      data: {
        label: n.label,
        kind: n.kind,
        status: n.status,
        badges: n.badges,
        dimmed: vlanMatch ? !vlanMatch.nodes.has(n.id) : false,
        stale: staleNodeGroups?.has(n.nodeGroup) ?? false,
        highlighted,
        isGuestGroup: isGuestGroupId(n.id),
        collapsedCount: n.collapsedCount,
      },
    };
  });

  const flowEdges: FlowEdge<EntityEdgeData, "entity">[] = visibleEdges.map((e) => {
    const highlighted = hoverSet ? hoverSet.has(e.from) && hoverSet.has(e.to) : false;
    return {
      id: edgeId(e),
      source: e.from,
      target: e.to,
      type: "entity",
      data: {
        status: e.status,
        badges: e.badges,
        dimmed: vlanMatch ? !vlanMatch.edges.has(e) : false,
        highlighted,
      },
    };
  });

  return { nodes: flowNodes, edges: flowEdges };
}
