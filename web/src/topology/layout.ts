// SPDX-License-Identifier: Apache-2.0

// elkjs-driven auto-layout: node columns per cluster node, layer bands
// within each column (phys/l2/sdn/guest), the SDN band spanning the full
// width rather than belonging to any one node's column
// (docs/features/topology.md §1, §2: "auto-layout via elkjs layered
// algorithm"). React Flow owns rendering; this module only computes {x,y}
// per node id.
//
// Design note (see this task's completion report for the full rationale):
// rather than one global ELK graph with partitioning/compound nodes — whose
// exact cross-band composition semantics are hard to get right without a
// browser to look at the result — each (column, layer-band) *cell* gets its
// own small ELK "layered" sub-graph (direction RIGHT) using only the edges
// whose both endpoints fall in that cell (e.g. a bond→bridge port-of edge,
// both L2, same node). This is a real, if modest, use of ELK's crossing-
// minimizing layered algorithm — it orders/spaces siblings sensibly within
// a cell — while band-row and column-x placement is deterministic (band
// index → y, sorted nodeGroup → x), which is invariant-testable without
// rendering anything: phys is always below l2, which is always below sdn,
// which is always below guest, and columns never overlap in x.
import ELK from "elkjs/lib/elk.bundled.js";
import type { ElkExtendedEdge, ElkNode } from "elkjs/lib/elk-api";
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";

export interface XYPosition {
  x: number;
  y: number;
}

export const NODE_WIDTH = 180;
export const NODE_HEIGHT = 56;
const COLUMN_GUTTER = 96;
const ROW_HEIGHT = 220;
const INTRA_ROW_GUTTER = 40;

// Band order top-to-bottom on screen, per docs/user-guide.md §2: "Physical
// (bottom) ... L2 ... SDN ... Guests (top)". Lower row index = smaller y =
// higher on screen.
const BAND_ROW_INDEX: Record<Layer, number> = { guest: 0, sdn: 1, l2: 2, phys: 3 };

const elk = new ELK();

async function layoutCell(
  cellNodes: TopologyNode[],
  edges: TopologyEdge[],
): Promise<Map<string, XYPosition>> {
  const positions = new Map<string, XYPosition>();
  if (cellNodes.length === 0) return positions;

  const ids = new Set(cellNodes.map((n) => n.id));
  const cellEdges = edges.filter((e) => ids.has(e.from) && ids.has(e.to));

  const graph: ElkNode = {
    id: "cell",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.spacing.nodeNode": String(INTRA_ROW_GUTTER),
      "elk.layered.spacing.nodeNodeBetweenLayers": String(INTRA_ROW_GUTTER * 1.5),
    },
    children: cellNodes.map((n) => ({ id: n.id, width: NODE_WIDTH, height: NODE_HEIGHT })),
    edges: cellEdges.map(
      (e, i): ElkExtendedEdge => ({ id: `e${String(i)}`, sources: [e.from], targets: [e.to] }),
    ),
  };

  const result = await elk.layout(graph);
  for (const child of result.children ?? []) {
    positions.set(child.id, { x: child.x ?? 0, y: child.y ?? 0 });
  }
  return positions;
}

function cellWidth(positions: Map<string, XYPosition>): number {
  let maxRight = 0;
  for (const p of positions.values()) {
    maxRight = Math.max(maxRight, p.x + NODE_WIDTH);
  }
  return maxRight;
}

/** Computes {x,y} for every node in `nodes`, per the column/band contract
 * above. Nodes with nodeGroup === "" (the cluster-spanning SDN band) are
 * laid out in their own full-width row rather than any column's cell. */
export async function computeLayout(
  nodes: TopologyNode[],
  edges: TopologyEdge[],
): Promise<Map<string, XYPosition>> {
  const columnNames = Array.from(new Set(nodes.filter((n) => n.nodeGroup !== "").map((n) => n.nodeGroup))).sort();

  const positions = new Map<string, XYPosition>();
  let xCursor = 0;
  const columnWidths: number[] = [];

  for (const column of columnNames) {
    const columnNodes = nodes.filter((n) => n.nodeGroup === column);
    const cellsByLayer = new Map<Layer, TopologyNode[]>();
    for (const n of columnNodes) {
      if (n.layer === "sdn") continue; // sdn never belongs to a column
      const list = cellsByLayer.get(n.layer);
      if (list) list.push(n);
      else cellsByLayer.set(n.layer, [n]);
    }

    let columnWidth = 0;
    const cellResults: { layer: Layer; positions: Map<string, XYPosition> }[] = [];
    for (const [layer, cellNodes] of cellsByLayer) {
      const cellPositions = await layoutCell(cellNodes, edges);
      columnWidth = Math.max(columnWidth, cellWidth(cellPositions));
      cellResults.push({ layer, positions: cellPositions });
    }

    for (const { layer, positions: cellPositions } of cellResults) {
      const rowY = BAND_ROW_INDEX[layer] * ROW_HEIGHT;
      for (const [id, p] of cellPositions) {
        positions.set(id, { x: xCursor + p.x, y: rowY + p.y });
      }
    }

    columnWidths.push(columnWidth);
    xCursor += columnWidth + COLUMN_GUTTER;
  }

  const totalWidth = Math.max(xCursor - COLUMN_GUTTER, NODE_WIDTH);

  const sdnNodes = nodes.filter((n) => n.nodeGroup === "");
  if (sdnNodes.length > 0) {
    const sdnPositions = await layoutCell(sdnNodes, edges);
    const rawWidth = Math.max(cellWidth(sdnPositions), NODE_WIDTH);
    // Stretch the SDN band's local x-range to visually span the full width
    // of the node columns (docs/features/topology.md §1: "cluster-scoped
    // SDN entities in a band spanning all nodes"), rather than leaving it
    // only as wide as its own node count needs.
    const scale = columnNames.length > 0 ? Math.max(1, totalWidth / rawWidth) : 1;
    const rowY = BAND_ROW_INDEX.sdn * ROW_HEIGHT;
    for (const [id, p] of sdnPositions) {
      positions.set(id, { x: p.x * scale, y: rowY + p.y });
    }
  }

  return positions;
}
