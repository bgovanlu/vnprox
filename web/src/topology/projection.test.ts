// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../api/types";
import {
  computeHoverHighlight,
  computeVlanMatch,
  filterByLayers,
  isGuestGroupId,
  isPhysGroupId,
  parseGuestGroupId,
  parsePhysGroupId,
} from "./projection";

function node(partial: Partial<TopologyNode> & Pick<TopologyNode, "id" | "kind" | "layer">): TopologyNode {
  return {
    label: partial.id,
    nodeGroup: "pve1",
    status: "ok",
    badges: [],
    ...partial,
  };
}

function edge(from: string, to: string, kind: string, badges: string[] = []): TopologyEdge {
  return { from, to, kind, status: "ok", badges };
}

describe("isGuestGroupId / parseGuestGroupId", () => {
  it("recognizes the guest-group prefix and parses node + targetRef", () => {
    const id = "guest-group:pve1:bridge:pve1:vmbr0";
    expect(isGuestGroupId(id)).toBe(true);
    expect(parseGuestGroupId(id)).toEqual({ node: "pve1", targetRef: "bridge:pve1:vmbr0" });
  });

  it("handles a target ref whose id contains ':' (e.g. an sdn-vnet path)", () => {
    // targetRef itself is "sdn-vnet::zone1/vnet1" (Node="" for cluster-scoped,
    // so the ref string is "sdn-vnet::zone1/vnet1" — two consecutive colons).
    const id = "guest-group:pve1:sdn-vnet::zone1/vnet1";
    expect(parseGuestGroupId(id)).toEqual({ node: "pve1", targetRef: "sdn-vnet::zone1/vnet1" });
  });

  it("is not a guest-group id for an ordinary inventory ref", () => {
    const id = "bridge:pve1:vmbr0";
    expect(isGuestGroupId(id)).toBe(false);
    expect(parseGuestGroupId(id)).toBeUndefined();
  });
});

describe("isPhysGroupId / parsePhysGroupId", () => {
  it("recognizes the phys-group prefix and parses the node name", () => {
    const id = "phys-group:pve1";
    expect(isPhysGroupId(id)).toBe(true);
    expect(parsePhysGroupId(id)).toEqual({ node: "pve1" });
  });

  it("is not a phys-group id for a guest-group id or an ordinary inventory ref", () => {
    for (const id of ["guest-group:pve1:bridge:pve1:vmbr0", "physnic:pve1:eno1", "bridge:pve1:vmbr0"]) {
      expect(isPhysGroupId(id)).toBe(false);
      expect(parsePhysGroupId(id)).toBeUndefined();
    }
  });

  it("rejects a malformed id with an empty node name", () => {
    expect(parsePhysGroupId("phys-group:")).toBeUndefined();
  });
});

describe("filterByLayers", () => {
  const nodes: TopologyNode[] = [
    node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys" }),
    node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2" }),
    node({ id: "guest:pve1:100", kind: "guest", layer: "guest" }),
  ];
  const edges: TopologyEdge[] = [
    edge("physnic:pve1:eno1", "bond:pve1:bond0", "enslaved-by"),
    edge("guest:pve1:100", "bond:pve1:bond0", "attached-to"),
  ];

  it("keeps only nodes in the active layer set and drops dangling edges", () => {
    const result = filterByLayers(nodes, edges, new Set(["phys", "l2"]));
    expect(result.nodes.map((n) => n.id)).toEqual(["physnic:pve1:eno1", "bond:pve1:bond0"]);
    // The guest→bond edge is dropped: "guest" is not an active layer, so its
    // endpoint is gone and the edge would otherwise dangle.
    expect(result.edges).toHaveLength(1);
    expect(result.edges[0]).toMatchObject({ from: "physnic:pve1:eno1", to: "bond:pve1:bond0" });
  });

  it("returns everything when every layer is active", () => {
    const result = filterByLayers(nodes, edges, new Set(["phys", "l2", "sdn", "guest"]));
    expect(result.nodes).toHaveLength(3);
    expect(result.edges).toHaveLength(2);
  });
});

