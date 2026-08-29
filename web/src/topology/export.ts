// SPDX-License-Identifier: Apache-2.0

// Map export serialization (T-906, closing the T-607-flagged "export the
// physical layer" gap — docs/features/topology.md §4). Deliberately
// framework-free — no React, no @xyflow/react rendering, no DOM/Canvas API
// — so the "what does the exported file contain" logic stays exhaustively
// Vitest-able, mirroring the discipline projection.ts/canvasScene.ts already
// follow (see those files' header comments). The browser-only download/
// rasterization mechanism lives in exportDownload.ts instead, per the task
// card's "independent of the browser download mechanism" instruction.
//
// Both Topology views feed this module:
//   - Graph view:  sceneFromFlowElements(elements) — the *same* FlowElements
//     (toFlowElements.ts) both renderers (v1 DOM, v2 canvas) draw, so the
//     export always matches what's on screen (post layer-toggle/VLAN-filter/
//     collapse — and, under the v2 renderer, post-LOD: TopologyPage feeds
//     the LOD-transformed scene here once the v2 canvas reports it via its
//     onSceneChange prop — see TopologyCanvasV2.tsx).
//   - Switch view: sceneFromSwitchTopology(topology, opts) — re-derives the
//     equivalent entity set from SwitchModel (switchModel.ts), since that
//     view has no FlowElements at all.
//
// VLAN-filter policy: toFlowElements/SwitchView only *dim* non-matching
// entities (low opacity, still on screen) rather than removing them, since
// dimmed context is useful while navigating live. An export is a static
// artifact meant to document one specific, intentional view of the network
// (docs/features/topology.md §4's gap note: "a dedicated map export"), so
// this module drops non-matching entities entirely instead of carrying
// dimmed noise into the file — "only the filtered/toggled entity set" (task
// card AC1), not a fainter copy of everything.
import type { EntityStatus, Layer } from "../api/types";
import type { SceneTheme } from "./canvasDraw";
import { DEFAULT_NODE_SIZE } from "./canvasScene";
import type { SwitchTopology } from "./switchModel";
import { switchCarriesVlan } from "./switchModel";
import type { FlowElements } from "./toFlowElements";

