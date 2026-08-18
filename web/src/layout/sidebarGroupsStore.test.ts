// T-3402: exercises the actual persistence mechanism behind Sidebar.tsx's
// "collapse state persisted in localStorage" requirement — zustand's
// `persist` middleware round-tripping through localStorage. Mirrors
// src/store/theme.test.ts's own doc comment and technique exactly: a real
// page reload can't happen inside one Vitest/jsdom process (the module
// graph is cached), so `vi.resetModules()` + a fresh `import()` stands in
// for it, re-running this module's top-level `create(persist(...))` call
// exactly as a real page load would, reading whatever is in localStorage
// at that moment. No React import anywhere in this file on purpose — this
// store has none of its own, and re-importing React/react-router-dom
// modules after `vi.resetModules()` would hand a freshly-rendered
// component a different React context instance than the one any
// statically-imported test-rendering helper is using. Sidebar.test.tsx
// covers the store <-> rendered-UI wiring; this file covers the store's
// own persistence in isolation.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

describe("sidebar groups store persistence", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("defaults every group to expanded (no localStorage entry yet)", async () => {
    const { useSidebarGroupsStore, isSidebarGroupExpanded } = await import("./sidebarGroupsStore");
    const { expanded } = useSidebarGroupsStore.getState();
    expect(isSidebarGroupExpanded(expanded, "network")).toBe(true);
    expect(isSidebarGroupExpanded(expanded, "operate")).toBe(true);
    expect(isSidebarGroupExpanded(expanded, "automate")).toBe(true);
  });

  it("persists a collapse to localStorage", async () => {
    const { useSidebarGroupsStore } = await import("./sidebarGroupsStore");
    useSidebarGroupsStore.getState().setExpanded("network", false);

    const stored = localStorage.getItem("vnprox.sidebarGroups");
    expect(stored).toBeTruthy();
    expect(JSON.parse(stored ?? "{}")).toMatchObject({ state: { expanded: { network: false } } });
  });

  it("hydrates a collapsed group from localStorage on a fresh module load (simulated reload)", async () => {
    localStorage.setItem(
      "vnprox.sidebarGroups",
      JSON.stringify({ state: { expanded: { network: false } }, version: 0 }),
    );

    const { useSidebarGroupsStore, isSidebarGroupExpanded } = await import("./sidebarGroupsStore");
    const { expanded } = useSidebarGroupsStore.getState();
    expect(isSidebarGroupExpanded(expanded, "network")).toBe(false);
    // A group this run never touched stays at the "never toggled" default.
    expect(isSidebarGroupExpanded(expanded, "operate")).toBe(true);
  });

  it("round-trips a collapse through a simulated reload", async () => {
    const first = await import("./sidebarGroupsStore");
    first.useSidebarGroupsStore.getState().setExpanded("automate", false);
    expect(first.isSidebarGroupExpanded(first.useSidebarGroupsStore.getState().expanded, "automate")).toBe(false);

    vi.resetModules();
    const second = await import("./sidebarGroupsStore");
    expect(second.isSidebarGroupExpanded(second.useSidebarGroupsStore.getState().expanded, "automate")).toBe(false);
    // Re-expanding is the same write path in reverse.
    second.useSidebarGroupsStore.getState().setExpanded("automate", true);
    expect(second.isSidebarGroupExpanded(second.useSidebarGroupsStore.getState().expanded, "automate")).toBe(true);
  });
});
