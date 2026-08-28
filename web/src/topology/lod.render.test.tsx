// SPDX-License-Identifier: Apache-2.0

// T-902 AC2/AC3/AC5: integration coverage proving the LOD transform is
// actually wired into TopologyCanvasV2 (not just correct in isolation, per
// lod.test.ts) — zooming the real component out engages capsule/bundle
// collapsing, the a11y bridge reflects the collapsed entity set (same seam
// T-901's own render tests assert against), and a click on the resulting
// synthetic proxy restores full detail. jsdom lacks a real
// CanvasRenderingContext2D and ResizeObserver (see TopologyCanvasV2.render.
// test.tsx's own doc comment), so — exactly like that file — this asserts
// through the a11y proxy layer, not pixels; the real draw path is exercised
// by web/e2e/scale.spec.ts / web/e2e/lod.spec.ts in headless Chromium.
import scaleLabFixture from "./__fixtures__/scale-lab-topology.json";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { EDGE_BUNDLE_THRESHOLD, bundleId } from "./lod";
import { TopologyCanvasV2 } from "./TopologyCanvasV2";
import { toFlowElements } from "./toFlowElements";

const scaleLab = scaleLabFixture as unknown as TopologyResponse;
const ALL_LAYERS = new Set(["phys", "l2", "sdn", "guest"] as const);

function noop() {
  /* test stub */
}

function baseProps() {
  return { onNodeClick: noop, onNodeHover: noop, onNodeDrop: noop, onPaneClick: noop };
}

/** Dispatches `n` zoom-out wheel ticks at the canvas center — the same
 * gesture web/e2e/scale.spec.ts's real-browser sampler uses — driving
 * TopologyCanvasV2's internal viewport.zoom down by a factor of 1/1.1 each
 * tick (canvasScene.ts's zoomAt), independent of ResizeObserver/layout. */
function zoomOut(target: Element, n: number): void {
  for (let i = 0; i < n; i++) {
    fireEvent.wheel(target, { deltaY: 120, clientX: 400, clientY: 300 });
  }
}