describe("computeHoverHighlight", () => {
  // guest nic --attached-to--> bridge --port-of(from bond)--> bond is wrong
  // direction; port-of goes port->bridge, so bond--port-of-->bridge.
  // bond <--enslaved-by-- physnic (physnic is slave/child of bond).
  // physnic --lldp-adjacent--> lldp neighbor.
  const nodes: TopologyNode[] = [
    node({ id: "guest-nic:pve1:100/net0", kind: "guest-nic", layer: "guest" }),
    node({ id: "guest-nic:pve1:101/net0", kind: "guest-nic", layer: "guest" }), // sibling on same bridge
    node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2" }),
    node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2" }),
    node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys" }),
    node({ id: "physnic:pve1:eno2", kind: "physnic", layer: "phys" }), // other bond slave
    node({ id: "lldp-neighbor:pve1:eno1@sw1", kind: "lldp-neighbor", layer: "phys" }),
  ];
  const edges: TopologyEdge[] = [
    edge("guest-nic:pve1:100/net0", "bridge:pve1:vmbr0", "attached-to", ["vid=20"]),
    edge("guest-nic:pve1:101/net0", "bridge:pve1:vmbr0", "attached-to", ["vid=20"]),
    edge("bond:pve1:bond0", "bridge:pve1:vmbr0", "port-of"),
    edge("physnic:pve1:eno1", "bond:pve1:bond0", "enslaved-by"),
    edge("physnic:pve1:eno2", "bond:pve1:bond0", "enslaved-by"),
    edge("physnic:pve1:eno1", "lldp-neighbor:pve1:eno1@sw1", "lldp-adjacent"),
  ];
  const nodesById = new Map(nodes.map((n) => [n.id, n]));

  it("highlights a guest NIC's full path to the physical NIC and switch, without pulling in the sibling guest", () => {
    const highlighted = computeHoverHighlight(nodesById, edges, "guest-nic:pve1:100/net0");
    expect(highlighted).toEqual(
      new Set([
        "guest-nic:pve1:100/net0",
        "bridge:pve1:vmbr0",
        "bond:pve1:bond0",
        "physnic:pve1:eno1",
        "physnic:pve1:eno2",
        "lldp-neighbor:pve1:eno1@sw1",
      ]),
    );
    expect(highlighted.has("guest-nic:pve1:101/net0")).toBe(false);
  });

  it("hovering the bridge directly reveals its guest attachments too (it's the hub itself)", () => {
    const highlighted = computeHoverHighlight(nodesById, edges, "bridge:pve1:vmbr0");
    expect(highlighted.has("guest-nic:pve1:100/net0")).toBe(true);
    expect(highlighted.has("guest-nic:pve1:101/net0")).toBe(true);
    expect(highlighted.has("bond:pve1:bond0")).toBe(true);
  });

  it("hovering a lone physnic reaches its bond and LLDP neighbor but no guests", () => {
    const highlighted = computeHoverHighlight(nodesById, edges, "physnic:pve1:eno2");
    expect(highlighted.has("bond:pve1:bond0")).toBe(true);
    expect(highlighted.has("bridge:pve1:vmbr0")).toBe(true);
    expect(highlighted.has("guest-nic:pve1:100/net0")).toBe(false);
  });
});

describe("computeVlanMatch", () => {
  const nodes: TopologyNode[] = [
    node({ id: "vlan:pve1:vmbr0.20", kind: "vlan", layer: "l2", badges: ["vid=20"] }),
    node({ id: "bridge:pve1:vmbr1", kind: "bridge", layer: "l2", badges: ["vlans=10-30"] }),
    node({ id: "bridge:pve1:vmbr2", kind: "bridge", layer: "l2", badges: [] }), // plain bridge, no vlan badge itself
    node({ id: "guest-nic:pve1:100/net0", kind: "guest-nic", layer: "guest", badges: ["vid=20"] }),
    node({ id: "sdn-vnet::zone1/vnet1", kind: "sdn-vnet", layer: "sdn", badges: ["tag=20"] }),
    node({ id: "guest:pve1:999", kind: "guest", layer: "guest", badges: [] }), // unrelated
  ];
  const taggedEdge = edge("guest-nic:pve1:100/net0", "bridge:pve1:vmbr2", "attached-to", ["vid=20"]);
  const edges: TopologyEdge[] = [taggedEdge];

  it("matches nodes carrying the VID directly via vid=/tag=/vlans= badges", () => {
    const match = computeVlanMatch(nodes, [], 20);
    expect(match.nodes.has("vlan:pve1:vmbr0.20")).toBe(true);
    expect(match.nodes.has("bridge:pve1:vmbr1")).toBe(true); // 20 within 10-30
    expect(match.nodes.has("sdn-vnet::zone1/vnet1")).toBe(true); // tag=20
    expect(match.nodes.has("guest:pve1:999")).toBe(false);
  });

  it("does not match a bridge outside its trunked range", () => {
    const match = computeVlanMatch(
      [node({ id: "bridge:pve1:vmbr1", kind: "bridge", layer: "l2", badges: ["vlans=10-15"] })],
      [],
      20,
    );
    expect(match.nodes.size).toBe(0);
  });

  it("pulls in an edge's other endpoint (a plain, non-VLAN-aware bridge) via the tagged edge itself", () => {
    const match = computeVlanMatch(nodes, edges, 20);
    expect(match.nodes.has("bridge:pve1:vmbr2")).toBe(true);
    expect(match.edges.has(taggedEdge)).toBe(true);
  });
});
