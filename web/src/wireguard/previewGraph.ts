// Builds the "connect two clusters" wizard's live preview pane graph
// (rendered through WizardPreviewPane — the REAL map components, exactly
// like every SDN zone wizard's own preview: see sdn/wizards/
// previewEntities.ts's identical doc comment). Framework-free and pure so
// it's directly Vitest-able without mounting React Flow. Deliberately kept
// self-contained (not importing sdn/wizards/previewEntities.ts's private
// node/edge helpers, which aren't exported) rather than reaching into
// another feature's wizard internals.
import type { EntityStatus, TopologyEdge, TopologyNode } from "../api/types";
import { WG_ENDPOINT_KIND, wgEndpointNodeId } from "./wgTunnelEdges";
import type { ConnectClustersParams } from "./wizardOps";

export interface WgPreviewGraph {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

const EMPTY_GRAPH: WgPreviewGraph = { nodes: [], edges: [] };

/** "Planned" (not-yet-applied) entities render distinctly from real ones —
 * same status/badge convention sdn/wizards/previewEntities.ts's PLANNED
 * constant and plannedNode/plannedEdge use. */
const PLANNED: EntityStatus = "unknown";

function shortKey(key: string): string {
  return key.length <= 8 ? key : `${key.slice(0, 8)}…`;
}

/** Builds the preview graph for the current form state. Empty until both a
 * source node and a peer public key are entered — an incomplete form has
 * nothing meaningful to draw (mirrors previewEntities.ts's EMPTY_GRAPH
 * early-return for an unnamed zone). `tunnelId` must be the exact same id
 * wizardOps.ts's buildConnectClustersOps will be called with at submit
 * time (see that file's doc comment on why) — this function never
 * generates one itself. */
export function buildConnectClustersPreview(p: ConnectClustersParams, tunnelId: string): WgPreviewGraph {
  if (!p.sourceNode || !p.peerPublicKey) return EMPTY_GRAPH;

  const localId = `node:${p.sourceNode}:${p.sourceNode}`;
  const peerId = wgEndpointNodeId(tunnelId, p.peerPublicKey);
  const peerLabel = p.peerEndpoint || `${shortKey(p.peerPublicKey)} (no fixed endpoint)`;

  const nodes: TopologyNode[] = [
    // The source node already exists — rendered as a real, "ok" entity
    // (previewEntities.ts's "existingNode" convention), not "planned".
    { id: localId, kind: "node", label: p.sourceNode, layer: "phys", nodeGroup: p.sourceNode, status: "ok", badges: [] },
    {
      id: peerId,
      kind: WG_ENDPOINT_KIND,
      label: peerLabel,
      layer: "phys",
      nodeGroup: "",
      status: PLANNED,
      badges: ["planned", "external"],
    },
  ];
  const edges: TopologyEdge[] = [{ from: localId, to: peerId, kind: "wg-tunnel", status: PLANNED, badges: ["planned"] }];

  return { nodes, edges };
}