export interface ExportNode {
  id: string;
  kind: string;
  label: string;
  status: EntityStatus;
  badges: string[];
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface ExportEdge {
  id: string;
  from: string;
  to: string;
  status: EntityStatus;
}

export interface ExportScene {
  nodes: ExportNode[];
  edges: ExportEdge[];
}

/** Builds the export scene for the Graph view straight from the FlowElements
 * both renderers already draw — no re-derivation of layer/VLAN/collapse
 * logic here, so the export can never drift from what's on screen. Nodes
 * (and any edge touching a dropped node) whose `dimmed` flag is set — the
 * VLAN filter's "doesn't match" marker (see toFlowElements.ts) — are
 * excluded; see this file's header comment for why. */
export function sceneFromFlowElements(elements: FlowElements): ExportScene {
  const nodes: ExportNode[] = [];
  for (const n of elements.nodes) {
    if (n.data.dimmed) continue;
    nodes.push({
      id: n.id,
      kind: n.data.kind,
      label: n.data.label,
      status: n.data.status,
      badges: n.data.badges,
      x: n.position.x,
      y: n.position.y,
      width: DEFAULT_NODE_SIZE.width,
      height: DEFAULT_NODE_SIZE.height,
    });
  }
  const nodeIds = new Set(nodes.map((n) => n.id));
  const edges: ExportEdge[] = [];
  for (const e of elements.edges) {
    if (e.data?.dimmed) continue;
    if (!nodeIds.has(e.source) || !nodeIds.has(e.target)) continue;
    edges.push({ id: e.id, from: e.source, to: e.target, status: e.data?.status ?? "ok" });
  }
  return { nodes, edges };
}

export interface SwitchSceneOptions {
  activeLayers: ReadonlySet<Layer>;
  vlanFilter?: number;
}

const SWITCH_COLUMN_GAP = 260;
const SWITCH_ROW_GAP = 220;
const LEAF_COLUMN_GAP = 90;
const LEAF_HEIGHT = 40;
const LEAF_ROW_OFFSET = DEFAULT_NODE_SIZE.height + 24;

/** Re-derives an export scene for the Switch (faceplate) view from
 * SwitchModel — mirroring SwitchView.tsx/SwitchFaceplate.tsx's own
 * filtering rules (the source of truth for what that view actually shows)
 * rather than duplicating a second projection:
 *   - a layer toggle hides that layer's *slot* on every switch card
 *     (uplinks/vlans/access ports/vnets — SwitchView.tsx's
 *     showUplinks/showVlans/showPorts/showVnets), not the switch itself;
 *   - the VLAN filter drops a whole switch when none of its slots carry the
 *     filtered VID (switchCarriesVlan — the exact predicate SwitchFaceplate
 *     dims by), consistent with this module's "exclude, don't dim" export
 *     policy above.
 * Synthesizes a simple grid layout (switch faceplates have no {x,y} of
 * their own — they're not part of the elk-laid-out graph) so the resulting
 * scene can go through the same renderSvg the Graph view's scene does. */
export function sceneFromSwitchTopology(topology: SwitchTopology, opts: SwitchSceneOptions): ExportScene {
  const nodes: ExportNode[] = [];
  const edges: ExportEdge[] = [];
  const { activeLayers, vlanFilter } = opts;

  let col = 0;
  for (const group of topology.nodes) {
    let row = 0;
    const addLeaf = (
      swRef: string,
      baseX: number,
      baseY: number,
      leafIndex: number,
      id: string,
      kind: string,
      label: string,
      status: EntityStatus,
      badges: string[],
    ): void => {
      nodes.push({
        id,
        kind,
        label,
        status,
        badges,
        x: baseX + leafIndex * LEAF_COLUMN_GAP,
        y: baseY + LEAF_ROW_OFFSET,
        width: LEAF_COLUMN_GAP - 10,
        height: LEAF_HEIGHT,
      });
      edges.push({ id: `${swRef}=>${id}`, from: swRef, to: id, status });
    };

    for (const sw of group.switches) {
      if (vlanFilter !== undefined && !switchCarriesVlan(sw, vlanFilter)) {
        continue;
      }
      const baseX = col * SWITCH_COLUMN_GAP;
      const baseY = row * SWITCH_ROW_GAP;
      nodes.push({
        id: sw.ref,
        kind: sw.kind,
        label: `${group.node}: ${sw.name}`,
        status: sw.status,
        badges: sw.badges,
        x: baseX,
        y: baseY,
        width: DEFAULT_NODE_SIZE.width,
        height: DEFAULT_NODE_SIZE.height,
      });

      let leafIndex = 0;
      if (activeLayers.has("phys")) {
        for (const uplink of sw.uplinks) {
          for (const member of uplink.members) {
            addLeaf(sw.ref, baseX, baseY, leafIndex, member.ref, "physnic", member.label, member.status, member.badges);
            leafIndex += 1;
          }
        }
      }
      if (activeLayers.has("l2")) {
        for (const vlan of sw.vlans) {
          addLeaf(sw.ref, baseX, baseY, leafIndex, vlan.ref, "vlan", vlan.label, vlan.status, []);
          leafIndex += 1;
        }
      }
      if (activeLayers.has("guest")) {
        for (const port of sw.accessPorts) {
          addLeaf(
            sw.ref,
            baseX,
            baseY,
            leafIndex,
            port.ref,
            port.isGroup ? "guest-group" : "guest-nic",
            port.label,
            port.status,
            port.badges,
          );
          leafIndex += 1;
        }
      }
      if (activeLayers.has("sdn")) {
        for (const vnet of sw.vnets) {
          addLeaf(sw.ref, baseX, baseY, leafIndex, vnet.ref, "sdn-vnet", vnet.label, vnet.status, []);
          leafIndex += 1;
        }
      }
      row += 1;
    }

    if (activeLayers.has("phys")) {
      for (const port of group.freePorts) {
        nodes.push({
          id: port.ref,
          kind: "physnic",
          label: `${group.node}: ${port.label}`,
          status: port.status,
          badges: port.badges,
          x: col * SWITCH_COLUMN_GAP,
          y: row * SWITCH_ROW_GAP,
          width: DEFAULT_NODE_SIZE.width,
          height: DEFAULT_NODE_SIZE.height,
        });
        row += 1;
      }
    }
    col += 1;
  }

  return { nodes, edges };
}

/** Total visible entity count (nodes + edges) — the number the task card's
 * acceptance criteria ("the exported SVG's entity count") assert against. */
export function sceneEntityCount(scene: ExportScene): number {
  return scene.nodes.length + scene.edges.length;
}

export interface CaptionParams {
  viewMode: "graph" | "switch";
  activeLayers: ReadonlySet<Layer>;
  layerOrder: readonly Layer[];
  vlanFilter?: number;
  generatedAt?: Date;
}

const LAYER_LABEL: Record<Layer, string> = { phys: "Physical", l2: "L2", sdn: "SDN", guest: "Guests" };

/** The caption lines both the SVG/PNG export (embedded as on-image text)
 * and the print stylesheet (rendered as an on-page caption — see
 * TopologyPage.tsx's `print:block` block) use to record which filters were
 * active, so a printed/exported map is self-describing rather than a bare
 * picture with no legend (task card deliverable 2: "current filter/legend
 * state printed as a caption"). */
export function buildCaptionLines(params: CaptionParams): string[] {
  const hidden = params.layerOrder.filter((l) => !params.activeLayers.has(l));
  const lines: string[] = [
    `vnprox topology map — ${params.viewMode === "graph" ? "Graph" : "Switch"} view`,
    hidden.length > 0 ? `Layers hidden: ${hidden.map((l) => LAYER_LABEL[l]).join(", ")}` : "All layers shown",
    params.vlanFilter !== undefined ? `VLAN filter: ${String(params.vlanFilter)}` : "No VLAN filter",
    `Generated ${(params.generatedAt ?? new Date()).toISOString()}`,
  ];
  return lines;
}

export interface RenderSvgOptions {
  captionLines?: string[];
  padding?: number;
  /** The resolved scene palette — the SAME object `TopologyCanvasV2` draws
   * the live map from.
   *
   * T-4301 said an export that carries its own palette "stops matching the
   * screen it was exported from", and measuring this one showed that is not
   * a forecast. Its `STATUS_COLOR` was a FOURTH copy of the status scale
   * (T-4302 found three), and its surface pair was worse than stale — it was
   * backwards: light mode drew nodes at `#f8fafc` on a `#ffffff` page, i.e.
   * DARKER than the page, while the design language's ladder puts a raised
   * surface LIGHTER than the page (`#ffffff` on `#fafbfd`). The exported
   * picture had the surface ladder inverted, in the theme most exports are
   * taken in. Its `ok` border also measured 2.45 against the fill it was
   * drawn on, versus 3.25 for the token.
   *
   * Required, not optional: a default here would be a fallback palette, and
   * canvasPalette.ts's whole argument is that a fallback which drifts renders
   * plausibly while being wrong. Resolving it is the caller's job because
   * only the caller has a document — `renderSvg` stays a pure string builder,
   * which is what lets export.test.ts drive it with a stub. */
  palette: SceneTheme;
}

/** The export's status colours, taken from the same palette the canvas uses.
 * `ok` and `unknown` both read `nodeBorderOk` (`--color-outline`), matching
 * canvasDraw's `statusBorder` — the dash that separates them on screen has no
 * counterpart in this flat SVG, which is worth knowing rather than papering
 * over: an exported map states `unknown` less clearly than the live one. */
function exportStatusColor(status: EntityStatus, palette: SceneTheme): string {
  switch (status) {
    case "down":
      return palette.statusDown;
    case "degraded":
      return palette.statusDegraded;
    case "unknown":
      return palette.statusUnknown;
    default:
      return palette.nodeBorderOk;
  }
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&apos;");
}

/** Serializes an ExportScene to a standalone SVG document string (self-
 * contained: no external stylesheet/font references, safe to save/open on
 * its own). Every node renders as a `<g data-export-entity="node"
 * data-entity-ref="...">`; every edge as a `<line data-export-entity="edge"
 * data-entity-ref="...">` — stable hooks a Playwright/DOM-parsing test can
 * count without depending on visual layout (AC1/AC4: "parsing the exported
 * SVG's entity count"). The root `<svg>` additionally carries
 * data-export-node-count/data-export-edge-count summary attributes for a
 * cheap, non-traversing assertion. */
export function renderSvg(scene: ExportScene, opts: RenderSvgOptions): string {
  const padding = opts.padding ?? 32;
  const captionLines = opts.captionLines ?? [];
  const palette = opts.palette;
  const bg = palette.background;
  const fg = palette.nodeText;
  const nodeFill = palette.nodeFill;

  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of scene.nodes) {
    minX = Math.min(minX, n.x);
    minY = Math.min(minY, n.y);
    maxX = Math.max(maxX, n.x + n.width);
    maxY = Math.max(maxY, n.y + n.height);
  }
  if (!Number.isFinite(minX)) {
    minX = 0;
    minY = 0;
    maxX = 0;
    maxY = 0;
  }

