// T-403 acceptance criterion 4: "Preview pane updates live as parameters
// change (<100ms perceived; debounced)". useDebouncedValue.test.ts already
// proves the debounce primitive itself settles well under 100ms after the
// last change; this file proves WizardPreviewPane actually uses it to
// avoid recomputing the real layout (computeLayout) on every intermediate
// param change during a rapid burst — only the settled, final graph
// triggers a layout pass.
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PreviewGraph } from "./previewEntities";
import { WizardPreviewPane } from "./WizardPreviewPane";

const { computeLayoutMock } = vi.hoisted(() => ({ computeLayoutMock: vi.fn() }));
vi.mock("../../topology/layout", async () => {
  const actual = await vi.importActual<typeof import("../../topology/layout")>("../../topology/layout");
  return { ...actual, computeLayout: computeLayoutMock };
});

function graphWithNodeCount(n: number): PreviewGraph {
  return {
    nodes: Array.from({ length: n }, (_, i) => ({
      id: `wizard-preview:n${String(i)}`,
      kind: "sdn-zone",
      label: `n${String(i)}`,
      layer: "sdn" as const,
      nodeGroup: "",
      status: "unknown" as const,
      badges: [],
    })),
    edges: [],
  };
}

beforeEach(() => {
  computeLayoutMock.mockReset();
  computeLayoutMock.mockResolvedValue(new Map());
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("WizardPreviewPane debounce", () => {
  it("collapses a rapid burst of param changes into exactly one layout computation", async () => {
    const { rerender } = render(<WizardPreviewPane graph={graphWithNodeCount(1)} debounceMs={80} />);
    // The very first render's own settle also computes a layout — let it
    // fire first so the burst below is isolated.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(80);
    });
    computeLayoutMock.mockClear();

    // A rapid burst of param changes, each well inside the debounce
    // window — mirrors a user adjusting several wizard fields quickly.
    for (let n = 2; n <= 5; n++) {
      rerender(<WizardPreviewPane graph={graphWithNodeCount(n)} debounceMs={80} />);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(20);
      });
    }
    expect(computeLayoutMock).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(80);
    });

    expect(computeLayoutMock).toHaveBeenCalledTimes(1);
    // Settled on the *last* graph in the burst (5 nodes), not an
    // intermediate one.
    expect(computeLayoutMock.mock.calls[0]?.[0]).toHaveLength(5);
  });

  it("commits well under the 100ms perceived-latency budget after the last change", async () => {
    const { rerender } = render(<WizardPreviewPane graph={graphWithNodeCount(1)} debounceMs={80} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(80);
    });
    computeLayoutMock.mockClear();

    rerender(<WizardPreviewPane graph={graphWithNodeCount(2)} debounceMs={80} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(99);
    });
    expect(computeLayoutMock).toHaveBeenCalledTimes(1);
  });
});
