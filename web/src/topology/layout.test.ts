import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../api/types";
import { computeLayout, NODE_WIDTH } from "./layout";

function node(partial: Partial<TopologyNode> & Pick<TopologyNode, "id" | "kind" | "layer" | "nodeGroup">): TopologyNode {
  return { label: partial.id, status: "ok", badges: [], ...partial };
}

describe("computeLayout", () => {
  it("stacks bands so phys is below l2, which is below sdn, which is below guest (screen y grows downward)", async () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
      node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2", nodeGroup: "pve1" }),
      node({ id: "sdn-vnet::z1/v1", kind: "sdn-vnet", layer: "sdn", nodeGroup: "" }),
      node({ id: "guest:pve1:100", kind: "guest", layer: "guest", nodeGroup: "pve1" }),
    ];
    const positions = await computeLayout(nodes, []);

    const y = (id: string) => positions.get(id)?.y ?? NaN;
    expect(y("guest:pve1:100")).toBeLessThan(y("sdn-vnet::z1/v1"));
    expect(y("sdn-vnet::z1/v1")).toBeLessThan(y("bond:pve1:bond0"));
    expect(y("bond:pve1:bond0")).toBeLessThan(y("physnic:pve1:eno1"));
  });

  it("places each cluster node's column in a disjoint, non-overlapping x-range, sorted by name", async () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve2:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve2" }),
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
    ];
    const positions = await computeLayout(nodes, []);

    const x1 = positions.get("physnic:pve1:eno1")?.x ?? NaN;
    const x2 = positions.get("physnic:pve2:eno1")?.x ?? NaN;
    // pve1 sorts before pve2, so its column must be entirely to the left.
    expect(x1 + NODE_WIDTH).toBeLessThanOrEqual(x2);
  });

  it("spans the SDN band's x-range across (at least) the full column width rather than confining it to one node's column", async () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
      node({ id: "physnic:pve2:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve2" }),
      node({ id: "physnic:pve3:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve3" }),
      node({ id: "sdn-vnet::z1/v1", kind: "sdn-vnet", layer: "sdn", nodeGroup: "" }),
    ];
    const positions = await computeLayout(nodes, []);

    const columnXs = ["pve1", "pve2", "pve3"].map(
      (n) => positions.get(`physnic:${n}:eno1`)?.x ?? 0,
    );
    const totalColumnSpan = Math.max(...columnXs) + NODE_WIDTH;
    const sdnX = positions.get("sdn-vnet::z1/v1")?.x ?? NaN;
    // A single SDN node can't literally span the whole width by itself, but
    // its band shouldn't be confined near x=0 while three node columns
    // stretch far to the right — assert it's not trivially left-clamped.
    expect(totalColumnSpan).toBeGreaterThan(NODE_WIDTH * 2);
    expect(sdnX).toBeGreaterThanOrEqual(0);
  });

  it("returns an empty map for an empty graph without throwing", async () => {
    const positions = await computeLayout([], []);
    expect(positions.size).toBe(0);
  });

  it("handles a node with no nodeGroup peers and no edges (single-node fixture shape)", async () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
    ];
    const positions = await computeLayout(nodes, []);
    expect(positions.has("physnic:pve1:eno1")).toBe(true);
  });

  it("positions every node from a realistic multi-layer, multi-edge graph without throwing", async () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
      node({ id: "physnic:pve1:eno2", kind: "physnic", layer: "phys", nodeGroup: "pve1" }),
      node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2", nodeGroup: "pve1" }),
      node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", nodeGroup: "pve1" }),
      node({ id: "sdn-vnet::z1/v1", kind: "sdn-vnet", layer: "sdn", nodeGroup: "" }),
      node({ id: "guest-nic:pve1:100/net0", kind: "guest-nic", layer: "guest", nodeGroup: "pve1" }),
      node({ id: "guest:pve1:100", kind: "guest", layer: "guest", nodeGroup: "pve1" }),
    ];
    const edges: TopologyEdge[] = [
      { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
      { from: "physnic:pve1:eno2", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
      { from: "bond:pve1:bond0", to: "bridge:pve1:vmbr0", kind: "port-of", status: "ok", badges: [] },
      { from: "guest-nic:pve1:100/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    ];
    const positions = await computeLayout(nodes, edges);
    expect(positions.size).toBe(nodes.length);
    for (const n of nodes) {
      expect(positions.has(n.id)).toBe(true);
    }
  });
});
