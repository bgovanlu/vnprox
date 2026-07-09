// Cross-validates this frontend's client-side layer/VLAN filtering against
// the *real backend's own* server-side ?vlan=20 filter — both captured by
// running the actual pvemock -> collect -> inventory.Graph ->
// topology.Project pipeline against testdata/clusters/three-node-vlan.yaml
// (internal/topology's own T-106 golden fixture; see this task's completion
// report for exactly how these were captured). Acceptance criterion 1
// ("against pvemock three-node-vlan: all four layers render correctly") and
// criterion 2 ("VLAN filter 20 dims everything else") are both covered here
// against real data, not hand-typed approximations.
import fullFixture from "./__fixtures__/three-node-vlan-topology.json";
import vlan20Fixture from "./__fixtures__/three-node-vlan-topology-vlan20.json";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Layer, TopologyResponse } from "../api/types";
import { TopologyCanvas } from "./TopologyCanvas";
import { computeVlanMatch, filterByLayers } from "./projection";
import { computeLayout } from "./layout";
import { toFlowElements } from "./toFlowElements";

const full = fullFixture as unknown as TopologyResponse;
const serverVlan20 = vlan20Fixture as unknown as TopologyResponse;

describe("three-node-vlan: all four layers", () => {
  const ALL_LAYERS: Layer[] = ["phys", "l2", "sdn", "guest"];

  it("the captured fixture actually contains all four layers with nodes in each (sanity-checking the fixture itself)", () => {
    const layersPresent = new Set(full.nodes.map((n) => n.layer));
    for (const layer of ALL_LAYERS) {
      expect(layersPresent.has(layer)).toBe(true);
    }
  });

  it("each layer toggled off removes exactly that layer's nodes and no others", () => {
    for (const layer of ALL_LAYERS) {
      const active = new Set(ALL_LAYERS.filter((l) => l !== layer));
      const { nodes } = filterByLayers(full.nodes, full.edges, active);
      expect(nodes.some((n) => n.layer === layer)).toBe(false);
      expect(nodes.length).toBe(full.nodes.filter((n) => n.layer !== layer).length);
    }
  });

  it("has a cluster-spanning SDN band (nodeGroup === '') distinct from any single node's column", () => {
    const sdnNodes = full.nodes.filter((n) => n.layer === "sdn");
    expect(sdnNodes.length).toBeGreaterThan(0);
    for (const n of sdnNodes) {
      expect(n.nodeGroup).toBe("");
    }
  });
});

describe("three-node-vlan: VLAN 20 filter matches the server's own ?vlan=20 filter", () => {
  it("computeVlanMatch's node set is exactly the server-filtered node set", () => {
    const clientMatch = computeVlanMatch(full.nodes, full.edges, 20);
    const serverIds = new Set(serverVlan20.nodes.map((n) => n.id));

    expect(clientMatch.nodes).toEqual(serverIds);
  });

  it("dims every node the server's ?vlan=20 filter excluded", () => {
    const serverIds = new Set(serverVlan20.nodes.map((n) => n.id));
    const { nodes } = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      vlanFilter: 20,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    for (const n of nodes) {
      expect(n.data.dimmed).toBe(!serverIds.has(n.id));
    }
    // And it's a genuine partition, not "dim everything"/"dim nothing".
    expect(nodes.some((n) => n.data.dimmed)).toBe(true);
    expect(nodes.some((n) => !n.data.dimmed)).toBe(true);
  });
});

describe("three-node-vlan: renders the full canvas without throwing", () => {
  it("renders all layers, unfiltered", async () => {
    const positions = await computeLayout(full.nodes, full.edges);
    const elements = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layoutPositions: positions,
      manualPositions: {},
    });
    render(
      <div style={{ width: 1000, height: 800 }}>
        <TopologyCanvas
          elements={elements}
          onNodeClick={() => undefined}
          onNodeHover={() => undefined}
          onNodeDragStop={() => undefined}
          onPaneClick={() => undefined}
        />
      </div>,
    );
    expect((await screen.findAllByText("vmbr0")).length).toBe(3); // one per node
  });
});
