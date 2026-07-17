// T-906 acceptance criterion 5: Vitest coverage on the export-serialization
// logic, independent of the browser download mechanism (exportDownload.ts
// is deliberately untested here — see that file's header comment).
import { describe, expect, it } from "vitest";
import type { EntityFlowEdge } from "./EntityEdge";
import type { EntityFlowNode } from "./EntityNode";
import {
  buildCaptionLines,
  renderSvg,
  sceneEntityCount,
  sceneFromFlowElements,
  sceneFromSwitchTopology,
} from "./export";
import type { FlowElements } from "./toFlowElements";
import type { SwitchTopology } from "./switchModel";

function node(id: string, overrides: Partial<EntityFlowNode["data"]> = {}, position = { x: 0, y: 0 }): EntityFlowNode {
  return {
    id,
    type: "entity",
    position,
    data: {
      label: id,
      kind: "bridge",
      status: "ok",
      badges: [],
      dimmed: false,
      highlighted: false,
      isGuestGroup: false,
      ...overrides,
    },
  };
}

function edge(id: string, source: string, target: string, overrides: Partial<EntityFlowEdge["data"]> = {}): EntityFlowEdge {
  return {
    id,
    source,
    target,
    type: "entity",
    data: { status: "ok", badges: [], dimmed: false, highlighted: false, ...overrides },
  };
}

describe("sceneFromFlowElements", () => {
  it("carries every non-dimmed node/edge through with its graph position", () => {
    const elements: FlowElements = {
      nodes: [node("a", {}, { x: 10, y: 20 }), node("b", { kind: "physnic" }, { x: 200, y: 20 })],
      edges: [edge("a=>b", "a", "b")],
    };
    const scene = sceneFromFlowElements(elements);
    expect(scene.nodes).toHaveLength(2);
    expect(scene.edges).toHaveLength(1);
    expect(scene.nodes[0]).toMatchObject({ id: "a", x: 10, y: 20 });
    expect(sceneEntityCount(scene)).toBe(3);
  });

  it("drops VLAN-dimmed nodes entirely — an export documents one filtered view, not a fainter copy of everything", () => {
    const elements: FlowElements = {
      nodes: [node("match"), node("nomatch", { dimmed: true })],
      edges: [],
    };
    const scene = sceneFromFlowElements(elements);
    expect(scene.nodes.map((n) => n.id)).toEqual(["match"]);
  });

  it("drops an edge whose endpoint node was dropped (dimmed or a dimmed edge itself)", () => {
    const elements: FlowElements = {
      nodes: [node("a"), node("b", { dimmed: true })],
      edges: [edge("a=>b", "a", "b"), edge("a=>a-dimmed", "a", "a", { dimmed: true })],
    };
    const scene = sceneFromFlowElements(elements);
    expect(scene.edges).toHaveLength(0);
  });
});

function switchTopologyFixture(): SwitchTopology {
  return {
    nodes: [
      {
        node: "pve1",
        switches: [
          {
            ref: "pve1/vmbr0",
            node: "pve1",
            name: "vmbr0",
            kind: "bridge",
            status: "ok",
            badges: [],
            uplinks: [
              {
                ref: "pve1/bond0",
                label: "bond0",
                kind: "bond",
                status: "ok",
                badges: [],
                members: [
                  { ref: "pve1/eno1", label: "eno1", status: "ok", active: true, badges: [] },
                  { ref: "pve1/eno2", label: "eno2", status: "ok", active: true, badges: [] },
                ],
              },
            ],
            vlans: [{ ref: "pve1/vmbr0.20", label: "vmbr0.20", status: "ok", vid: 20 }],
            accessPorts: [
              { ref: "pve1/app01/net0", label: "app01/net0", status: "ok", badges: [], vid: 100, isGroup: false },
            ],
            vnets: [{ ref: "sdn/vnetz", label: "vnetz", status: "ok", tag: 200 }],
          },
        ],
        freePorts: [{ ref: "pve1/eno3", label: "eno3", kind: "physnic", status: "ok", badges: [], members: [] }],
      },
    ],
  };
}

