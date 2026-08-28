// SPDX-License-Identifier: Apache-2.0

// Renders the topology canvas against a *real, captured* projection of the
// messy-brownfield pvemock fixture (internal/topology's own T-106 dataset
// for degraded/conflicting cluster state: unknown-status peer links,
// half-configured bridges, an isolated VLAN, etc.) — acceptance criterion 5:
// "messy-brownfield fixture renders without errors (degraded data
// tolerance)". This is exactly the kind of thing Testing Library *can*
// prove headlessly (a render exception surfaces as a thrown error in the
// test), even without a real browser: what it can't prove is that the
// result *looks* right, which needs a human — see this task's completion
// report.
//
// The fixture file was captured by running the real pvemock -> collect ->
// inventory.Graph -> topology.Project pipeline (internal/topology's own
// test harness) against testdata/clusters/messy-brownfield.yaml and
// dumping topology.Project's JSON output — not hand-typed, so it reflects
// whatever real degenerate shapes that fixture produces.
import fixtureJson from "./__fixtures__/messy-brownfield-topology.json";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TopologyResponse } from "../api/types";
import { TopologyCanvas } from "./TopologyCanvas";
import { computeLayout } from "./layout";
import { toFlowElements } from "./toFlowElements";

const fixture = fixtureJson as unknown as TopologyResponse;

describe("messy-brownfield fixture", () => {
  it("computes a layout for every node without throwing", async () => {
    const positions = await computeLayout(fixture.nodes, fixture.edges);
    for (const n of fixture.nodes) {
      expect(positions.has(n.id)).toBe(true);
    }
  });

  it("projects to flow elements without throwing, including for nodes with 'unknown'/'degraded' status", async () => {
    const positions = await computeLayout(fixture.nodes, fixture.edges);
    expect(() =>
      toFlowElements({
        nodes: fixture.nodes,
        edges: fixture.edges,
        expandedGroups: new Set(),
        activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
        layoutPositions: positions,
        manualPositions: {},
      }),
    ).not.toThrow();

    const { nodes } = toFlowElements({
      nodes: fixture.nodes,
      edges: fixture.edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layoutPositions: positions,
      manualPositions: {},
    });
    expect(nodes.some((n) => n.data.status === "unknown")).toBe(true);
  });

  it("hover-highlight and VLAN-filter computations don't throw on every node in the fixture", async () => {
    const positions = await computeLayout(fixture.nodes, fixture.edges);
    for (const hoveredId of fixture.nodes.map((n) => n.id)) {
      expect(() =>
        toFlowElements({
          nodes: fixture.nodes,
          edges: fixture.edges,
          expandedGroups: new Set(),
          activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
          hoveredId,
          vlanFilter: 20,
          layoutPositions: positions,
          manualPositions: {},
        }),
      ).not.toThrow();
    }
  });

  it("renders the full canvas (React Flow + custom node/edge components) without throwing", async () => {
    const positions = await computeLayout(fixture.nodes, fixture.edges);
    const elements = toFlowElements({
      nodes: fixture.nodes,
      edges: fixture.edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layoutPositions: positions,
      manualPositions: {},
    });

    const { container } = render(
      <div style={{ width: 800, height: 600 }}>
        <TopologyCanvas
          elements={elements}
          onNodeClick={() => undefined}
          onNodeHover={() => undefined}
          onNodeDragStop={() => undefined}
          onPaneClick={() => undefined}
        />
      </div>,
    );

    expect(container.querySelector(".react-flow")).not.toBeNull();
    // At least one real entity label from the fixture made it into the
    // rendered DOM (three nodes are separately named "vmbr0", one per
    // cluster node — findAllByText, not findByText, is correct here).
    expect((await screen.findAllByText("vmbr0")).length).toBeGreaterThan(0);
  });
});
