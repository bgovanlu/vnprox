// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyNode, TopologyResponse } from "../api/types";
import { computeDragOp } from "./dragDropOps";

function node(partial: Partial<TopologyNode> & Pick<TopologyNode, "id" | "kind" | "nodeGroup">): TopologyNode {
  return { label: partial.id, layer: "phys", status: "ok", badges: [], ...partial };
}

const eno1 = node({ id: "physnic:pve1:eno1", kind: "physnic", nodeGroup: "pve1" });
const eno2 = node({ id: "physnic:pve1:eno2", kind: "physnic", nodeGroup: "pve1" });
const eno3 = node({ id: "physnic:pve1:eno3", kind: "physnic", nodeGroup: "pve1" });
const bond0 = node({ id: "bond:pve1:bond0", kind: "bond", nodeGroup: "pve1" });
const vmbr0 = node({ id: "bridge:pve1:vmbr0", kind: "bridge", nodeGroup: "pve1" });
const vmbr0Node2 = node({ id: "bridge:pve2:vmbr0", kind: "bridge", nodeGroup: "pve2" });
const vnet100 = node({ id: "sdn-vnet::vnet100", kind: "sdn-vnet", nodeGroup: "" });
const guestNic = node({ id: "guest-nic:pve1:200/net0", kind: "guest-nic", nodeGroup: "pve1" });

function topologyWith(nodes: TopologyNode[], edges: TopologyResponse["edges"] = []): TopologyResponse {
  return { nodes, edges, layers: [], generatedAt: 1 };
}

describe("computeDragOp", () => {
  it("physnic onto physnic (same node): creates a new bond from both, naming it the next free bondN", () => {
    const topo = topologyWith([eno1, eno2, bond0]);
    const op = computeDragOp(eno1, eno2, topo);
    expect(op).toEqual({
      op: "bond.create",
      target: "bond:pve1:bond1",
      params: { mode: "802.3ad", slaves: ["eno2", "eno1"] },
    });
  });

  it("physnic onto an existing bond: appends as a slave, preserving current slaves", () => {
    const topo = topologyWith([eno3, bond0], [
      { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    ]);
    const op = computeDragOp(eno3, bond0, topo);
    expect(op).toEqual({ op: "bond.update", target: "bond:pve1:bond0", params: { slaves: ["eno1", "eno3"] } });
  });

  it("physnic already a member of the target bond: no-op (undefined)", () => {
    const topo = topologyWith([eno1, bond0], [
      { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    ]);
    expect(computeDragOp(eno1, bond0, topo)).toBeUndefined();
  });

  it("physnic onto a bridge: bridge.port.add", () => {
    const topo = topologyWith([eno3, vmbr0]);
    expect(computeDragOp(eno3, vmbr0, topo)).toEqual({
      op: "bridge.port.add",
      target: "bridge:pve1:vmbr0",
      params: { port: "eno3" },
    });
  });

  it("guest NIC onto a bridge on the same node: guest.nic.update reattach", () => {
    const topo = topologyWith([guestNic, vmbr0]);
    expect(computeDragOp(guestNic, vmbr0, topo)).toEqual({
      op: "guest.nic.update",
      target: "guest-nic:pve1:200/net0",
      params: { bridgeOrVnet: "vmbr0" },
    });
  });

  it("guest NIC onto a cluster-scoped SDN VNet: allowed even though nodeGroups differ", () => {
    const topo = topologyWith([guestNic, vnet100]);
    expect(computeDragOp(guestNic, vnet100, topo)).toEqual({
      op: "guest.nic.update",
      target: "guest-nic:pve1:200/net0",
      params: { bridgeOrVnet: "vnet100" },
    });
  });

  it("cross-node physnic-onto-bridge is not a recognized gesture", () => {
    const topo = topologyWith([eno3, vmbr0Node2]);
    expect(computeDragOp(eno3, vmbr0Node2, topo)).toBeUndefined();
  });

  it("an unrecognized kind pair (e.g. bridge onto bridge) returns undefined", () => {
    const topo = topologyWith([vmbr0, vmbr0Node2]);
    expect(computeDragOp(vmbr0, vmbr0Node2, topo)).toBeUndefined();
  });

  it("dropping a node onto itself is a no-op", () => {
    const topo = topologyWith([eno1]);
    expect(computeDragOp(eno1, eno1, topo)).toBeUndefined();
  });
});
