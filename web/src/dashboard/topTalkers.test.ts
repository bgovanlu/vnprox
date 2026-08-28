// SPDX-License-Identifier: Apache-2.0

// Pure-logic coverage for the top-talkers tile's ranking (T-904),
// independent of React/TanStack Query — see topTalkers.ts's own doc
// comment for why this stays framework-free.
import { describe, expect, it } from "vitest";
import type { LiveMetric, TopologyEdge, TopologyNode } from "../api/types";
import { bridgeGuestGroups, computeTopTalkers, refsToSample } from "./topTalkers";

const RATES = (rxBps: number, txBps: number) => ({
  rxBps,
  txBps,
  rxPps: 0,
  txPps: 0,
  rxErrsPerSec: 0,
  txErrsPerSec: 0,
  rxDropPerSec: 0,
  txDropPerSec: 0,
});

const nodes: TopologyNode[] = [
  { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
  { id: "bridge:pve2:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve2", status: "ok", badges: [] },
  { id: "guest-nic:pve1:100/net0", kind: "guest-nic", label: "vm100/net0", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
  { id: "guest-nic:pve1:101/net0", kind: "guest-nic", label: "vm101/net0", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
  { id: "guest-nic:pve2:200/net0", kind: "guest-nic", label: "vm200/net0", layer: "guest", nodeGroup: "pve2", status: "ok", badges: [] },
  // A collapsed guest-group pill sharing an "attached-to" edge with a real
  // guest-nic's bridge — must never be treated as a sampleable ref.
  { id: "guest-group:pve1:bridge:pve1:vmbr0", kind: "guest-group", label: "12 more", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [], collapsedCount: 12 },
];

const edges: TopologyEdge[] = [
  { from: "guest-nic:pve1:100/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  { from: "guest-nic:pve1:101/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  { from: "guest-nic:pve2:200/net0", to: "bridge:pve2:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  { from: "guest-group:pve1:bridge:pve1:vmbr0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  // Non-"attached-to" edges (e.g. port-of) must not contribute a guest.
  { from: "physnic:pve1:eno1", to: "bridge:pve1:vmbr0", kind: "port-of", status: "ok", badges: [] },
];

function labelOf(ref: string): string {
  return nodes.find((n) => n.id === ref)?.label ?? ref;
}

describe("bridgeGuestGroups", () => {
  it("groups guest-nic refs by their directly-attached bridge, excluding guest-group pills and non-attached-to edges", () => {
    const groups = bridgeGuestGroups(nodes, edges);
    const byBridge = new Map(groups.map((g) => [g.bridgeRef, g.guestRefs]));
    expect(byBridge.get("bridge:pve1:vmbr0")).toEqual(["guest-nic:pve1:100/net0", "guest-nic:pve1:101/net0"]);
    expect(byBridge.get("bridge:pve2:vmbr0")).toEqual(["guest-nic:pve2:200/net0"]);
  });
});

describe("refsToSample", () => {
  it("flattens every group's guest refs into one deduplicated ref list", () => {
    const groups = bridgeGuestGroups(nodes, edges);
    expect(refsToSample(groups)).toEqual(
      expect.arrayContaining(["guest-nic:pve1:100/net0", "guest-nic:pve1:101/net0", "guest-nic:pve2:200/net0"]),
    );
  });
});

describe("computeTopTalkers", () => {
  it("picks the busiest bridge and ranks its guests by combined rx+tx, descending", () => {
    const groups = bridgeGuestGroups(nodes, edges);
    const live = new Map<string, LiveMetric>([
      ["guest-nic:pve1:100/net0", { ref: "guest-nic:pve1:100/net0", at: 1, rates: RATES(5_000_000, 1_000_000) }],
      ["guest-nic:pve1:101/net0", { ref: "guest-nic:pve1:101/net0", at: 1, rates: RATES(500_000, 100_000) }],
      ["guest-nic:pve2:200/net0", { ref: "guest-nic:pve2:200/net0", at: 1, rates: RATES(1_000, 500) }],
    ]);

    const result = computeTopTalkers(groups, live, labelOf);

    expect(result?.bridgeRef).toBe("bridge:pve1:vmbr0");
    expect(result?.talkers.map((t) => t.ref)).toEqual(["guest-nic:pve1:100/net0", "guest-nic:pve1:101/net0"]);
  });

  it("returns undefined when there is no measurable traffic anywhere (the tile's empty-state trigger)", () => {
    const groups = bridgeGuestGroups(nodes, edges);
    const live = new Map<string, LiveMetric>();
    expect(computeTopTalkers(groups, live, labelOf)).toBeUndefined();
  });

  it("caps the ranked list at `limit`", () => {
    const manyNodes: TopologyNode[] = [
      { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
      ...Array.from({ length: 8 }, (_, i) => ({
        id: `guest-nic:pve1:${String(100 + i)}/net0`,
        kind: "guest-nic",
        label: `vm${String(100 + i)}/net0`,
        layer: "guest" as const,
        nodeGroup: "pve1",
        status: "ok" as const,
        badges: [],
      })),
    ];
    const manyEdges: TopologyEdge[] = manyNodes
      .filter((n) => n.kind === "guest-nic")
      .map((n) => ({ from: n.id, to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok" as const, badges: [] }));
    const groups2 = bridgeGuestGroups(manyNodes, manyEdges);
    const live2 = new Map<string, LiveMetric>(
      manyNodes
        .filter((n) => n.kind === "guest-nic")
        .map((n, i) => [n.id, { ref: n.id, at: 1, rates: RATES(1000 * (i + 1), 0) }]),
    );
    const result2 = computeTopTalkers(groups2, live2, labelOf, 5);
    expect(result2?.talkers).toHaveLength(5);
  });
});
