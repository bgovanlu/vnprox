// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { ManagementPathRef, TopologyNode } from "../api/types";
import { analyzeMgmtSituation, physnicsOfNode } from "./situation";

function physnic(node: string, id: string): TopologyNode {
  return { id: `physnic:${node}:${id}`, kind: "physnic", label: id, layer: "phys", nodeGroup: node, status: "ok", badges: [] };
}

describe("physnicsOfNode", () => {
  it("returns only this node's physnics, sorted", () => {
    const nodes = [physnic("pve1", "eno2"), physnic("pve1", "eno1"), physnic("pve2", "eno1")];
    expect(physnicsOfNode(nodes, "pve1")).toEqual(["eno1", "eno2"]);
  });
});

describe("analyzeMgmtSituation", () => {
  it("single-node SPOF (bridge -> single NIC): offers flow A + C, one candidate", () => {
    const paths: ManagementPathRef[] = [
      { ref: "bridge:pve1:vmbr0", roles: ["mgmt"], path: ["physnic:pve1:eno1"], redundant: false },
    ];
    const nodes = [physnic("pve1", "eno1"), physnic("pve1", "eno2")];
    const s = analyzeMgmtSituation(paths, nodes, "pve1");
    expect(s.carrierRef).toBe("bridge:pve1:vmbr0");
    expect(s.pathNics).toEqual(["eno1"]);
    expect(s.candidateNics).toEqual(["eno2"]);
    expect(s.bondRef).toBeUndefined();
    expect(s.canBondUplink).toBe(true);
    expect(s.canAddSlave).toBe(false);
    expect(s.canDedicatedVlan).toBe(true);
    expect(s.redundant).toBe(false);
  });

  it("already-redundant bond (three-node-vlan pve1): offers flow B + C, no flow A", () => {
    const paths: ManagementPathRef[] = [
      {
        ref: "bridge:pve1:vmbr0",
        roles: ["mgmt", "corosync"],
        path: ["bond:pve1:bond0", "physnic:pve1:eno1", "physnic:pve1:eno2"],
        redundant: true,
      },
    ];
    const nodes = [physnic("pve1", "eno1"), physnic("pve1", "eno2"), physnic("pve1", "eno3")];
    const s = analyzeMgmtSituation(paths, nodes, "pve1");
    expect(s.bondRef).toBe("bond:pve1:bond0");
    expect(s.pathNics).toEqual(["eno1", "eno2"]);
    expect(s.candidateNics).toEqual(["eno3"]);
    expect(s.canBondUplink).toBe(false);
    expect(s.canAddSlave).toBe(true);
    expect(s.canDedicatedVlan).toBe(true);
    expect(s.redundant).toBe(true);
  });

  it("VLAN carrier (vlan-mgmt): flow C available, flow A/B not (no spare NIC to bond)", () => {
    const paths: ManagementPathRef[] = [
      { ref: "vlan:pve1:vmbr0.30", roles: ["mgmt"], path: ["bridge:pve1:vmbr0", "physnic:pve1:eno1"], redundant: false },
    ];
    const nodes = [physnic("pve1", "eno1")];
    const s = analyzeMgmtSituation(paths, nodes, "pve1");
    expect(s.carrierKind).toBe("vlan");
    expect(s.candidateNics).toEqual([]);
    expect(s.canBondUplink).toBe(false); // carrier is a vlan, not a bridge
    expect(s.canAddSlave).toBe(false); // no bond in path
    expect(s.canDedicatedVlan).toBe(true);
  });

  it("no mgmt-role carrier: offers nothing", () => {
    const s = analyzeMgmtSituation([], [physnic("pve1", "eno1")], "pve1");
    expect(s.carrierRef).toBeUndefined();
    expect(s.canBondUplink).toBe(false);
    expect(s.canAddSlave).toBe(false);
    expect(s.canDedicatedVlan).toBe(false);
  });

  it("prefers the mgmt-role ref over a corosync-only one", () => {
    const paths: ManagementPathRef[] = [
      { ref: "bridge:pve1:vmbrcoro", roles: ["corosync"], path: ["physnic:pve1:eno9"], redundant: false },
      { ref: "bridge:pve1:vmbr0", roles: ["mgmt"], path: ["physnic:pve1:eno1"], redundant: false },
    ];
    const s = analyzeMgmtSituation(paths, [physnic("pve1", "eno1"), physnic("pve1", "eno2")], "pve1");
    expect(s.carrierRef).toBe("bridge:pve1:vmbr0");
  });
});
