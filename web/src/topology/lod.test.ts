// SPDX-License-Identifier: Apache-2.0

// T-902 AC2/AC3: deterministic coverage for the LOD transform (lod.ts)
// against the real scale-lab fixture (the same fixture scaleLab.render.
// test.tsx exercises for progressive disclosure) — no browser/canvas
// needed, since applyLod is a pure FlowElements -> FlowElements function.
import scaleLabFixture from "./__fixtures__/scale-lab-topology.json";
import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { EDGE_BUNDLE_THRESHOLD, LOD_BANDS, applyLod, bundleId, capsuleId, parseLodId, zoomBandFor } from "./lod";
import { toFlowElements } from "./toFlowElements";

const scaleLab = scaleLabFixture as unknown as TopologyResponse;
const ALL_LAYERS = new Set(["phys", "l2", "sdn", "guest"] as const);

function elementsFor(nodes: TopologyNode[], edges: TopologyEdge[]) {
  return toFlowElements({
    nodes,
    edges,
    expandedGroups: new Set(),
    activeLayers: ALL_LAYERS,
    layoutPositions: new Map(),
    manualPositions: {},
  });
}

describe("zoomBandFor", () => {
  it("resolves the three named bands in ascending zoom order", () => {
    expect(LOD_BANDS.map((b) => b.id)).toEqual(["capsule", "simplified", "full"]);
    expect(zoomBandFor(0)).toBe("capsule");
    expect(zoomBandFor(0.05)).toBe("capsule");
    expect(zoomBandFor(0.19999)).toBe("capsule");
    expect(zoomBandFor(0.2)).toBe("simplified");
    expect(zoomBandFor(0.49999)).toBe("simplified");
    expect(zoomBandFor(0.5)).toBe("full");
    expect(zoomBandFor(2)).toBe("full");
  });
});

describe("applyLod: 'full' band is a pass-through", () => {
  it("returns the exact same entity set the scale-lab fixture already produces", () => {
    const elements = elementsFor(scaleLab.nodes, scaleLab.edges);
    const result = applyLod(elements, "full");
    expect(result.nodes).toHaveLength(elements.nodes.length);
    expect(result.edges).toHaveLength(elements.edges.length);
    expect(result.nodes.map((n) => n.id).sort()).toEqual(elements.nodes.map((n) => n.id).sort());
  });
});

