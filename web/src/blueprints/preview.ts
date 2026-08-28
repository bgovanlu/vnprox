// SPDX-License-Identifier: Apache-2.0

// Builds a simple structural preview (nodes + edges) from a blueprint's
// entities, for BlueprintPreviewDiagram's "preview diagram" requirement
// (docs/features/blueprints.md §1: "Each starter carries ... a preview
// diagram"). Framework-free (no React/@xyflow import) so it's directly
// Vitest-able, mirroring web/src/changesets/planPreview.ts's "pure
// function over the wire shape, framework glue lives in the component"
// split. This does not resolve {{param}} placeholders (the preview shows
// the *template*, not one instantiation) — a placeholder token renders
// verbatim as the node's port/parent/zone label, which is exactly what an
// author reviewing the template's shape wants to see.
import type { Blueprint, BlueprintEntityTemplate } from "../api/types";

/** A referenced-but-not-created input (e.g. a bridge's port naming a
 * physical NIC, which blueprints never create themselves) gets kind
 * "input" rather than being mistaken for one of the six entity kinds. */
export type PreviewNodeKind = BlueprintEntityTemplate["kind"] | "input";

export interface PreviewNode {
  id: string;
  label: string;
  kind: PreviewNodeKind;
}

export interface PreviewEdge {
  from: string;
  to: string;
}

export interface BlueprintPreview {
  nodes: PreviewNode[];
  edges: PreviewEdge[];
}

function asStringArray(v: unknown): string[] {
  if (Array.isArray(v)) return v.filter((x): x is string => typeof x === "string");
  if (typeof v === "string") return [v];
  return [];
}

function asString(v: unknown): string | undefined {
  return typeof v === "string" ? v : undefined;
}

/** entityId returns the stable node id this preview uses for one
 * EntityTemplate: its idTemplate verbatim (placeholders included) is
 * already a reasonable, human-readable label — using it directly as the
 * graph id too keeps edge-wiring below a simple string match against
 * other entities' own idTemplate/port-reference fields, with no need to
 * resolve anything. */
function entityId(et: BlueprintEntityTemplate): string {
  return et.idTemplate;
}

/**
 * Builds the {nodes, edges} preview graph for bp: one PreviewNode per
 * entity, plus PreviewEdges for every structural reference a field
 * carries (bridge "ports" -> the bridge, bond "slaves" -> the bond, vlan
 * "parent" -> the vlan, sdn-vnet "zone" -> the vnet, sdn-subnet "vnet" ->
 * the subnet). A referenced name that isn't itself another entity in this
 * blueprint (e.g. a bridge's port naming a physical NIC, which blueprints
 * never create) still gets its own node — the point of the preview is
 * "what does this template touch", including inputs it consumes but
 * doesn't create.
 */
export function buildPreview(bp: Blueprint): BlueprintPreview {
  const nodes = new Map<string, PreviewNode>();
  const edges: PreviewEdge[] = [];

  const ensureNode = (id: string, kind: PreviewNodeKind | undefined): void => {
    if (nodes.has(id)) return;
    nodes.set(id, { id, label: id, kind: kind ?? "input" });
  };

  for (const et of bp.entities) {
    const id = entityId(et);
    nodes.set(id, { id, label: id, kind: et.kind });
  }

  for (const et of bp.entities) {
    const id = entityId(et);
    switch (et.kind) {
      case "bridge":
        for (const port of asStringArray(et.fields.ports)) {
          ensureNode(port, undefined);
          edges.push({ from: port, to: id });
        }
        break;
      case "bond":
        for (const slave of asStringArray(et.fields.slaves)) {
          ensureNode(slave, undefined);
          edges.push({ from: slave, to: id });
        }
        break;
      case "vlan": {
        const parent = asString(et.fields.parent);
        if (parent) {
          ensureNode(parent, undefined);
          edges.push({ from: parent, to: id });
        }
        break;
      }
      case "sdn-vnet": {
        const zone = asString(et.fields.zone);
        if (zone) {
          ensureNode(zone, "sdn-zone");
          edges.push({ from: zone, to: id });
        }
        break;
      }
      case "sdn-subnet": {
        const vnet = asString(et.fields.vnet);
        if (vnet) {
          ensureNode(vnet, "sdn-vnet");
          edges.push({ from: vnet, to: id });
        }
        break;
      }
    }
  }

  return { nodes: [...nodes.values()], edges };
}

/** layerOf orders preview nodes into the diagram's rendering columns
 * (physical/bond -> bridge/vlan -> SDN), for BlueprintPreviewDiagram's
 * simple layered layout. */
export function layerOf(kind: PreviewNodeKind | undefined): number {
  switch (kind) {
    case "input":
    case "bond":
      return 0;
    case "bridge":
    case "vlan":
      return 1;
    case "sdn-zone":
      return 2;
    case "sdn-vnet":
      return 3;
    case "sdn-subnet":
      return 4;
    default:
      return 0;
  }
}
