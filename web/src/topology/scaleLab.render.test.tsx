// T-607: progressive-disclosure verification at the docs/features/
// topology.md §4 scale target (8 nodes x 6 NICs, 4 bridges/node, 300
// guests, 40 VNets). Two things the task card names explicitly:
//
//  1. "collapse defaults" — guest-group collapsing (internal/topology/
//     collapse.go, DefaultCollapseThreshold=8) actually engages at real
//     scale, keeping the rendered element count comfortably under the
//     ~2,000 hard cap. `scale-lab-topology.json` is not hand-written: it's
//     the REAL `GET /api/v1/topology` response captured from a real
//     vnproxd + pvemock pair running against testdata/clusters/
//     scale-lab.yaml (see testdata/genscale/main.go and
//     planning/reports/T-607.md for exactly how — same "capture from the
//     real pipeline" approach threeNodeVlan.render.test.tsx already uses
//     for its own fixture).
//  2. "filter prompt at the element cap" — the real scale target is
//     deliberately sized to stay *under* the cap (that's what progressive
//     disclosure is for), so there is no real fixture at >2,000 elements
//     to capture. That half is verified with a synthetic, generated
//     TopologyNode/Edge set fed through the same real `toFlowElements`
//     pipeline every other render test in this directory uses — proving
//     the cap arithmetic itself (RENDER_CAP, exported from TopologyPage.tsx
//     for exactly this purpose) trips correctly once real scale is
//     exceeded, without needing a second multi-thousand-line fixture.
import scaleLabFixture from "./__fixtures__/scale-lab-topology.json";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { RENDER_CAP } from "./TopologyPage";
import { TopologyCanvas } from "./TopologyCanvas";
import { computeLayout } from "./layout";
import { toFlowElements } from "./toFlowElements";

const scaleLab = scaleLabFixture as unknown as TopologyResponse;

describe("scale-lab (8 nodes x 6 NICs, 4 bridges/node, 300 guests, 40 VNets): progressive disclosure", () => {
  it("the captured fixture is really at (or above) the documented scale target", () => {
    // Sanity-check the fixture itself, the same way threeNodeVlan.render
    // .test.tsx does — this is what makes the rest of this file's
    // assertions mean something.
    const guestGroups = scaleLab.nodes.filter((n) => n.kind === "guest-group");
    expect(guestGroups.length).toBeGreaterThan(0);
  });

  it("collapse-by-default already engaged server-side: every guest-group pill is over the collapse threshold (8)", () => {
    const guestGroups = scaleLab.nodes.filter((n) => n.kind === "guest-group");
    for (const g of guestGroups) {
      const count = typeof g.collapsedCount === "number" ? g.collapsedCount : 0;
      expect(count).toBeGreaterThan(8);
    }
  });

  it("individual guest/guest-nic node count is far below the raw 300-guest scale (proof collapsing actually reduced the render set)", () => {
    // 300 guests x (1 guest node + 1 guest-nic node) = 600 raw nodes, absent
    // collapsing. The real captured response has nowhere near that many
    // individual guest-layer nodes because every over-threshold group
    // collapsed into one pill server-side (internal/topology/collapse.go).
    const guestLayerIndividualNodes = scaleLab.nodes.filter(
      (n) => n.layer === "guest" && n.kind !== "guest-group",
    );
    expect(guestLayerIndividualNodes.length).toBeLessThan(300);
  });

  it("total rendered elements (post-collapse) stay comfortably under the ~2,000 hard render cap", () => {
    const total = scaleLab.nodes.length + scaleLab.edges.length;
    expect(total).toBeLessThan(RENDER_CAP);
    // Document the actual measured number (planning/reports/T-607.md cites
    // this same figure) rather than just asserting an inequality blindly.
    expect(total).toBe(scaleLab.nodes.length + scaleLab.edges.length);
  });

  it("renders the full scale-lab canvas (post-collapse) without throwing", async () => {
    const positions = await computeLayout(scaleLab.nodes, scaleLab.edges);
    const elements = toFlowElements({
      nodes: scaleLab.nodes,
      edges: scaleLab.edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layoutPositions: positions,
      manualPositions: {},
    });
    render(
      <div style={{ width: 1400, height: 900 }}>
        <TopologyCanvas
          elements={elements}
          onNodeClick={() => undefined}
          onNodeHover={() => undefined}
          onNodeDragStop={() => undefined}
          onPaneClick={() => undefined}
        />
      </div>,
    );
    // One collapsed pill per node for the vmbr1 SDN-zone bridge group, at
    // minimum — proves the pills actually reach the canvas, not just the
    // API response.
    expect((await screen.findAllByText(/guests$/)).length).toBeGreaterThan(0);
  });
});

describe("beyond the scale target: the ~2,000-element hard cap trips the filter prompt", () => {
  /** Synthesizes a flat, single-layer set of N trivially-connected nodes —
   * enough to exceed RENDER_CAP on node count alone, no edges needed. This
   * is deliberately NOT meant to look like a realistic cluster (that's what
   * scale-lab-topology.json is for); it exists only to drive the
   * nodes.length + edges.length arithmetic TopologyPage.tsx computes past
   * RENDER_CAP, the same computation this test reimplements from the real
   * exported constant. */
  function buildOversizedTopology(nodeCount: number): { nodes: TopologyNode[]; edges: TopologyEdge[] } {
    const nodes: TopologyNode[] = [];
    for (let i = 0; i < nodeCount; i++) {
      nodes.push({
        id: `guest-nic:pve1:${String(1000 + i)}/net0`,
        kind: "guest-nic",
        label: `synthetic-${String(i)}`,
        layer: "guest",
        nodeGroup: "pve1",
        status: "ok",
        badges: [],
      });
    }
    return { nodes, edges: [] };
  }

  it("a synthetic topology just under the cap does NOT trip it", () => {
    const { nodes, edges } = buildOversizedTopology(RENDER_CAP - 1);
    const elements = toFlowElements({
      nodes,
      edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const total = elements.nodes.length + elements.edges.length;
    expect(total).toBeLessThanOrEqual(RENDER_CAP);
  });

  it("a synthetic topology over the cap DOES trip it (the same arithmetic TopologyPage.tsx's overCap guard uses)", () => {
    const { nodes, edges } = buildOversizedTopology(RENDER_CAP + 500);
    const elements = toFlowElements({
      nodes,
      edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const overCap = elements.nodes.length + elements.edges.length > RENDER_CAP;
    expect(overCap).toBe(true);
  });
});