describe("applyLod: 'capsule' band collapses each node's physical layer (NICs + bonds)", () => {
  it("replaces every physnic/bond node with one per-node-group capsule, resolving the T-607 gap", () => {
    const elements = elementsFor(scaleLab.nodes, scaleLab.edges);
    const result = applyLod(elements, "capsule");

    // No individual physnic/bond nodes survive the capsule band.
    expect(result.nodes.some((n) => n.data.kind === "physnic")).toBe(false);
    expect(result.nodes.some((n) => n.data.kind === "bond")).toBe(false);

    // One capsule per node-group that had phys-layer entities (8 nodes in
    // scale-lab: pve1..pve8, each with 6 NICs + 2 bonds per docs/performance
    // .md §1 — except pve1, which also picks up this dev host's own real
    // interfaces per topology.spec.ts's documented environment caveat, so
    // its count is computed from the source fixture rather than hardcoded).
    const capsules = result.nodes.filter((n) => n.data.kind === "phys-capsule");
    expect(capsules).toHaveLength(8);
    const expectedByGroup = new Map<string, number>();
    for (const n of elements.nodes) {
      if (n.data.kind !== "physnic" && n.data.kind !== "bond") continue;
      const group = n.id.split(":")[1] ?? "";
      expectedByGroup.set(group, (expectedByGroup.get(group) ?? 0) + 1);
    }
    for (const c of capsules) {
      const group = c.id.slice(capsuleId("").length);
      expect(c.data.collapsedCount).toBe(expectedByGroup.get(group));
      expect(c.data.badges).toContain(`count=${String(expectedByGroup.get(group))}`);
    }

    // Bridges (L2 but not part of the NIC/bond capsule scope) are untouched.
    expect(result.nodes.filter((n) => n.data.kind === "bridge")).toHaveLength(
      elements.nodes.filter((n) => n.data.kind === "bridge").length,
    );

    // Every edge that used to touch a collapsed bond now touches its
    // capsule instead — nothing dangles (no edge references a removed id).
    const survivingIds = new Set(result.nodes.map((n) => n.id));
    for (const e of result.edges) {
      expect(survivingIds.has(e.source)).toBe(true);
      expect(survivingIds.has(e.target)).toBe(true);
    }
    // At least one capsule->bridge edge exists (bond--port-of-->bridge
    // redirected), so the capsule isn't a disconnected island.
    const capsuleIds = new Set(capsules.map((c) => c.id));
    expect(result.edges.some((e) => capsuleIds.has(e.source))).toBe(true);
  });

  it("a manually-expanded capsule (click override) is not collapsed", () => {
    const elements = elementsFor(scaleLab.nodes, scaleLab.edges);
    const result = applyLod(elements, "capsule", { expandedCapsules: new Set(["pve1"]) });
    expect(result.nodes.some((n) => n.id === capsuleId("pve1"))).toBe(false);
    expect(result.nodes.some((n) => n.data.kind === "physnic" && n.id.includes(":pve1:"))).toBe(true);
    // Every other node-group still collapses.
    expect(result.nodes.some((n) => n.id === capsuleId("pve2"))).toBe(true);
  });
});

describe("applyLod: 'simplified' band leaves the physical layer uncollapsed", () => {
  it("does not introduce any phys-capsule node", () => {
    const elements = elementsFor(scaleLab.nodes, scaleLab.edges);
    const result = applyLod(elements, "simplified");
    expect(result.nodes.some((n) => n.data.kind === "phys-capsule")).toBe(false);
    expect(result.nodes.filter((n) => n.data.kind === "physnic")).toHaveLength(
      elements.nodes.filter((n) => n.data.kind === "physnic").length,
    );
  });
});

