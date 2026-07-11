// Builds the synthetic {nodes, edges} the wizard preview pane renders
// through the REAL topology components (TopologyCanvas/toFlowElements/
// computeLayout — see WizardPreviewPane.tsx), one function per zone type.
// Framework-free and pure so every shape is directly Vitest-able without
// mounting React Flow. Every synthetic id is namespaced "wizard-preview:"
// so it can never collide with a real inventory Ref (and so nothing here
// is ever mistaken for a live entity if a preview node were somehow
// clicked through to the inspector, which WizardPreviewPane doesn't wire
// up anyway).
import type { EntityStatus, TopologyEdge, TopologyNode } from "../../api/types";
import { computeVxlanMtuDerivation } from "./vxlanMath";

export interface PreviewGraph {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

const PLANNED: EntityStatus = "unknown";
const EMPTY_GRAPH: PreviewGraph = { nodes: [], edges: [] };

function pid(...parts: string[]): string {
  return `wizard-preview:${parts.join(":")}`;
}

function plannedNode(id: string, kind: string, label: string, nodeGroup: string, extraBadges: string[] = []): TopologyNode {
  return { id, kind, label, layer: layerFor(kind), nodeGroup, status: PLANNED, badges: ["planned", ...extraBadges] };
}

/** An *existing* entity referenced by the wizard (e.g. a real bridge the
 * user picked) — rendered without the "planned" badge and with a real
 * "ok" status, so the preview visually distinguishes what already exists
 * from what this changeset would create. */
function existingNode(id: string, kind: string, label: string, nodeGroup: string, extraBadges: string[] = []): TopologyNode {
  return { id, kind, label, layer: layerFor(kind), nodeGroup, status: "ok", badges: extraBadges };
}

function layerFor(kind: string): TopologyNode["layer"] {
  if (kind === "bridge" || kind === "ovs-bridge" || kind === "bond" || kind === "vlan") return "l2";
  if (kind === "sdn-zone" || kind === "sdn-vnet" || kind === "sdn-subnet") return "sdn";
  return "phys";
}

function plannedEdge(from: string, to: string, kind: string, badges: string[] = []): TopologyEdge {
  return { from, to, kind, status: PLANNED, badges: ["planned", ...badges] };
}

// --- shared zone/vnet/subnet scaffold ---------------------------------

export interface ZoneWizardBaseParams {
  zoneId: string;
  zoneType: string;
  memberNodes: string[];
  vnetId: string;
  vnetAlias: string;
  /** Empty string = no subnet (subnet step skipped). */
  subnetCidr: string;
  /** Only meaningful when subnetCidr is set. */
  subnetGateway?: string;
  snat?: boolean;
}

function zoneVnetSubnetGraph(p: ZoneWizardBaseParams, extraZoneBadges: string[] = []): {
  zoneNode: TopologyNode;
  vnetNode: TopologyNode;
  graph: PreviewGraph;
} {
  const zoneNode = plannedNode(pid("zone"), "sdn-zone", p.zoneId || "(zone)", "", extraZoneBadges);
  const vnetLabel = p.vnetAlias ? `${p.vnetId || "(vnet)"} (${p.vnetAlias})` : p.vnetId || "(vnet)";
  const vnetNode = plannedNode(pid("vnet"), "sdn-vnet", vnetLabel, "");
  const nodes = [zoneNode, vnetNode];
  const edges = [plannedEdge(vnetNode.id, zoneNode.id, "zone-of")];

  if (p.subnetCidr) {
    const subnetBadges: string[] = [];
    if (p.subnetGateway) subnetBadges.push(`gw=${p.subnetGateway}`);
    if (p.snat) subnetBadges.push("snat");
    const subnetNode = plannedNode(pid("subnet"), "sdn-subnet", p.subnetCidr, "", subnetBadges);
    nodes.push(subnetNode);
    edges.push(plannedEdge(subnetNode.id, vnetNode.id, "subnet-of"));
  }

  return { zoneNode, vnetNode, graph: { nodes, edges } };
}

function memberBridgeNodes(memberNodes: string[], vnetNode: TopologyNode, bridgeLabel: (node: string) => string, existing: boolean): PreviewGraph {
  const nodes: TopologyNode[] = [];
  const edges: TopologyEdge[] = [];
  for (const node of memberNodes) {
    const bridgeId = pid("bridge", node);
    nodes.push(
      existing ? existingNode(bridgeId, "bridge", bridgeLabel(node), node) : plannedNode(bridgeId, "bridge", bridgeLabel(node), node),
    );
    edges.push(plannedEdge(vnetNode.id, bridgeId, "realizes"));
  }
  return { nodes, edges };
}

function merge(...graphs: PreviewGraph[]): PreviewGraph {
  return {
    nodes: graphs.flatMap((g) => g.nodes),
    edges: graphs.flatMap((g) => g.edges),
  };
}

// --- 1. Simple / SNAT zone ----------------------------------------------

export interface SimpleZoneParams extends ZoneWizardBaseParams {
  /** "" = PVE names the bridge after the zone automatically. */
  bridgeName: string;
}

export function buildSimplePreview(p: SimpleZoneParams): PreviewGraph {
  if (!p.zoneId || p.memberNodes.length === 0) return EMPTY_GRAPH;
  const { vnetNode, graph } = zoneVnetSubnetGraph(p);
  const bridges = memberBridgeNodes(p.memberNodes, vnetNode, () => p.bridgeName || p.zoneId, false);
  return merge(graph, bridges);
}

// --- 2. VLAN zone ---------------------------------------------------------

export interface VlanZoneParams extends ZoneWizardBaseParams {
  bridgeName: string;
  vid: number;
}

export function buildVlanPreview(p: VlanZoneParams): PreviewGraph {
  if (!p.zoneId || !p.bridgeName || p.memberNodes.length === 0) return EMPTY_GRAPH;
  const { vnetNode, graph } = zoneVnetSubnetGraph(p, p.vid > 0 ? [`vid=${String(p.vid)}`] : []);
  const bridges = memberBridgeNodes(p.memberNodes, vnetNode, () => p.bridgeName, true);
  return merge(graph, bridges);
}

// --- 3. QinQ zone ----------------------------------------------------------

export interface QinqZoneParams extends ZoneWizardBaseParams {
  bridgeName: string;
  serviceVid: number;
  customerVid: number;
}

/** Adds the double-tag illustration: two chained "vlan"-kind preview nodes
 * (service tag wrapping customer tag) feeding into the vnet, so the
 * preview graph itself shows the QinQ encapsulation shape docs/features/
 * sdn.md §2 calls for ("double-tag illustration"), not just a badge. */
export function buildQinqPreview(p: QinqZoneParams): PreviewGraph {
  if (!p.zoneId || !p.bridgeName || p.memberNodes.length === 0) return EMPTY_GRAPH;
  const badges = [];
  if (p.serviceVid > 0) badges.push(`s-vid=${String(p.serviceVid)}`);
  if (p.customerVid > 0) badges.push(`c-vid=${String(p.customerVid)}`);
  const { vnetNode, graph } = zoneVnetSubnetGraph(p, badges);

  const serviceNode = plannedNode(pid("qinq-service"), "vlan", `Service tag ${String(p.serviceVid || "?")}`, "");
  const customerNode = plannedNode(pid("qinq-customer"), "vlan", `Customer tag ${String(p.customerVid || "?")}`, "");
  const illustration: PreviewGraph = {
    nodes: [serviceNode, customerNode],
    edges: [
      plannedEdge(customerNode.id, serviceNode.id, "double-tag"),
      plannedEdge(vnetNode.id, customerNode.id, "double-tag"),
    ],
  };

  const bridges = memberBridgeNodes(p.memberNodes, vnetNode, () => p.bridgeName, true);
  return merge(graph, illustration, bridges);
}

// --- 4. VXLAN zone -----------------------------------------------------

export interface VxlanZoneParams extends ZoneWizardBaseParams {
  mtu: number;
  /** The VNet's VNI (tunnel identifier) — VXLAN's analog of a VLAN's VID. */
  vni: number;
  /** node -> peer underlay address (undefined = not yet suggested/entered). */
  peers: Record<string, string | undefined>;
}

export function buildVxlanPreview(p: VxlanZoneParams): PreviewGraph {
  if (!p.zoneId || p.memberNodes.length === 0) return EMPTY_GRAPH;
  const derivation = computeVxlanMtuDerivation(p.mtu);
  const zoneBadges = [`mtu=${String(derivation.requestedMtu || derivation.safeMtu)}`];
  if (derivation.warn) zoneBadges.push("mtu-too-large");
  const { vnetNode, graph } = zoneVnetSubnetGraph(p, zoneBadges);
  if (p.vni > 0) vnetNode.badges.push(`vni=${String(p.vni)}`);

  // VTEP mesh: one node per member with a suggested/entered peer address,
  // full-meshed via vtep-peer edges — mirrors internal/inventory/link.go's
  // EdgeVtepPeer full-mesh construction for real vxlan/evpn zones.
  const peerNodes = p.memberNodes
    .filter((node) => p.peers[node])
    .map((node) => plannedNode(pid("vtep", node), "sdn-zone", p.peers[node] ?? "", node, ["vtep"]));
  const peerEdges: TopologyEdge[] = [];
  for (let i = 0; i < peerNodes.length; i++) {
    for (let j = i + 1; j < peerNodes.length; j++) {
      const a = peerNodes[i];
      const b = peerNodes[j];
      if (!a || !b) continue;
      peerEdges.push(plannedEdge(a.id, b.id, "vtep-peer", [`mtu=${String(derivation.safeMtu)}`]));
    }
  }

  return merge(graph, { nodes: peerNodes, edges: peerEdges });
}

// --- 5. EVPN zone --------------------------------------------------------

export interface EvpnZoneParams extends ZoneWizardBaseParams {
  controller: string;
  asn: number;
  peerAddresses: string[];
  exitNodes: string[];
  /** Only needed for inter-VNet routing; 0 = unset. */
  vrfVxlan: number;
}

/** Adds the BGP session graph: a controller hub node connected to one node
 * per peer address (docs/features/sdn.md §2: "the wizard renders the
 * resulting BGP session graph before creation"). Every exit node is
 * badged "exit"; the first exit node is additionally badged "primary" —
 * this codebase's op vocabulary has no separate primaryExit wire field
 * (see EvpnZoneWizard.tsx's doc comment), so "first in the list" is the
 * UI-only convention. */
export function buildEvpnPreview(p: EvpnZoneParams): PreviewGraph {
  if (!p.zoneId || p.memberNodes.length === 0) return EMPTY_GRAPH;
  const zoneBadges = p.controller ? [`controller=${p.controller}`] : [];
  const { zoneNode, vnetNode, graph } = zoneVnetSubnetGraph(p, zoneBadges);

  const bridges = memberBridgeNodes(
    p.memberNodes,
    vnetNode,
    (node) => (p.exitNodes.includes(node) ? `${node} (exit)` : node),
    false,
  );

  const controllerNode = plannedNode(pid("controller"), "sdn-zone", p.controller ? `${p.controller} (AS${String(p.asn || 0)})` : `AS${String(p.asn || 0)}`, "", [
    "controller",
  ]);
  const peerNodes = p.peerAddresses
    .filter((addr) => addr.trim().length > 0)
    .map((addr, i) => {
      const isExit = i < p.exitNodes.length;
      const badges = isExit ? (i === 0 ? ["exit", "primary"] : ["exit"]) : [];
      return plannedNode(pid("bgp-peer", addr), "sdn-zone", addr, "", badges);
    });
  const sessionEdges = peerNodes.map((peer) => plannedEdge(controllerNode.id, peer.id, "bgp-session"));

  return merge(graph, bridges, {
    nodes: [controllerNode, ...peerNodes],
    edges: [plannedEdge(controllerNode.id, zoneNode.id, "controller-of"), ...sessionEdges],
  });
}
