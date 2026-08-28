// SPDX-License-Identifier: Apache-2.0

// T-2806's canvas-layer tests: the escaping assertion for the map render
// path (AC6), the orphaned-note rendering (AC2), and the region-persistence
// property (AC5) at the level where a client could break it — a view switch
// or a layout save must not disturb the region cache.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Annotation, MapRegion } from "../api/types";
import { AnnotationLayer } from "./AnnotationLayer";
import { MAP_REGIONS_QUERY_KEY, useMapRegionsQuery } from "./annotationsQueries";

const VIEWPORT = { x: 0, y: 0, zoom: 1 };

/** The classic operator-authored injection payload: text one operator typed
 * into a note, rendered on another operator's map. */
const HOSTILE = `<img src=x onerror="alert(1)">`;

function note(partial: Partial<Annotation> & Pick<Annotation, "id" | "ref" | "content">): Annotation {
  return {
    createdBy: "alice@pve",
    createdAt: 1,
    updatedAt: 1,
    expiresAt: 0,
    expired: false,
    orphaned: false,
    ...partial,
  };
}

function region(partial: Partial<MapRegion> & Pick<MapRegion, "id" | "label">): MapRegion {
  return {
    color: "",
    createdBy: "alice@pve",
    x: 10,
    y: 20,
    w: 300,
    h: 180,
    createdAt: 1,
    updatedAt: 1,
    expiresAt: 0,
    expired: false,
    ...partial,
  };
}

describe("AnnotationLayer", () => {
  it("draws each region with its label, positioned in graph space", () => {
    render(
      <AnnotationLayer
        regions={[region({ id: "r1", label: "vendor-managed, do not touch" })]}
        notes={[]}
        anchors={[]}
        viewport={VIEWPORT}
      />,
    );

    const rendered = screen.getByTestId("map-region");
    expect(rendered).toHaveStyle({ left: "10px", top: "20px", width: "300px", height: "180px" });
    expect(screen.getByText("vendor-managed, do not touch")).toBeInTheDocument();
  });

  it("pins a note marker to its entity's on-canvas anchor", () => {
    render(
      <AnnotationLayer
        regions={[]}
        notes={[note({ id: "a1", ref: "bridge:pve1:vmbr0", content: "temporary until the switch swap" })]}
        anchors={[{ ref: "bridge:pve1:vmbr0", x: 40, y: 60 }]}
        viewport={VIEWPORT}
      />,
    );

    const marker = screen.getByTestId("annotation-marker");
    expect(marker).toHaveAttribute("data-entity-ref", "bridge:pve1:vmbr0");
    expect(marker).toHaveTextContent("temporary until the switch swap");
  });

  // T-2806 AC2, canvas half: an annotation whose entity is gone still has to
  // be visible somewhere. It has no anchor to hang off, so it moves to the
  // orphan list rather than silently disappearing with the entity.
  it("keeps a note whose entity is no longer on the map, in the orphan list", () => {
    render(
      <AnnotationLayer
        regions={[]}
        notes={[
          note({ id: "a1", ref: "bridge:pve1:vmbr0", content: "still anchored" }),
          note({
            id: "a2",
            ref: "bridge:pve1:vmbr9",
            content: "removed: vendor switch could not trunk VLAN 40",
            orphaned: true,
          }),
        ]}
        anchors={[{ ref: "bridge:pve1:vmbr0", x: 0, y: 0 }]}
        viewport={VIEWPORT}
      />,
    );

    const orphans = screen.getByTestId("orphan-notes");
    expect(orphans).toHaveTextContent("removed: vendor switch could not trunk VLAN 40");
    expect(orphans).toHaveTextContent("bridge:pve1:vmbr9");
    expect(orphans).not.toHaveTextContent("still anchored");
  });

  // T-2806 AC6, render path: the map canvas overlay. This is a DIFFERENT
  // path from the inspector's note list (AnnotationsSection.test.tsx), and
  // is asserted separately for that reason — one assertion per path.
  it("escapes note text on the canvas: markup renders as literal text, not as an element", () => {
    const { container } = render(
      <AnnotationLayer
        regions={[]}
        notes={[note({ id: "a1", ref: "bridge:pve1:vmbr0", content: HOSTILE })]}
        anchors={[{ ref: "bridge:pve1:vmbr0", x: 0, y: 0 }]}
        viewport={VIEWPORT}
      />,
    );

    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByTestId("annotation-marker")).toHaveTextContent(HOSTILE);
  });

  // T-2806 AC6, render path: the same canvas layer's REGION labels, which
  // are written by a different element than the note markers above.
  it("escapes region labels on the canvas", () => {
    const { container } = render(
      <AnnotationLayer regions={[region({ id: "r1", label: HOSTILE })]} notes={[]} anchors={[]} viewport={VIEWPORT} />,
    );

    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByTestId("map-region")).toHaveTextContent(HOSTILE);
  });

  it("applies pan/zoom as one container transform rather than per element", () => {
    render(
      <AnnotationLayer
        regions={[region({ id: "r1", label: "grouped" })]}
        notes={[]}
        anchors={[]}
        viewport={{ x: -120, y: 40, zoom: 1.4 }}
      />,
    );

    const rendered = screen.getByTestId("map-region");
    // The region keeps its GRAPH-space position; the transform lives on the
    // ancestor, which is what keeps a pan frame to one style write.
    expect(rendered).toHaveStyle({ left: "10px", top: "20px" });
    const transformed = rendered.parentElement;
    expect(transformed?.style.transform).toBe("translate(-120px, 40px) scale(1.4)");
  });
});