describe("applyLod: edge bundling (AC3)", () => {
  // scale-lab's base fixture already has every guest group server-collapsed
  // (docs/performance.md §4: 24 pills, all over threshold) — there are no
  // individual guest-nic nodes/edges left to bundle in the raw fixture. This
  // mirrors expand.ts's real scenario instead: a user expanded one
  // guest-group pill (revealing its individual guest-nic members, exactly
  // as expandGuestGroup synthesizes), and LOD re-bundles the now-dense set
  // once zoomed back out — the "unbundles on zoom-in" flow reversed.
  const targetBridge = "bridge:pve1:vmbr0";
  const guestCount = EDGE_BUNDLE_THRESHOLD + 4; // comfortably over the mirrored threshold
  const expandedNodes: TopologyNode[] = [];
  const expandedEdges: TopologyEdge[] = [];
  for (let i = 0; i < guestCount; i++) {
    const id = `guest-nic:pve1:${String(9000 + i)}/net0`;
    expandedNodes.push({
      id,
      kind: "guest-nic",
      label: `synthetic-${String(i)}`,
      layer: "guest",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    });
    expandedEdges.push({ from: id, to: targetBridge, kind: "attached-to", status: "ok", badges: [] });
  }

  function buildElements() {
    return elementsFor([...scaleLab.nodes, ...expandedNodes], [...scaleLab.edges, ...expandedEdges]);
  }

  it("does not bundle at the 'full' band", () => {
    const result = applyLod(buildElements(), "full");
    expect(result.nodes.filter((n) => n.data.kind === "guest-nic")).toHaveLength(guestCount);
  });

  it("bundles the dense guest-NIC group into one node + one edge with a correct count badge at 'simplified'", () => {
    const result = applyLod(buildElements(), "simplified");

    expect(result.nodes.some((n) => n.data.kind === "guest-nic" && n.id.startsWith("guest-nic:pve1:9"))).toBe(false);

    const bundles = result.nodes.filter((n) => n.data.kind === "guest-nic-bundle");
    expect(bundles).toHaveLength(1);
    const bundle = bundles[0];
    expect(bundle?.data.collapsedCount).toBe(guestCount);
    expect(bundle?.data.badges).toContain(`count=${String(guestCount)}`);

    const key = `${targetBridge}|pve1`;
    expect(bundle?.id).toBe(bundleId(key));

    const bundleEdges = result.edges.filter((e) => e.source === bundle?.id);
    expect(bundleEdges).toHaveLength(1);
    expect(bundleEdges[0]?.target).toBe(targetBridge);
    expect(bundleEdges[0]?.data?.badges).toContain(`count=${String(guestCount)}`);

    // The bridge itself, and every other real entity, survive untouched.
    expect(result.nodes.some((n) => n.id === targetBridge)).toBe(true);
  });

  it("unbundling restores the per-guest edges (click override, or crossing back into 'full')", () => {
    const elements = buildElements();
    const key = `${targetBridge}|pve1`;

    const collapsed = applyLod(elements, "simplified");
    expect(collapsed.nodes.filter((n) => n.data.kind === "guest-nic-bundle")).toHaveLength(1);

    // Click-to-unbundle: same band, but the group is in the override set.
    const clicked = applyLod(elements, "simplified", { unbundledGroups: new Set([key]) });
    expect(clicked.nodes.some((n) => n.data.kind === "guest-nic-bundle")).toBe(false);
    expect(clicked.nodes.filter((n) => n.data.kind === "guest-nic")).toHaveLength(guestCount);

    // Zoom-in-to-unbundle: crossing back into "full" always restores detail,
    // even without any manual override.
    const zoomedIn = applyLod(elements, "full");
    expect(zoomedIn.nodes.some((n) => n.data.kind === "guest-nic-bundle")).toBe(false);
    expect(zoomedIn.nodes.filter((n) => n.data.kind === "guest-nic")).toHaveLength(guestCount);
  });

  it("a group at or under the threshold never bundles", () => {
    const smallNodes: TopologyNode[] = [];
    const smallEdges: TopologyEdge[] = [];
    for (let i = 0; i < EDGE_BUNDLE_THRESHOLD; i++) {
      const id = `guest-nic:pve2:${String(9100 + i)}/net0`;
      smallNodes.push({
        id,
        kind: "guest-nic",
        label: `small-${String(i)}`,
        layer: "guest",
        nodeGroup: "pve2",
        status: "ok",
        badges: [],
      });
      smallEdges.push({ from: id, to: "bridge:pve2:vmbr0", kind: "attached-to", status: "ok", badges: [] });
    }
    const elements = elementsFor([...scaleLab.nodes, ...smallNodes], [...scaleLab.edges, ...smallEdges]);
    const result = applyLod(elements, "capsule");
    expect(result.nodes.some((n) => n.data.kind === "guest-nic-bundle")).toBe(false);
    expect(result.nodes.filter((n) => n.data.kind === "guest-nic")).toHaveLength(EDGE_BUNDLE_THRESHOLD);
  });
});

describe("parseLodId", () => {
  it("recognizes capsule and bundle ids and round-trips their key", () => {
    expect(parseLodId(capsuleId("pve1"))).toEqual({ kind: "capsule", key: "pve1" });
    expect(parseLodId(bundleId("bridge:pve1:vmbr0|pve1"))).toEqual({ kind: "bundle", key: "bridge:pve1:vmbr0|pve1" });
    expect(parseLodId("bridge:pve1:vmbr0")).toBeUndefined();
    expect(parseLodId("guest-group:pve1:bridge:pve1:vmbr0")).toBeUndefined();
  });
});
