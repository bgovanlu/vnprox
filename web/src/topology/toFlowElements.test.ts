// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../api/types";
import { toFlowElements } from "./toFlowElements";

function node(partial: Partial<TopologyNode> & Pick<TopologyNode, "id" | "kind" | "layer" | "nodeGroup">): TopologyNode {
  return { label: partial.id, status: "ok", badges: [], ...partial };
}

describe("toFlowElements", () => {
  const baseNodes: TopologyNode[] = [
    node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", nodeGroup: "pve1" }),
    node({
      id: "guest-group:pve1:bridge:pve1:vmbr0",
      kind: "guest-group",
      layer: "guest",
      nodeGroup: "pve1",
      collapsedCount: 12,
      badges: ["count=12"],
    }),
  ];
  const baseEdges: TopologyEdge[] = [
    { from: "guest-group:pve1:bridge:pve1:vmbr0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  ];

  const allLayers = new Set(["phys", "l2", "sdn", "guest"] as const);

  it("renders the collapsed pill by default", () => {
    const { nodes } = toFlowElements({
      nodes: baseNodes,
      edges: baseEdges,
      expandedGroups: new Set(),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    expect(nodes.map((n) => n.id)).toContain("guest-group:pve1:bridge:pve1:vmbr0");
  });

  it("replaces the pill + its edge with the expanded members once the group is expanded", () => {
    const extraNodes: TopologyNode[] = [
      node({ id: "guest-nic:pve1:100/net0", kind: "guest-nic", layer: "guest", nodeGroup: "pve1" }),
    ];
    const extraEdges: TopologyEdge[] = [
      { from: "guest-nic:pve1:100/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    ];
    const { nodes, edges } = toFlowElements({
      nodes: baseNodes,
      edges: baseEdges,
      extraNodes,
      extraEdges,
      expandedGroups: new Set(["guest-group:pve1:bridge:pve1:vmbr0"]),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    expect(nodes.map((n) => n.id)).not.toContain("guest-group:pve1:bridge:pve1:vmbr0");
    expect(nodes.map((n) => n.id)).toContain("guest-nic:pve1:100/net0");
    expect(edges.some((e) => e.source === "guest-group:pve1:bridge:pve1:vmbr0")).toBe(false);
    expect(edges.some((e) => e.source === "guest-nic:pve1:100/net0")).toBe(true);
  });

  it("dims nodes/edges that don't carry the active VLAN filter", () => {
    const nodes: TopologyNode[] = [
      node({ id: "vlan:pve1:vmbr0.20", kind: "vlan", layer: "l2", nodeGroup: "pve1", badges: ["vid=20"] }),
      node({ id: "vlan:pve1:vmbr0.30", kind: "vlan", layer: "l2", nodeGroup: "pve1", badges: ["vid=30"] }),
    ];
    const { nodes: flowNodes } = toFlowElements({
      nodes,
      edges: [],
      expandedGroups: new Set(),
      activeLayers: allLayers,
      vlanFilter: 20,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const byId = new Map(flowNodes.map((n) => [n.id, n]));
    expect(byId.get("vlan:pve1:vmbr0.20")?.data.dimmed).toBe(false);
    expect(byId.get("vlan:pve1:vmbr0.30")?.data.dimmed).toBe(true);
  });

  it("uses a manual (dragged) position over the computed layout position", () => {
    const { nodes } = toFlowElements({
      nodes: [node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", nodeGroup: "pve1" })],
      edges: [],
      expandedGroups: new Set(),
      activeLayers: allLayers,
      layoutPositions: new Map([["bridge:pve1:vmbr0", { x: 10, y: 10 }]]),
      manualPositions: { "bridge:pve1:vmbr0": { x: 999, y: 888 } },
    });
    expect(nodes[0]?.position).toEqual({ x: 999, y: 888 });
  });

  it("drops edges whose endpoint was filtered out by the active layer set", () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
      node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2", nodeGroup: "pve1" }),
    ];
    const edges: TopologyEdge[] = [
      { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    ];
    const { edges: flowEdges } = toFlowElements({
      nodes,
      edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["l2"]),
      layoutPositions: new Map(),
      manualPositions: {},
    });
    expect(flowEdges).toHaveLength(0);
  });

  // docs/features/topology.md §5: a stale node-scoped collector source
  // greys exactly that node's band; other bands and the cluster-spanning
  // SDN band (nodeGroup === "") are untouched.
  it("marks exactly the stale node band's nodes as stale", () => {
    const nodes: TopologyNode[] = [
      node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", nodeGroup: "pve1" }),
      node({ id: "bridge:pve2:vmbr0", kind: "bridge", layer: "l2", nodeGroup: "pve2" }),
      node({ id: "sdn-vnet::vlanz/vnet100", kind: "sdn-vnet", layer: "sdn", nodeGroup: "" }),
    ];
    const { nodes: flowNodes } = toFlowElements({
      nodes,
      edges: [],
      expandedGroups: new Set(),
      activeLayers: allLayers,
      staleNodeGroups: new Set(["pve2"]),
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const staleById = new Map(flowNodes.map((n) => [n.id, n.data.stale]));
    expect(staleById.get("bridge:pve1:vmbr0")).toBe(false);
    expect(staleById.get("bridge:pve2:vmbr0")).toBe(true);
    expect(staleById.get("sdn-vnet::vlanz/vnet100")).toBe(false);
  });

  it("marks nothing stale when staleNodeGroups is absent (healthy topology)", () => {
    const { nodes: flowNodes } = toFlowElements({
      nodes: baseNodes,
      edges: baseEdges,
      expandedGroups: new Set(),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    expect(flowNodes.every((n) => n.data.stale === false)).toBe(true);
  });

  // T-806 "Verify live": pathHighlight.verifyNodeId/verifyOutcome/
  // verifyDiverges project onto exactly the probed source's own node, never
  // onto any other node on the traced path.
  it("projects the verify overlay onto exactly the probed source node", () => {
    const { nodes: flowNodes } = toFlowElements({
      nodes: baseNodes,
      edges: baseEdges,
      expandedGroups: new Set(),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
      pathHighlight: {
        nodeIds: new Set(["bridge:pve1:vmbr0"]),
        edgeIds: new Set(),
        missingNodeIds: new Set(),
        verdict: "deny",
        verifyNodeId: "bridge:pve1:vmbr0",
        verifyOutcome: "reachable",
        verifyDiverges: true,
      },
    });
    const byId = new Map(flowNodes.map((n) => [n.id, n.data]));
    expect(byId.get("bridge:pve1:vmbr0")?.verifyOutcome).toBe("reachable");
    expect(byId.get("bridge:pve1:vmbr0")?.verifyDiverges).toBe(true);
    expect(byId.get("guest-group:pve1:bridge:pve1:vmbr0")?.verifyOutcome).toBeUndefined();
    expect(byId.get("guest-group:pve1:bridge:pve1:vmbr0")?.verifyDiverges).toBe(false);
  });

  it("leaves verifyOutcome/verifyDiverges unset when pathHighlight has no verify data", () => {
    const { nodes: flowNodes } = toFlowElements({
      nodes: baseNodes,
      edges: baseEdges,
      expandedGroups: new Set(),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
      pathHighlight: { nodeIds: new Set(), edgeIds: new Set(), missingNodeIds: new Set(), verdict: "allow" },
    });
    expect(flowNodes.every((n) => n.data.verifyOutcome === undefined && n.data.verifyDiverges === false)).toBe(true);
  });
});

// T-1907: a "phys-group:<node>" pill flows through the exact same generic
// expand/collapse mechanism a guest-group pill already uses (mergedNodes/
// mergedEdges just check expandedGroups.has(id), regardless of prefix) — the
// only thing toFlowElements itself needs to add is the isPhysGroup data flag
// EntityNode/canvasDraw key their pill styling off (AC3: this must not
// perturb the isGuestGroup flag or guest-group's own expand behavior above).
describe("toFlowElements — T-1907 phys-group pill", () => {
  const physNodes: TopologyNode[] = [
    node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2", nodeGroup: "pve1" }),
    node({
      id: "phys-group:pve1",
      kind: "phys-group",
      layer: "phys",
      nodeGroup: "pve1",
      collapsedCount: 10,
      badges: ["count=10"],
    }),
  ];
  const physEdges: TopologyEdge[] = [
    { from: "phys-group:pve1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: ["count=2"] },
  ];
  const allLayers = new Set(["phys", "l2", "sdn", "guest"] as const);

  it("flags the phys-group pill's node data isPhysGroup (and never isGuestGroup)", () => {
    const { nodes } = toFlowElements({
      nodes: physNodes,
      edges: physEdges,
      expandedGroups: new Set(),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const byId = new Map(nodes.map((n) => [n.id, n.data]));
    expect(byId.get("phys-group:pve1")?.isPhysGroup).toBe(true);
    expect(byId.get("phys-group:pve1")?.isGuestGroup).toBe(false);
    expect(byId.get("bond:pve1:bond0")?.isPhysGroup).toBe(false);
  });

  it("replaces the pill + its edge with the expanded member NICs once expanded", () => {
    const extraNodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
    ];
    const extraEdges: TopologyEdge[] = [
      { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    ];
    const { nodes, edges } = toFlowElements({
      nodes: physNodes,
      edges: physEdges,
      extraNodes,
      extraEdges,
      expandedGroups: new Set(["phys-group:pve1"]),
      activeLayers: allLayers,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    expect(nodes.map((n) => n.id)).not.toContain("phys-group:pve1");
    expect(nodes.map((n) => n.id)).toContain("physnic:pve1:eno1");
    expect(edges.some((e) => e.source === "phys-group:pve1")).toBe(false);
    expect(edges.some((e) => e.source === "physnic:pve1:eno1" && e.target === "bond:pve1:bond0")).toBe(true);
  });
});