describe("TopologyCanvasV2 + LOD: zooming out engages the physical-layer capsule (AC2)", () => {
  it("collapses every physnic/bond proxy into per-node capsule proxies once zoomed into the capsule band", () => {
    const elements = toFlowElements({
      nodes: scaleLab.nodes,
      edges: scaleLab.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const { container } = render(<TopologyCanvasV2 elements={elements} theme="light" {...baseProps()} />);
    const target = container.querySelector('[data-testid="topology-canvas-v2"]');
    expect(target).not.toBeNull();
    if (!target) return;

    const region = screen.getByRole("application", { name: /Topology map/ });
    // At the default zoom (1, "full" band — no ResizeObserver in jsdom means
    // no fit-to-view runs, so it stays at the initial {zoom:1}), every phys
    // entity has its own proxy — no capsules yet.
    expect(within(region).queryAllByLabelText(/phys-capsule/)).toHaveLength(0);
    const physnicCount = elements.nodes.filter((n) => n.data.kind === "physnic").length;
    expect(within(region).getAllByLabelText(/^physnic /)).toHaveLength(physnicCount);

    // Zoom out past the capsule threshold (~17 ticks from zoom=1 to <0.2,
    // per (1/1.1)^n; 20 gives comfortable margin above MIN_ZOOM's floor).
    zoomOut(target, 20);

    expect(within(region).queryAllByLabelText(/^physnic /)).toHaveLength(0);
    expect(within(region).queryAllByLabelText(/^bond /)).toHaveLength(0);
    const capsules = within(region).getAllByLabelText(/phys-capsule/);
    expect(capsules).toHaveLength(8); // one per scale-lab cluster node
  });

  it("a click on a capsule proxy force-expands it back to full detail (AC3's 'or click' clause)", () => {
    const elements = toFlowElements({
      nodes: scaleLab.nodes,
      edges: scaleLab.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const { container } = render(<TopologyCanvasV2 elements={elements} theme="light" {...baseProps()} />);
    const target = container.querySelector('[data-testid="topology-canvas-v2"]');
    if (!target) throw new Error("missing canvas");
    zoomOut(target, 20);

    const region = screen.getByRole("application", { name: /Topology map/ });
    const capsules = within(region).getAllByLabelText(/phys-capsule/);
    expect(capsules).toHaveLength(8);
    const firstCapsule = capsules[0];
    expect(firstCapsule).toBeDefined();
    if (!firstCapsule) return;

    fireEvent.click(firstCapsule);

    // One fewer capsule now (the clicked one expanded); its member
    // physnic/bond proxies are back.
    expect(within(region).getAllByLabelText(/phys-capsule/)).toHaveLength(7);
    expect(within(region).queryAllByLabelText(/^physnic /).length).toBeGreaterThan(0);
  });
});

describe("TopologyCanvasV2 + LOD: edge bundling collapses a dense guest-NIC group (AC3)", () => {
  const targetBridge = "bridge:pve1:vmbr0";
  const guestCount = EDGE_BUNDLE_THRESHOLD + 5;
  function buildElements() {
    const extraNodes: TopologyNode[] = [];
    const extraEdges: TopologyEdge[] = [];
    for (let i = 0; i < guestCount; i++) {
      const id = `guest-nic:pve1:${String(9000 + i)}/net0`;
      extraNodes.push({
        id,
        kind: "guest-nic",
        label: `expanded-${String(i)}`,
        layer: "guest",
        nodeGroup: "pve1",
        status: "ok",
        badges: [],
      });
      extraEdges.push({ from: id, to: targetBridge, kind: "attached-to", status: "ok", badges: [] });
    }
    return toFlowElements({
      nodes: [...scaleLab.nodes, ...extraNodes],
      edges: [...scaleLab.edges, ...extraEdges],
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
  }

  it("bundles the dense group once zoomed below the full-detail threshold, and unbundles on click", () => {
    const { container } = render(<TopologyCanvasV2 elements={buildElements()} theme="light" {...baseProps()} />);
    const target = container.querySelector('[data-testid="topology-canvas-v2"]');
    if (!target) throw new Error("missing canvas");

    const region = screen.getByRole("application", { name: /Topology map/ });
    expect(within(region).getAllByLabelText(/^guest-nic expanded-/)).toHaveLength(guestCount);

    // 8 ticks: zoom drops from 1 to ~0.47 — into "simplified" (bundling
    // active) without also crossing into "capsule".
    zoomOut(target, 8);

    expect(within(region).queryAllByLabelText(/^guest-nic expanded-/)).toHaveLength(0);
    // Look up the bundle proxy by its synthetic id rather than its label
    // text: scale-lab's 24 *real*, pre-existing guest-group pills span a
    // range of collapsedCount values, and one of them can coincidentally
    // also read "N guests" for whatever N this test picks — the id is the
    // only collision-free handle.
    const key = `${targetBridge}|pve1`;
    const bundleProxy = container.querySelector<HTMLElement>(`[data-entity-id="${bundleId(key)}"]`);
    expect(bundleProxy).not.toBeNull();
    if (!bundleProxy) return;
    expect(bundleProxy.getAttribute("aria-label")).toContain(`${String(guestCount)} guests`);
    expect(bundleProxy.getAttribute("aria-label")).toContain(`count=${String(guestCount)}`);

    fireEvent.click(bundleProxy);

    expect(container.querySelector(`[data-entity-id="${bundleId(key)}"]`)).toBeNull();
    expect(within(region).getAllByLabelText(/^guest-nic expanded-/)).toHaveLength(guestCount);
  });
});

describe("TopologyCanvasV2 + LOD: manual overrides reset when the underlying topology changes", () => {
  it("a capsule expanded by click re-collapses once the element set signature changes", () => {
    const elements = toFlowElements({
      nodes: scaleLab.nodes,
      edges: scaleLab.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    const onNodeClick = vi.fn();
    const { container, rerender } = render(
      <TopologyCanvasV2 elements={elements} theme="light" {...baseProps()} onNodeClick={onNodeClick} />,
    );
    const target = container.querySelector('[data-testid="topology-canvas-v2"]');
    if (!target) throw new Error("missing canvas");
    zoomOut(target, 20);

    const region = screen.getByRole("application", { name: /Topology map/ });
    const firstCapsule = within(region).getAllByLabelText(/phys-capsule/)[0];
    expect(firstCapsule).toBeDefined();
    if (!firstCapsule) return;
    fireEvent.click(firstCapsule);
    expect(within(region).getAllByLabelText(/phys-capsule/)).toHaveLength(7);

    // A different element set (a subset) re-renders — the manual override
    // for "pve1" no longer applies to this new signature.
    const smaller = toFlowElements({
      nodes: scaleLab.nodes.slice(0, -1),
      edges: scaleLab.edges,
      expandedGroups: new Set(),
      activeLayers: ALL_LAYERS,
      layoutPositions: new Map(),
      manualPositions: {},
    });
    rerender(<TopologyCanvasV2 elements={smaller} theme="light" {...baseProps()} onNodeClick={onNodeClick} />);
    expect(within(region).getAllByLabelText(/phys-capsule/)).toHaveLength(8);

    // Never confused a capsule/bundle click for a real entity click.
    expect(onNodeClick).not.toHaveBeenCalled();
  });
});