describe("sceneFromSwitchTopology", () => {
  it("includes the switch plus every layer's leaf entities when all layers are active", () => {
    const topology = switchTopologyFixture();
    const scene = sceneFromSwitchTopology(topology, {
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
    });
    const ids = scene.nodes.map((n) => n.id);
    // switch + 2 uplink NICs + 1 vlan-if + 1 access port + 1 vnet + 1 free port
    expect(ids).toEqual(
      expect.arrayContaining([
        "pve1/vmbr0",
        "pve1/eno1",
        "pve1/eno2",
        "pve1/vmbr0.20",
        "pve1/app01/net0",
        "sdn/vnetz",
        "pve1/eno3",
      ]),
    );
    expect(scene.nodes).toHaveLength(7);
  });

  it("a layer toggled off hides only that layer's slot, not the switch itself", () => {
    const topology = switchTopologyFixture();
    const scene = sceneFromSwitchTopology(topology, {
      activeLayers: new Set(["l2", "sdn", "guest"]), // phys off
    });
    const ids = scene.nodes.map((n) => n.id);
    expect(ids).toContain("pve1/vmbr0"); // the switch itself stays
    expect(ids).not.toContain("pve1/eno1"); // uplink members gone
    expect(ids).not.toContain("pve1/eno3"); // free port gone (phys-gated)
    expect(ids).toContain("pve1/vmbr0.20");
  });

  it("a VLAN filter drops a switch entirely when none of its slots carry that VID", () => {
    const topology = switchTopologyFixture();
    const activeLayers = new Set<"phys" | "l2" | "sdn" | "guest">(["phys", "l2", "sdn", "guest"]);
    const matching = sceneFromSwitchTopology(topology, { activeLayers, vlanFilter: 100 });
    expect(matching.nodes.map((n) => n.id)).toContain("pve1/vmbr0");

    const nonMatching = sceneFromSwitchTopology(topology, { activeLayers, vlanFilter: 999 });
    // The switch (and everything attached to it) drops out, but an
    // unattached free port belongs to no switch/VLAN — it isn't gated by
    // the VLAN filter at all (mirrors SwitchView.tsx: freePorts render
    // whenever the phys layer is on, independent of vlanFilter).
    expect(nonMatching.nodes.map((n) => n.id)).toEqual(["pve1/eno3"]);
    expect(nonMatching.edges).toHaveLength(0);
  });
});

describe("buildCaptionLines", () => {
  it("names hidden layers and the active VLAN filter", () => {
    const lines = buildCaptionLines({
      viewMode: "graph",
      activeLayers: new Set(["phys", "sdn"]),
      layerOrder: ["phys", "l2", "sdn", "guest"],
      vlanFilter: 20,
      generatedAt: new Date("2026-01-01T00:00:00Z"),
    });
    expect(lines[0]).toContain("Graph");
    expect(lines.some((l) => l.includes("L2") && l.includes("Guests"))).toBe(true);
    expect(lines.some((l) => l.includes("VLAN filter: 20"))).toBe(true);
    expect(lines.some((l) => l.includes("2026-01-01"))).toBe(true);
  });

  it("says all layers shown / no VLAN filter when nothing is restricted", () => {
    const lines = buildCaptionLines({
      viewMode: "switch",
      activeLayers: new Set(["phys", "l2", "sdn", "guest"]),
      layerOrder: ["phys", "l2", "sdn", "guest"],
    });
    expect(lines).toContain("All layers shown");
    expect(lines).toContain("No VLAN filter");
  });
});

describe("renderSvg", () => {
  it("emits one data-export-entity per node/edge and matching root count attributes", () => {
    const elements: FlowElements = {
      nodes: [node("a", {}, { x: 0, y: 0 }), node("b", {}, { x: 300, y: 0 })],
      edges: [edge("a=>b", "a", "b")],
    };
    const scene = sceneFromFlowElements(elements);
    const svg = renderSvg(scene);

    expect(svg.startsWith("<svg")).toBe(true);
    expect(svg).toContain('data-export-node-count="2"');
    expect(svg).toContain('data-export-edge-count="1"');
    const nodeMatches = svg.match(/data-export-entity="node"/g) ?? [];
    const edgeMatches = svg.match(/data-export-entity="edge"/g) ?? [];
    expect(nodeMatches).toHaveLength(2);
    expect(edgeMatches).toHaveLength(1);
    expect(svg).toContain('data-entity-ref="a"');
    expect(svg).toContain('data-entity-ref="b"');
  });

  it("includes caption lines as visible text when provided, and omits the caption group otherwise", () => {
    const scene = sceneFromFlowElements({ nodes: [node("a")], edges: [] });
    const withCaption = renderSvg(scene, { captionLines: ["VLAN filter: 20", "Layers hidden: Guests"] });
    expect(withCaption).toContain("VLAN filter: 20");
    expect(withCaption).toContain("Layers hidden: Guests");
    expect(withCaption).toContain('data-export-group="caption"');

    const withoutCaption = renderSvg(scene);
    expect(withoutCaption).not.toContain('data-export-group="caption"');
  });

  it("escapes label text so a malicious/odd entity name can't break the XML", () => {
    const scene = sceneFromFlowElements({
      nodes: [node("weird", { label: `<script>&"'</script>` })],
      edges: [],
    });
    const svg = renderSvg(scene);
    expect(svg).not.toContain("<script>");
    expect(svg).toContain("&lt;script&gt;&amp;&quot;&apos;&lt;/script&gt;");
  });

  it("produces a well-formed, non-empty document even for an empty scene", () => {
    const svg = renderSvg({ nodes: [], edges: [] });
    expect(svg).toContain('data-export-node-count="0"');
    expect(svg).toContain('data-export-edge-count="0"');
    expect(svg.endsWith("</svg>")).toBe(true);
  });
});
