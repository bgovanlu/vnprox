// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { FwLogEntry } from "../api/types";
import {
  CLIENT_BUFFER_CAP,
  RENDER_CAP,
  fwLogReducer,
  initialFwLogViewState,
  matchesFilter,
  selectVisibleEntries,
  type FwLogViewState,
} from "./reducer";

function entry(overrides: Partial<FwLogEntry> = {}): FwLogEntry {
  return {
    seq: 1,
    node: "pve1",
    vmid: 100,
    direction: "in",
    action: "DROP",
    raw: "raw line",
    correlation: { status: "unmatched", reason: "test" },
    ...overrides,
  };
}

describe("fwLogReducer: loaded", () => {
  it("replaces entries with the REST page and records droppedTotal", () => {
    const page = { items: [entry({ seq: 1 }), entry({ seq: 2 })], droppedTotal: 5 };
    const state = fwLogReducer(initialFwLogViewState, { type: "loaded", page });
    expect(state.entries).toHaveLength(2);
    expect(state.serverDroppedTotal).toBe(5);
  });

  it("caps the loaded page to CLIENT_BUFFER_CAP and counts the excess as client-dropped", () => {
    const items = Array.from({ length: CLIENT_BUFFER_CAP + 50 }, (_, i) => entry({ seq: i }));
    const state = fwLogReducer(initialFwLogViewState, { type: "loaded", page: { items, droppedTotal: 0 } });
    expect(state.entries).toHaveLength(CLIENT_BUFFER_CAP);
    expect(state.clientDroppedTotal).toBe(50);
    // Keeps the newest, not the oldest.
    expect(state.entries[state.entries.length - 1]?.seq).toBe(CLIENT_BUFFER_CAP + 49);
  });
});

describe("fwLogReducer: batch (follow mode)", () => {
  it("appends new entries and updates serverDroppedTotal", () => {
    const loaded = fwLogReducer(initialFwLogViewState, {
      type: "loaded",
      page: { items: [entry({ seq: 1 })], droppedTotal: 0 },
    });
    const next = fwLogReducer(loaded, { type: "batch", entries: [entry({ seq: 2 })], droppedTotal: 3 });
    expect(next.entries.map((e) => e.seq)).toEqual([1, 2]);
    expect(next.serverDroppedTotal).toBe(3);
  });

  it("evicts the oldest entries beyond CLIENT_BUFFER_CAP and increments clientDroppedTotal", () => {
    let state: FwLogViewState = initialFwLogViewState;
    // Fill to exactly the cap.
    state = fwLogReducer(state, {
      type: "loaded",
      page: { items: Array.from({ length: CLIENT_BUFFER_CAP }, (_, i) => entry({ seq: i })), droppedTotal: 0 },
    });
    expect(state.entries).toHaveLength(CLIENT_BUFFER_CAP);

    state = fwLogReducer(state, { type: "batch", entries: [entry({ seq: 999_999 })], droppedTotal: 0 });
    expect(state.entries).toHaveLength(CLIENT_BUFFER_CAP);
    expect(state.clientDroppedTotal).toBe(1);
    expect(state.entries[state.entries.length - 1]?.seq).toBe(999_999);
    expect(state.entries[0]?.seq).toBe(1); // the oldest (seq 0) was evicted
  });

  it("routes batches into `pending` while paused, leaving `entries` untouched", () => {
    let state = fwLogReducer(initialFwLogViewState, { type: "pause" });
    state = fwLogReducer(state, { type: "batch", entries: [entry({ seq: 1 }), entry({ seq: 2 })], droppedTotal: 0 });
    expect(state.entries).toHaveLength(0);
    expect(state.pending).toHaveLength(2);
    expect(state.paused).toBe(true);
  });
});

describe("fwLogReducer: pause/resume", () => {
  it("resume flushes pending into entries, oldest first, and clears pending", () => {
    let state = fwLogReducer(initialFwLogViewState, {
      type: "loaded",
      page: { items: [entry({ seq: 1 })], droppedTotal: 0 },
    });
    state = fwLogReducer(state, { type: "pause" });
    state = fwLogReducer(state, { type: "batch", entries: [entry({ seq: 2 }), entry({ seq: 3 })], droppedTotal: 0 });
    expect(state.entries.map((e) => e.seq)).toEqual([1]);

    state = fwLogReducer(state, { type: "resume" });
    expect(state.paused).toBe(false);
    expect(state.pending).toHaveLength(0);
    expect(state.entries.map((e) => e.seq)).toEqual([1, 2, 3]);
  });

  it("resume is a no-op when not paused", () => {
    const state = fwLogReducer(initialFwLogViewState, { type: "resume" });
    expect(state).toEqual(initialFwLogViewState);
  });

  it("pause is idempotent", () => {
    const once = fwLogReducer(initialFwLogViewState, { type: "pause" });
    const twice = fwLogReducer(once, { type: "pause" });
    expect(twice).toEqual(once);
  });
});

