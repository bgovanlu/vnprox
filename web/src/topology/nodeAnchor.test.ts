// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyNode } from "../api/types";
import { buildNodeAnchorResolver } from "./nodeAnchor";

function node(id: string, nodeGroup: string): TopologyNode {
  return { id, kind: "bridge", label: id, layer: "l2", nodeGroup, status: "ok", badges: [] };
}

describe("buildNodeAnchorResolver", () => {
  it("resolves a node name to the lexicographically first entity id in its band", () => {
    const resolve = buildNodeAnchorResolver([
      node("physnic:pve1:eno2", "pve1"),
      node("bridge:pve1:vmbr0", "pve1"),
      node("bridge:pve2:vmbr0", "pve2"),
    ]);
    // "bridge:pve1:vmbr0" < "physnic:pve1:eno2" lexicographically.
    expect(resolve("pve1")).toBe("bridge:pve1:vmbr0");
    expect(resolve("pve2")).toBe("bridge:pve2:vmbr0");
  });

  it("returns undefined for a node with no rendered entities at all", () => {
    const resolve = buildNodeAnchorResolver([node("bridge:pve1:vmbr0", "pve1")]);
    expect(resolve("pve9")).toBeUndefined();
  });

  it("never resolves the cluster-spanning SDN band sentinel (nodeGroup '') as a node name", () => {
    const resolve = buildNodeAnchorResolver([{ id: "sdn-zone::z1", kind: "sdn-zone", label: "z1", layer: "sdn", nodeGroup: "", status: "ok", badges: [] }]);
    expect(resolve("")).toBeUndefined();
  });

  it("is stable across repeated calls (memoizable) for the same input", () => {
    const nodes = [node("bridge:pve1:vmbr0", "pve1")];
    const resolve = buildNodeAnchorResolver(nodes);
    expect(resolve("pve1")).toBe(resolve("pve1"));
  });
});