// --- AC5: regions persist across layout changes and view switches ---------

vi.mock("../api/annotations", () => ({
  fetchAnnotations: vi.fn(() => Promise.resolve({ items: [] })),
  createAnnotation: vi.fn(),
  deleteAnnotation: vi.fn(),
  fetchMapRegions: vi.fn(() => Promise.resolve({ items: [region({ id: "r1", label: "vendor-managed" })] })),
  createMapRegion: vi.fn(),
  deleteMapRegion: vi.fn(),
}));

/** A miniature of the topology page's own structure: a view-mode toggle
 * around a canvas that renders the region layer from the regions query. */
function ViewHarness() {
  const [view, setView] = useState<"graph" | "switch">("graph");
  const { data: regions } = useMapRegionsQuery();
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setView((v) => (v === "graph" ? "switch" : "graph"));
        }}
      >
        toggle view
      </button>
      {view === "graph" && (
        <AnnotationLayer regions={regions ?? []} notes={[]} anchors={[]} viewport={VIEWPORT} />
      )}
    </div>
  );
}

describe("map regions persistence (T-2806 AC5)", () => {
  it("survives a view switch away and back, and a layout write in between", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const user = (await import("@testing-library/user-event")).default.setup();

    render(
      <QueryClientProvider client={queryClient}>
        <ViewHarness />
      </QueryClientProvider>,
    );

    await screen.findByText("vendor-managed");

    // Switch to the other view: the graph canvas (and its region layer)
    // unmounts entirely.
    await user.click(screen.getByRole("button", { name: "toggle view" }));
    expect(screen.queryByTestId("map-region")).toBeNull();

    // A layout save happens while we are away — the canvas auto-save that
    // fires whenever anyone drags a node. It writes a DIFFERENT query key,
    // so it cannot disturb the regions cache; if regions had been stored in
    // the layout blob (the design this task deliberately rejected) this is
    // exactly where they would be lost.
    act(() => {
      queryClient.setQueryData(["layouts", "topology"], { name: "topology", layout: {}, updatedAt: 2 });
    });

    await user.click(screen.getByRole("button", { name: "toggle view" }));
    await screen.findByText("vendor-managed");
    expect(queryClient.getQueryData(MAP_REGIONS_QUERY_KEY)).toHaveLength(1);
  });
});