  const captionHeight = captionLines.length > 0 ? captionLines.length * 16 + 16 : 0;
  const contentWidth = Math.max(1, maxX - minX);
  const contentHeight = Math.max(1, maxY - minY);
  const width = contentWidth + padding * 2;
  const height = contentHeight + padding * 2 + captionHeight;
  const offsetX = padding - minX;
  const offsetY = padding - minY;

  const byId = new Map(scene.nodes.map((n) => [n.id, n]));
  const parts: string[] = [];
  parts.push(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${String(width)}" height="${String(height)}" ` +
      `viewBox="0 0 ${String(width)} ${String(height)}" ` +
      `data-export-node-count="${String(scene.nodes.length)}" data-export-edge-count="${String(scene.edges.length)}">`,
  );
  parts.push(`<rect x="0" y="0" width="${String(width)}" height="${String(height)}" fill="${bg}" />`);

  parts.push(`<g data-export-group="edges">`);
  for (const e of scene.edges) {
    const from = byId.get(e.from);
    const to = byId.get(e.to);
    if (!from || !to) continue;
    const ax = from.x + from.width / 2 + offsetX;
    const ay = from.y + from.height / 2 + offsetY;
    const bx = to.x + to.width / 2 + offsetX;
    const by = to.y + to.height / 2 + offsetY;
    parts.push(
      `<line data-export-entity="edge" data-entity-ref="${escapeXml(e.id)}" ` +
        `x1="${String(ax)}" y1="${String(ay)}" x2="${String(bx)}" y2="${String(by)}" ` +
        `stroke="${exportStatusColor(e.status, palette)}" stroke-width="1.5" />`,
    );
  }
  parts.push(`</g>`);