describe("fwLogReducer: filter composition", () => {
  it("setFilter merges into the existing filter, leaving other fields untouched", () => {
    let state = fwLogReducer(initialFwLogViewState, { type: "setFilter", filter: { node: "pve1" } });
    state = fwLogReducer(state, { type: "setFilter", filter: { direction: "in" } });
    expect(state.filter).toEqual({ node: "pve1", vmid: "", direction: "in", action: "" });
  });

  it("matchesFilter ANDs every set field, case-insensitively", () => {
    const e = entry({ node: "pve1", vmid: 100, direction: "in", action: "DROP" });
    expect(matchesFilter(e, { node: "", vmid: "", direction: "", action: "" })).toBe(true);
    expect(matchesFilter(e, { node: "PVE1", vmid: "", direction: "", action: "" })).toBe(true);
    expect(matchesFilter(e, { node: "pve2", vmid: "", direction: "", action: "" })).toBe(false);
    expect(matchesFilter(e, { node: "", vmid: "100", direction: "", action: "" })).toBe(true);
    expect(matchesFilter(e, { node: "", vmid: "101", direction: "", action: "" })).toBe(false);
    expect(matchesFilter(e, { node: "", vmid: "", direction: "out", action: "" })).toBe(false);
    expect(matchesFilter(e, { node: "", vmid: "", direction: "", action: "drop" })).toBe(true);
    // Every field must match simultaneously (AND, not OR).
    expect(matchesFilter(e, { node: "pve1", vmid: "100", direction: "in", action: "drop" })).toBe(true);
    expect(matchesFilter(e, { node: "pve1", vmid: "100", direction: "out", action: "drop" })).toBe(false);
  });

  it("selectVisibleEntries returns everything when the filter is empty", () => {
    const state = fwLogReducer(initialFwLogViewState, {
      type: "loaded",
      page: { items: [entry({ seq: 1, node: "pve1" }), entry({ seq: 2, node: "pve2" })], droppedTotal: 0 },
    });
    expect(selectVisibleEntries(state)).toHaveLength(2);
  });

  it("selectVisibleEntries narrows by the composed filter", () => {
    let state = fwLogReducer(initialFwLogViewState, {
      type: "loaded",
      page: {
        items: [
          entry({ seq: 1, node: "pve1", direction: "in" }),
          entry({ seq: 2, node: "pve2", direction: "out" }),
          entry({ seq: 3, node: "pve1", direction: "out" }),
        ],
        droppedTotal: 0,
      },
    });
    state = fwLogReducer(state, { type: "setFilter", filter: { node: "pve1" } });
    expect(selectVisibleEntries(state).map((e) => e.seq)).toEqual([1, 3]);

    state = fwLogReducer(state, { type: "setFilter", filter: { direction: "out" } });
    expect(selectVisibleEntries(state).map((e) => e.seq)).toEqual([3]);
  });
});

describe("fwLogReducer: clear", () => {
  it("empties entries/pending and resets clientDroppedTotal, but keeps the filter and serverDroppedTotal", () => {
    let state = fwLogReducer(initialFwLogViewState, {
      type: "loaded",
      page: { items: [entry({ seq: 1 })], droppedTotal: 7 },
    });
    state = fwLogReducer(state, { type: "setFilter", filter: { node: "pve1" } });
    state = fwLogReducer(state, { type: "clear" });
    expect(state.entries).toHaveLength(0);
    expect(state.pending).toHaveLength(0);
    expect(state.clientDroppedTotal).toBe(0);
    expect(state.serverDroppedTotal).toBe(7);
    expect(state.filter.node).toBe("pve1");
  });
});

// This is the frontend counterpart of the backend's
// TestService_Storm_10kLinesPerMinute: feed 10,000 synthetic follow-mode
// batches through the reducer (no timers, no DOM, no network — just the
// pure state machine) within a time budget, and assert the client buffer
// never grows past its cap and the drop indicator engages.
describe("fwLogReducer: storm (AC3)", () => {
  it("processes a 10k-line storm fast, keeps the buffer bounded, and engages the drop indicator", () => {
    const start = performance.now();
    let state: FwLogViewState = initialFwLogViewState;
    let seq = 0;
    const totalLines = 10_000;
    const batchSize = 50; // smaller than one WS push in practice, to exercise many reducer calls

    for (let sent = 0; sent < totalLines; sent += batchSize) {
      const batch = Array.from({ length: batchSize }, () => entry({ seq: seq++ }));
      state = fwLogReducer(state, { type: "batch", entries: batch, droppedTotal: Math.floor(seq / 2) });
    }
    const elapsed = performance.now() - start;

    expect(elapsed).toBeLessThan(2000);
    expect(state.entries.length).toBeLessThanOrEqual(CLIENT_BUFFER_CAP);
    expect(state.clientDroppedTotal).toBeGreaterThan(0);
    expect(state.serverDroppedTotal).toBeGreaterThan(0);

    // The rendered view (RENDER_CAP) must also stay bounded regardless of
    // how much history the buffer itself retains.
    const rendered = selectVisibleEntries(state).slice(-RENDER_CAP);
    expect(rendered.length).toBeLessThanOrEqual(RENDER_CAP);
  });

  it("a storm while paused still bounds `pending` and counts client drops", () => {
    let state = fwLogReducer(initialFwLogViewState, { type: "pause" });
    let seq = 0;
    for (let i = 0; i < 200; i++) {
      const batch = Array.from({ length: 50 }, () => entry({ seq: seq++ }));
      state = fwLogReducer(state, { type: "batch", entries: batch, droppedTotal: 0 });
    }
    expect(state.pending.length).toBeLessThanOrEqual(CLIENT_BUFFER_CAP);
    expect(state.clientDroppedTotal).toBeGreaterThan(0);
    expect(state.entries).toHaveLength(0); // still frozen
  });
});
