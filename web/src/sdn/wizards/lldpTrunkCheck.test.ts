// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../../api/types";
import {
  bridgePhysNicRefs,
  checkVlanTrunk,
  findBridgeRef,
  lldpNeighborRefsForPhysNics,
  parseTaggedVlans,
  type LldpNeighborTrunkInfo,
} from "./lldpTrunkCheck";

function node(id: string, kind: string, nodeGroup: string, label: string): TopologyNode {
  return { id, kind, label, layer: kind === "bridge" ? "l2" : "phys", nodeGroup, status: "ok", badges: [] };
}

// A bonded-uplink shape mirroring three-node-vlan.yaml: bond0 (eno1+eno2)
// is a port of vmbr0; each physnic has its own LLDP neighbor.
const edges: TopologyEdge[] = [
  { from: "bond:pve1:bond0", to: "bridge:pve1:vmbr0", kind: "port-of", status: "ok", badges: [] },
  { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
  { from: "physnic:pve1:eno2", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
  {
    from: "physnic:pve1:eno1",
    to: "lldp-neighbor:pve1:eno1/sw1",
    kind: "lldp-adjacent",
    status: "ok",
    badges: [],
  },
  {
    from: "physnic:pve1:eno2",
    to: "lldp-neighbor:pve1:eno2/sw2",
    kind: "lldp-adjacent",
    status: "ok",
    badges: [],
  },
];

const topologyNodes: TopologyNode[] = [
  node("bridge:pve1:vmbr0", "bridge", "pve1", "vmbr0"),
  node("bond:pve1:bond0", "bond", "pve1", "bond0"),
];

describe("bridgePhysNicRefs", () => {
  it("resolves through a bond port to its slave physnics", () => {
    const refs = bridgePhysNicRefs(edges, "bridge:pve1:vmbr0");
    expect(refs.sort()).toEqual(["physnic:pve1:eno1", "physnic:pve1:eno2"]);
  });

  it("resolves a direct physnic port with no bond in between", () => {
    const directEdges: TopologyEdge[] = [
      { from: "physnic:pve2:eno1", to: "bridge:pve2:vmbr0", kind: "port-of", status: "ok", badges: [] },
    ];
    expect(bridgePhysNicRefs(directEdges, "bridge:pve2:vmbr0")).toEqual(["physnic:pve2:eno1"]);
  });

  it("returns [] for a bridge with no ports in the edge set", () => {
    expect(bridgePhysNicRefs(edges, "bridge:pve9:ghost")).toEqual([]);
  });
});

describe("lldpNeighborRefsForPhysNics", () => {
  it("resolves both neighbors for the bonded pair", () => {
    const refs = lldpNeighborRefsForPhysNics(edges, ["physnic:pve1:eno1", "physnic:pve1:eno2"]);
    expect(refs.sort()).toEqual(["lldp-neighbor:pve1:eno1/sw1", "lldp-neighbor:pve1:eno2/sw2"]);
  });

  it("returns [] when no physnic has an lldp-adjacent edge", () => {
    expect(lldpNeighborRefsForPhysNics(edges, ["physnic:pve9:ghost"])).toEqual([]);
  });
});

describe("findBridgeRef", () => {
  it("finds an existing bridge by node+name", () => {
    expect(findBridgeRef(topologyNodes, "pve1", "vmbr0")).toBe("bridge:pve1:vmbr0");
  });

  it("returns undefined for an unknown bridge/node", () => {
    expect(findBridgeRef(topologyNodes, "pve1", "vmbr9")).toBeUndefined();
    expect(findBridgeRef(topologyNodes, "pve9", "vmbr0")).toBeUndefined();
  });
});

describe("parseTaggedVlans", () => {
  it("parses a comma list", () => {
    expect(parseTaggedVlans("100,200,300")).toEqual([100, 200, 300]);
  });
  it("handles undefined/empty as no tagged vlans", () => {
    expect(parseTaggedVlans(undefined)).toEqual([]);
    expect(parseTaggedVlans("")).toEqual([]);
  });
  it("ignores non-numeric/zero garbage defensively", () => {
    expect(parseTaggedVlans("100, ,abc,0,200")).toEqual([100, 200]);
  });
});

describe("checkVlanTrunk", () => {
  const neighbors: LldpNeighborTrunkInfo[] = [
    { ref: "lldp-neighbor:pve1:eno1/sw1", chassisName: "sw-core-01", portId: "Te1/0/1", taggedVlans: [100, 200, 300] },
    { ref: "lldp-neighbor:pve3:eno1/sw1", chassisName: "sw-core-01", portId: "Te1/0/3", taggedVlans: [100, 200] },
  ];

  it("warns, naming the port, for every neighbor missing the vid — T-403 AC2", () => {
    const warnings = checkVlanTrunk(neighbors, 300);
    expect(warnings).toEqual([
      { neighborRef: "lldp-neighbor:pve3:eno1/sw1", chassisName: "sw-core-01", portId: "Te1/0/3" },
    ]);
  });

  it("no warnings when every neighbor trunks the vid", () => {
    expect(checkVlanTrunk(neighbors, 100)).toEqual([]);
  });

  it("returns [] (not a false all-clear) for an empty neighbor list", () => {
    expect(checkVlanTrunk([], 100)).toEqual([]);
  });
});
