// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyResponse } from "../api/types";
import { summarizeFound } from "./foundSummary";

function topology(overrides: Partial<TopologyResponse> = {}): TopologyResponse {
  return {
    nodes: [],
    edges: [],
    layers: ["phys", "l2", "sdn", "guest"],
    generatedAt: 0,
    ...overrides,
  };
}

describe("summarizeFound", () => {
  it("returns all zeros for undefined (still loading)", () => {
    expect(summarizeFound(undefined)).toEqual({
      clusterNodes: [],
      byLayer: { phys: 0, l2: 0, sdn: 0, guest: 0 },
      totalEntities: 0,
      totalEdges: 0,
    });
  });

  it("counts distinct cluster nodes and per-layer entities from a three-node-vlan-shaped topology", () => {
    const t = topology({
      nodes: [
        { id: "physnic:pve1:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
        { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
        { id: "physnic:pve2:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve2", status: "ok", badges: [] },
        { id: "bridge:pve2:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve2", status: "ok", badges: [] },
        { id: "bridge:pve3:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve3", status: "ok", badges: [] },
        { id: "sdn-zone:vlanz", kind: "sdn-zone", label: "vlanz", layer: "sdn", nodeGroup: "", status: "ok", badges: [] },
        { id: "guest:pve1:100", kind: "guest", label: "app01", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
      ],
      edges: [{ from: "physnic:pve1:eno1", to: "bridge:pve1:vmbr0", kind: "member", status: "ok", badges: [] }],
    });

    const summary = summarizeFound(t);
    expect(summary.clusterNodes).toEqual(["pve1", "pve2", "pve3"]);
    expect(summary.byLayer).toEqual({ phys: 2, l2: 3, sdn: 1, guest: 1 });
    expect(summary.totalEntities).toBe(7);
    expect(summary.totalEdges).toBe(1);
  });

  it("excludes the cluster-spanning SDN band (nodeGroup '') from clusterNodes", () => {
    const t = topology({
      nodes: [{ id: "sdn-zone:z", kind: "sdn-zone", label: "z", layer: "sdn", nodeGroup: "", status: "ok", badges: [] }],
    });
    expect(summarizeFound(t).clusterNodes).toEqual([]);
  });
});
