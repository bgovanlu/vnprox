// SPDX-License-Identifier: Apache-2.0

// T-908 acceptance criterion 1's core state-transition rules, tested as a
// pure reducer (no React needed) — see inspectorStack.ts's doc comment.
import { describe, expect, it } from "vitest";
import { closePane, MAX_INSPECTOR_PANES, selectPane, togglePin, type InspectorPane } from "./inspectorStack";

describe("selectPane", () => {
  it("with nothing pinned, replaces the whole stack with the new selection", () => {
    const panes: InspectorPane[] = [{ ref: "bond:pve1:bond0", pinned: false }];
    expect(selectPane(panes, "bond:pve2:bond0")).toEqual([{ ref: "bond:pve2:bond0", pinned: false }]);
  });

  it("is a no-op when the ref is already open", () => {
    const panes: InspectorPane[] = [{ ref: "bond:pve1:bond0", pinned: true }];
    expect(selectPane(panes, "bond:pve1:bond0")).toEqual(panes);
  });

  it("with something pinned, adds the new selection as an additional pane", () => {
    const panes: InspectorPane[] = [{ ref: "bond:pve1:bond0", pinned: true }];
    expect(selectPane(panes, "bond:pve2:bond0")).toEqual([
      { ref: "bond:pve1:bond0", pinned: true },
      { ref: "bond:pve2:bond0", pinned: false },
    ]);
  });

  it("never exceeds MAX_INSPECTOR_PANES, evicting the oldest unpinned pane to make room", () => {
    const panes: InspectorPane[] = [
      { ref: "a", pinned: true },
      { ref: "b", pinned: false },
      { ref: "c", pinned: false },
      { ref: "d", pinned: false },
    ];
    expect(panes.length).toBe(MAX_INSPECTOR_PANES);
    const next = selectPane(panes, "e");
    expect(next.length).toBe(MAX_INSPECTOR_PANES);
    expect(next.map((p) => p.ref)).toEqual(["a", "c", "d", "e"]);
  });

  it("drops the new selection outright when every pane at the cap is pinned", () => {
    const panes: InspectorPane[] = [
      { ref: "a", pinned: true },
      { ref: "b", pinned: true },
      { ref: "c", pinned: true },
      { ref: "d", pinned: true },
    ];
    expect(selectPane(panes, "e")).toEqual(panes);
  });
});

describe("closePane", () => {
  it("removes only the named pane, leaving the others intact", () => {
    const panes: InspectorPane[] = [
      { ref: "bond:pve1:bond0", pinned: true },
      { ref: "bond:pve2:bond0", pinned: false },
    ];
    expect(closePane(panes, "bond:pve2:bond0")).toEqual([{ ref: "bond:pve1:bond0", pinned: true }]);
  });
});

describe("togglePin", () => {
  it("flips pinned for the named pane only, preserving order", () => {
    const panes: InspectorPane[] = [
      { ref: "a", pinned: false },
      { ref: "b", pinned: false },
    ];
    expect(togglePin(panes, "a")).toEqual([
      { ref: "a", pinned: true },
      { ref: "b", pinned: false },
    ]);
  });
});