  parts.push(`<g data-export-group="nodes">`);
  for (const n of scene.nodes) {
    const x = n.x + offsetX;
    const y = n.y + offsetY;
    parts.push(
      `<g data-export-entity="node" data-entity-ref="${escapeXml(n.id)}" data-entity-kind="${escapeXml(n.kind)}">` +
        `<rect x="${String(x)}" y="${String(y)}" width="${String(n.width)}" height="${String(n.height)}" rx="6" ` +
        `fill="${nodeFill}" stroke="${exportStatusColor(n.status, palette)}" stroke-width="1.5" />` +
        `<text x="${String(x + 6)}" y="${String(y + n.height / 2)}" font-size="10" ` +
        `font-family="ui-sans-serif, system-ui, sans-serif" fill="${fg}" dominant-baseline="middle">` +
        `${escapeXml(n.label)}</text>` +
        `</g>`,
    );
  }
  parts.push(`</g>`);

  if (captionLines.length > 0) {
    const capY = contentHeight + padding + 20;
    parts.push(`<g data-export-group="caption">`);
    captionLines.forEach((line, i) => {
      parts.push(
        `<text x="${String(padding)}" y="${String(capY + i * 16)}" font-size="11" ` +
          `font-family="ui-sans-serif, system-ui, sans-serif" fill="${fg}">${escapeXml(line)}</text>`,
      );
    });
    parts.push(`</g>`);
  }

  parts.push(`</svg>`);
  return parts.join("");
}
