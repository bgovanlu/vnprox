// SPDX-License-Identifier: Apache-2.0

// T-901 renderer-v2 coverage. jsdom has no real CanvasRenderingContext2D, so
// these tests do NOT assert pixels — they assert the things v2 must get right
// independent of drawing: the accessibility bridge (one labeled, focusable
// DOM proxy per visible entity, kept in sync with selection), golden parity
// with the v1 data pipeline (same VLAN-dim set the server's own ?vlan=20
// filter and the v1 render test assert), and that a drag resolves the same
// (dragged, target) pair — and therefore the same drafted op — v1 would.
// The pixel path itself is exercised by the e2e perf/scale run in real
// headless Chromium.
import fullFixture from "./__fixtures__/three-node-vlan-topology.json";
import vlan20Fixture from "./__fixtures__/three-node-vlan-topology-vlan20.json";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { TopologyNode, TopologyResponse } from "../api/types";
import { computeDragOp } from "../changesets/dragDropOps";
import { TopologyCanvasV2 } from "./TopologyCanvasV2";
import { TopologyA11yLayer } from "./TopologyA11yLayer";
import { buildA11yProxies, entityAriaLabel } from "./a11yBridge";
import { toFlowElements } from "./toFlowElements";
import { DEFAULT_NODE_SIZE } from "./canvasScene";
import type { FlowElements } from "./toFlowElements";

const full = fullFixture as unknown as TopologyResponse;
const serverVlan20 = vlan20Fixture as unknown as TopologyResponse;

const ALL_LAYERS = new Set(["phys", "l2", "sdn", "guest"] as const);

function noop() {
  /* test stub */
}

function baseProps() {
  return {
    onNodeClick: noop,
    onNodeHover: noop,
    onNodeDrop: noop,
    onPaneClick: noop,
  };
}

describe("v2 a11y bridge: one labeled proxy per visible entity (AC5)", () => {
  it("renders an accessible button per node with the aria-label vocabulary", () => {
    const elements = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    render(<TopologyCanvasV2 elements={elements} theme="light" {...baseProps()} />);

    const region = screen.getByRole("application", { name: /Topology map/ });
    const buttons = within(region).getAllByRole("button");
    // Exactly one proxy per visible node.
    expect(buttons).toHaveLength(elements.nodes.length);
    // Each proxy's accessible name matches the shared label builder. (Some
    // labels legitimately repeat — a same-named bridge like vmbr0 exists on
    // each of the three nodes — so match >=1 rather than exactly one.)
    for (const n of elements.nodes) {
      expect(screen.getAllByLabelText(entityAriaLabel(n.data)).length).toBeGreaterThanOrEqual(1);
    }
    // At least one vmbr0 bridge is present and labeled as such.
    expect(screen.getAllByLabelText(/bridge vmbr0, status/).length).toBeGreaterThanOrEqual(1);
  });

  it("keeps the proxy set in sync with selection (aria-pressed) and re-render", () => {
    const elements = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      selectedId: full.nodes[0]?.id,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const first = elements.nodes[0];
    expect(first).toBeDefined();
    if (!first) return;
    render(<TopologyCanvasV2 elements={elements} selectedId={first.id} theme="light" {...baseProps()} />);
    const pressed = screen.getByLabelText(entityAriaLabel(first.data));
    expect(pressed).toHaveAttribute("aria-pressed", "true");
  });
});

