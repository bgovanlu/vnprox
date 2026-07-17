// T-903 AC4's DOM-wiring half: arrow keys move real focus between
// `data-entity-ref` elements in visual-adjacency order, and Enter activates
// the focused one the same way a click would (TopologyPage wires
// `onActivate` straight to its existing `handleNodeClick` — selection →
// inspector open — so this proves that contract at the hook level without
// needing the full Topology page/fixture stood up).
import { useRef } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useRovingFocus } from "./useRovingFocus";

// jsdom's getBoundingClientRect always returns all-zero rects; stub each
// element's per test with a fixed position so visual-adjacency ordering
// has something real to sort by.
function stubRect(el: HTMLElement, x: number, y: number): void {
  vi.spyOn(el, "getBoundingClientRect").mockReturnValue({
    x,
    y,
    left: x,
    top: y,
    right: x + 40,
    bottom: y + 20,
    width: 40,
    height: 20,
    toJSON: () => ({}),
  });
}

function Harness({ onActivate }: { onActivate: (id: string) => void }) {
  const containerRef = useRef<HTMLDivElement>(null);
  useRovingFocus({ containerRef, onActivate });
  return (
    <div ref={containerRef}>
      <button type="button" data-entity-ref="bond0">
        bond0
      </button>
      <button type="button" data-entity-ref="vmbr0">
        vmbr0
      </button>
      <div role="button" tabIndex={0} data-entity-ref="eno1">
        eno1
      </div>
    </div>
  );
}

function renderHarness(onActivate: (id: string) => void = vi.fn()) {
  const result = render(<Harness onActivate={onActivate} />);
  stubRect(screen.getByText("bond0"), 0, 0);
  stubRect(screen.getByText("vmbr0"), 100, 0);
  stubRect(screen.getByText("eno1"), 0, 100);
  return result;
}

describe("useRovingFocus", () => {
  it("moves DOM focus to the next entity in visual-adjacency order on ArrowRight", () => {
    renderHarness();
    const bond0 = screen.getByText("bond0");
    bond0.focus();
    expect(bond0).toHaveFocus();

    fireEvent.keyDown(bond0, { key: "ArrowRight" });

    expect(screen.getByText("vmbr0")).toHaveFocus();
  });

  it("moves DOM focus to the previous entity on ArrowLeft, wrapping at the start", () => {
    renderHarness();
    const bond0 = screen.getByText("bond0");
    bond0.focus();

    fireEvent.keyDown(bond0, { key: "ArrowLeft" });

    // bond0 is first in visual order (row 1: bond0, vmbr0; row 2: eno1) —
    // ArrowLeft wraps to the last entity, eno1.
    expect(screen.getByText("eno1")).toHaveFocus();
  });

  it("moves focus down into the next row on ArrowDown", () => {
    renderHarness();
    const vmbr0 = screen.getByText("vmbr0");
    vmbr0.focus();

    fireEvent.keyDown(vmbr0, { key: "ArrowDown" });

    expect(screen.getByText("eno1")).toHaveFocus();
  });

  it("Enter activates the focused entity exactly once — the same action a click would (selection → inspector open)", () => {
    const onActivate = vi.fn();
    renderHarness(onActivate);
    const vmbr0 = screen.getByText("vmbr0");
    vmbr0.focus();

    fireEvent.keyDown(vmbr0, { key: "Enter" });

    expect(onActivate).toHaveBeenCalledTimes(1);
    expect(onActivate).toHaveBeenCalledWith("vmbr0");
  });

  it("does not react to arrow keys fired outside any data-entity-ref element", () => {
    const { container } = renderHarness();
    fireEvent.keyDown(container, { key: "ArrowRight" });
    // Nothing in the harness should have gained focus as a result.
    expect(document.activeElement).not.toHaveAttribute("data-entity-ref");
  });
});
