import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});

// jsdom has no ResizeObserver (it doesn't lay out or paint anything), but
// @xyflow/react's canvas (src/topology/TopologyCanvas.tsx) requires one to
// mount at all — it watches its container for size changes. A minimal
// no-op stub is the standard fix for testing React Flow components under
// jsdom; real browsers always have the real implementation.
class ResizeObserverStub {
  observe(): void {
    /* no-op */
  }
  unobserve(): void {
    /* no-op */
  }
  disconnect(): void {
    /* no-op */
  }
}
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = ResizeObserverStub;
}