describe("v2 a11y layer stays in sync across pan/zoom (AC5)", () => {
  it("applies the viewport as a single transform, updated on viewport change", () => {
    const nodes = buildA11yProxies(
      toFlowElements({
        nodes: full.nodes.slice(0, 3),
        edges: [],
        expandedGroups: new Set(),
        activeLayers: ALL_LAYERS,
        layoutPositions: new Map(),
        manualPositions: {},
      }).nodes,
    );
    const { container, rerender } = render(
      <TopologyA11yLayer
        proxies={nodes}
        viewport={{ x: 0, y: 0, zoom: 1 }}
        activeId={undefined}
        onActiveChange={noop}
        onActivate={noop}
      />,
    );
    const transformed = container.querySelector<HTMLElement>("[style*='translate']");
    expect(transformed?.style.transform).toContain("translate(0px, 0px)");
    rerender(
      <TopologyA11yLayer
        proxies={nodes}
        viewport={{ x: 120, y: -40, zoom: 1.5 }}
        activeId={undefined}
        onActiveChange={noop}
        onActivate={noop}
      />,
    );
    const after = container.querySelector<HTMLElement>("[style*='translate']");
    expect(after?.style.transform).toContain("translate(120px, -40px)");
    expect(after?.style.transform).toContain("scale(1.5)");
  });

  it("Enter activates the roving-focused entity the same way a click would", () => {
    const proxies = buildA11yProxies(
      toFlowElements({
        nodes: full.nodes.slice(0, 2),
        edges: [],
        expandedGroups: new Set(),
        activeLayers: ALL_LAYERS,
        layoutPositions: new Map(),
        manualPositions: {},
      }).nodes,
    );
    const onActivate = vi.fn();
    const first = proxies[0];
    expect(first).toBeDefined();
    if (!first) return;
    render(
      <TopologyA11yLayer
        proxies={proxies}
        viewport={{ x: 0, y: 0, zoom: 1 }}
        activeId={first.id}
        onActiveChange={noop}
        onActivate={onActivate}
      />,
    );
    const region = screen.getByRole("application");
    fireEvent.keyDown(region, { key: "Enter" });
    expect(onActivate).toHaveBeenCalledWith(first.id);
  });
});

describe("v2 golden parity: VLAN-20 dim set matches the server filter (AC2)", () => {
  it("dims exactly the entities the server's ?vlan=20 filter excludes", () => {
    const elements = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      vlanFilter: 20,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const serverIds = new Set(serverVlan20.nodes.map((n) => n.id));
    // v2 consumes elements.nodes[].data.dimmed unchanged from toFlowElements,
    // the same field the v1 render test asserts — so the dim partition is
    // identical by construction. Assert it here against the server's own set.
    for (const n of elements.nodes) {
      expect(n.data.dimmed).toBe(!serverIds.has(n.id));
    }
    expect(elements.nodes.some((n) => n.data.dimmed)).toBe(true);
    expect(elements.nodes.some((n) => !n.data.dimmed)).toBe(true);
  });
});

describe("v2 drag-drop resolves the same (dragged, target) pair & op as v1 (AC3)", () => {
  // A synthetic 2-entity topology with fixed manual positions so the drag
  // geometry is deterministic (no elk, no jsdom layout): a free physnic and a
  // bond on the same node, placed apart.
  const physnic: TopologyNode = {
    id: "physnic:pve1:eth9",
    kind: "physnic",
    label: "eth9",
    layer: "phys",
    nodeGroup: "pve1",
    status: "ok",
    badges: [],
  };
  const bond: TopologyNode = {
    id: "bond:pve1:bond0",
    kind: "bond",
    label: "bond0",
    layer: "l2",
    nodeGroup: "pve1",
    status: "ok",
    badges: [],
  };
  const topology: TopologyResponse = {
    nodes: [physnic, bond],
    edges: [],
    layers: ["phys", "l2", "sdn", "guest"],
    generatedAt: 0,
  };

  function elementsFor(): FlowElements {
    return toFlowElements({
      nodes: topology.nodes,
      edges: topology.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      // Fixed graph positions; box is 172x52, viewport is jsdom's initial
      // {48,48,zoom:1} (no ResizeObserver => no fit), so screen = graph + 48.
      manualPositions: {
        [physnic.id]: { x: 0, y: 0 },
        [bond.id]: { x: 300, y: 0 },
      },
    });
  }

  it("a NIC dragged onto a bond fires onNodeDrop(nic, bond) => bond.update op", () => {
    const onNodeDrop = vi.fn();
    const { container } = render(
      <TopologyCanvasV2 elements={elementsFor()} theme="light" {...baseProps()} onNodeDrop={onNodeDrop} />,
    );
    const target = container.querySelector('[data-testid="topology-canvas-v2"]');
    expect(target).not.toBeNull();
    if (!target) return;

    const half = { x: DEFAULT_NODE_SIZE.width / 2, y: DEFAULT_NODE_SIZE.height / 2 };
    const nicCenter = { x: 0 + half.x + 48, y: 0 + half.y + 48 };
    const bondCenter = { x: 300 + half.x + 48, y: 0 + half.y + 48 };

    fireEvent.pointerDown(target, { button: 0, pointerId: 1, clientX: nicCenter.x, clientY: nicCenter.y });
    fireEvent.pointerMove(target, { pointerId: 1, clientX: bondCenter.x, clientY: bondCenter.y });
    fireEvent.pointerUp(target, { pointerId: 1, clientX: bondCenter.x, clientY: bondCenter.y });

    expect(onNodeDrop).toHaveBeenCalledTimes(1);
    const [draggedId, targetId] = onNodeDrop.mock.calls[0] as [string, string | undefined, unknown];
    expect(draggedId).toBe(physnic.id);
    expect(targetId).toBe(bond.id);

    // And the op that pair drafts (the shared computeDragOp both renderers
    // feed) is the expected bond.update — proving "identical op under v2".
    const op = computeDragOp(physnic, bond, topology);
    expect(op).toEqual({ op: "bond.update", target: bond.id, params: { slaves: ["eth9"] } });
  });

  it("a click (no drag) selects rather than dropping", () => {
    const onNodeClick = vi.fn();
    const onNodeDrop = vi.fn();
    const { container } = render(
      <TopologyCanvasV2
        elements={elementsFor()}
        theme="light"
        {...baseProps()}
        onNodeClick={onNodeClick}
        onNodeDrop={onNodeDrop}
      />,
    );
    const target = container.querySelector('[data-testid="topology-canvas-v2"]');
    if (!target) throw new Error("missing canvas");
    const nicCenter = { x: DEFAULT_NODE_SIZE.width / 2 + 48, y: DEFAULT_NODE_SIZE.height / 2 + 48 };
    fireEvent.pointerDown(target, { button: 0, pointerId: 1, clientX: nicCenter.x, clientY: nicCenter.y });
    fireEvent.pointerUp(target, { pointerId: 1, clientX: nicCenter.x, clientY: nicCenter.y });
    expect(onNodeClick).toHaveBeenCalledWith(physnic.id);
    expect(onNodeDrop).not.toHaveBeenCalled();
  });
});

