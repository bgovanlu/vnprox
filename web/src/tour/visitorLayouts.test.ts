// SPDX-License-Identifier: Apache-2.0

// The public demo's layout persistence: the same four operations
// api/layouts.ts offers, against the edge's per-visitor scratch store
// instead of the daemon's /layouts routes (which are refused at the edge).
//
// The isolation itself is a server-side property and is asserted where it
// lives — internal/publicdemo and cmd/vnproxd. What is asserted here is the
// client half: the 404 convention existing callers depend on, and that a
// refused save does not leave this tab believing it saved.
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import type { TopologyLayoutPayload } from "../api/types";
import {
  deleteVisitorLayout,
  fetchVisitorLayout,
  listVisitorLayouts,
  resetVisitorLayoutCache,
  saveVisitorLayout,
} from "./visitorLayouts";
import { fetchVisitorState, saveVisitorState } from "./visitorApi";

vi.mock("./visitorApi", () => ({
  fetchVisitorState: vi.fn(() => Promise.resolve(null)),
  saveVisitorState: vi.fn(() => Promise.resolve()),
  fetchVisitorSession: vi.fn(() => Promise.resolve(null)),
}));

const mockedFetch = vi.mocked(fetchVisitorState);
const mockedSave = vi.mocked(saveVisitorState);

const layout: TopologyLayoutPayload = { positions: { pve1: { x: 1, y: 2 } }, activeLayers: ["phys"] };

beforeEach(() => {
  resetVisitorLayoutCache();
  mockedFetch.mockReset();
  mockedFetch.mockResolvedValue(null);
  mockedSave.mockReset();
  mockedSave.mockResolvedValue(undefined);
});

describe("visitor layouts", () => {
  it("rejects with a 404 ApiError when this visitor has never saved one", async () => {
    await expect(fetchVisitorLayout("topology")).rejects.toMatchObject({ status: 404 });
    await expect(fetchVisitorLayout("topology")).rejects.toBeInstanceOf(ApiError);
  });

  it("stores every layout under one scratch key, so a name can be anything", async () => {
    await saveVisitorLayout("a view with spaces & punctuation", layout);
    expect(mockedSave).toHaveBeenCalledWith("layouts", { "a view with spaces & punctuation": layout });
    // One key, not one per layout: the edge's scratch keys are a restricted
    // character set and a sanitiser would collide two view names into one.
    expect(mockedSave.mock.calls.every(([key]) => key === "layouts")).toBe(true);
  });

  it("reads back what it saved", async () => {
    await saveVisitorLayout("topology", layout);
    await expect(fetchVisitorLayout("topology")).resolves.toMatchObject({ name: "topology", layout });
  });

  it("does not believe a refused save", async () => {
    mockedSave.mockRejectedValue(new ApiError(413, "public_demo_state_too_large", "capped"));
    await expect(saveVisitorLayout("topology", layout)).rejects.toMatchObject({ status: 413 });
    // The cache must not hold a value the edge refused, or the next read
    // would report a layout that does not exist.
    await expect(fetchVisitorLayout("topology")).rejects.toMatchObject({ status: 404 });
  });

  it("lists what this visitor has", async () => {
    mockedFetch.mockResolvedValue({ topology: layout, "view:noc": layout });
    await expect(listVisitorLayouts()).resolves.toEqual({
      items: [
        { name: "topology", layout, updatedAt: 0 },
        { name: "view:noc", layout, updatedAt: 0 },
      ],
    });
  });

  it("deletes, idempotently", async () => {
    mockedFetch.mockResolvedValue({ topology: layout, other: layout });
    await deleteVisitorLayout("topology");
    expect(mockedSave).toHaveBeenCalledWith("layouts", { other: layout });
    await expect(fetchVisitorLayout("topology")).rejects.toMatchObject({ status: 404 });

    mockedSave.mockClear();
    await deleteVisitorLayout("topology");
    expect(mockedSave, "deleting a layout that is not there wrote anyway").not.toHaveBeenCalled();
  });

  it("reads the visitor's scratch store once per tab, not once per call", async () => {
    mockedFetch.mockResolvedValue({ topology: layout });
    await fetchVisitorLayout("topology");
    await fetchVisitorLayout("topology");
    await listVisitorLayouts();
    expect(mockedFetch).toHaveBeenCalledTimes(1);
  });
});