describe("v2 saved-layout round-trip: positions render identically (AC4)", () => {
  it("a manual position saved under v1 places the v2 entity at the same graph coords", () => {
    const saved = { [full.nodes[0]?.id ?? ""]: { x: 777, y: 333 } };
    const elements = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: saved,
    });
    // v2 draws and builds a11y proxies from elements.nodes[].position — the
    // exact same field the v1 renderer positions React Flow nodes at. So a
    // layout saved under either renderer lands at identical graph coords
    // under the other.
    const proxies = buildA11yProxies(elements.nodes);
    const moved = proxies.find((p) => p.id === full.nodes[0]?.id);
    expect(moved?.graph).toMatchObject({ x: 777, y: 333 });
  });
});

describe("T-905: drift pulse respects prefers-reduced-motion", () => {
  function stubPrefersReducedMotion(matches: boolean) {
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches,
        media: "(prefers-reduced-motion: reduce)",
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
  }

  function elementsWithOneDriftNode(): FlowElements {
    const first = full.nodes[0];
    if (!first) throw new Error("fixture has no nodes");
    const driftNode: TopologyNode = { ...first, badges: [...first.badges, "drift"] };
    return toFlowElements({
      nodes: [driftNode, ...full.nodes.slice(1)],
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
  }

  it("never starts the pulse interval when prefers-reduced-motion: reduce is set, even with a drift entity present", () => {
    stubPrefersReducedMotion(true);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    render(<TopologyCanvasV2 elements={elementsWithOneDriftNode()} theme="light" {...baseProps()} />);
    expect(setIntervalSpy).not.toHaveBeenCalled();
    setIntervalSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("starts the pulse interval when motion is allowed and a drift entity is present", () => {
    stubPrefersReducedMotion(false);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    render(<TopologyCanvasV2 elements={elementsWithOneDriftNode()} theme="light" {...baseProps()} />);
    expect(setIntervalSpy).toHaveBeenCalled();
    setIntervalSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("never starts the pulse interval when no visible entity carries the drift badge, even with motion allowed", () => {
    stubPrefersReducedMotion(false);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const elements = toFlowElements({
      nodes: full.nodes,
      edges: full.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    render(<TopologyCanvasV2 elements={elements} theme="light" {...baseProps()} />);
    expect(setIntervalSpy).not.toHaveBeenCalled();
    setIntervalSpy.mockRestore();
    vi.unstubAllGlobals();
  });
});
