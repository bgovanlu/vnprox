import { describe, expect, it } from "vitest";
import type { SavedViewState } from "./savedViews";
import {
  decodeViewFromSearch,
  encodeViewToSearch,
  fromSavedViewPayload,
  isSavedViewPayload,
  toSavedViewPayload,
} from "./savedViews";

const FULL_STATE: SavedViewState = {
  layers: ["phys", "sdn"],
  vlanFilter: 100,
  zoom: 1.4,
  viewport: { x: -120, y: 40 },
  selection: "bridge:pve1:vmbr0",
  view: "graph",
};

// T-907 AC1: "Saving a view against three-node-vlan (specific layers off, a
// VLAN filter set, zoomed/panned) and reloading it restores the exact same
// rendered state — a Vitest test round-trips the captured state through
// save/load." This is the save/load half: toSavedViewPayload (what PUT
// /layouts/{name} sends) and fromSavedViewPayload (what applying a fetched
// saved view reads back) must be exact inverses.
describe("toSavedViewPayload / fromSavedViewPayload round-trip (T-907 AC1)", () => {
  it("round-trips a full state through the layouts-table payload shape", () => {
    const payload = toSavedViewPayload(FULL_STATE);
    expect(payload.kind).toBe("view");
    expect(fromSavedViewPayload(payload)).toEqual(FULL_STATE);
  });

  it("round-trips a minimal state (no vlanFilter/selection)", () => {
    const minimal: SavedViewState = {
      layers: ["phys", "l2", "sdn", "guest"],
      zoom: 1,
      viewport: { x: 0, y: 0 },
      view: "switch",
    };
    const payload = toSavedViewPayload(minimal);
    expect(fromSavedViewPayload(payload)).toEqual(minimal);
  });
});

describe("isSavedViewPayload", () => {
  it("accepts a well-formed saved view payload", () => {
    expect(isSavedViewPayload(toSavedViewPayload(FULL_STATE))).toBe(true);
  });

  it("rejects the reserved auto-layout blob shape (no kind field)", () => {
    expect(isSavedViewPayload({ positions: {}, activeLayers: ["phys"] })).toBe(false);
  });

  it("rejects non-object and malformed values", () => {
    expect(isSavedViewPayload(undefined)).toBe(false);
    expect(isSavedViewPayload(null)).toBe(false);
    expect(isSavedViewPayload("view")).toBe(false);
    expect(isSavedViewPayload({ kind: "view" })).toBe(false);
    expect(isSavedViewPayload({ kind: "view", layers: ["phys"], zoom: 1, viewport: { x: 0 }, view: "graph" })).toBe(false);
    expect(isSavedViewPayload({ kind: "view", layers: ["phys"], zoom: 1, viewport: { x: 0, y: 0 }, view: "nope" })).toBe(false);
  });
});

// T-907 AC2: "A saved view's shareable URL, opened in a fresh session with
// no prior layouts row for that viewer, renders the same filtered/zoomed
// state (state lives in the URL, not only server-side)." — the URL
// encode/decode side needs no server round trip at all.
describe("encodeViewToSearch / decodeViewFromSearch round-trip (T-907 AC2)", () => {
  it("round-trips a full state through URL query params", () => {
    const params = encodeViewToSearch(FULL_STATE);
    expect(decodeViewFromSearch(params)).toEqual(FULL_STATE);
  });

  it("round-trips a minimal state (vlanFilter/selection omitted from the URL)", () => {
    const minimal: SavedViewState = {
      layers: ["l2"],
      zoom: 0.5,
      viewport: { x: 10, y: -10 },
      view: "switch",
    };
    const params = encodeViewToSearch(minimal);
    expect(params.has("svVlan")).toBe(false);
    expect(params.has("svSel")).toBe(false);
    expect(decodeViewFromSearch(params)).toEqual(minimal);
  });

  it("round-trips through a plain query string, not just URLSearchParams", () => {
    const params = encodeViewToSearch(FULL_STATE);
    const decoded = decodeViewFromSearch(params.toString());
    expect(decoded).toEqual(FULL_STATE);
  });

  it("returns undefined for a URL that carries none of this feature's params", () => {
    expect(decodeViewFromSearch(new URLSearchParams("foo=bar"))).toBeUndefined();
    expect(decodeViewFromSearch("")).toBeUndefined();
  });

  it("degrades a malformed layers list to every layer rather than throwing", () => {
    const decoded = decodeViewFromSearch("svLayers=bogus,phys");
    expect(decoded?.layers).toEqual(["phys"]);
  });

  it("degrades an empty layers list to every layer", () => {
    const decoded = decodeViewFromSearch("svLayers=");
    expect(decoded?.layers).toEqual(["phys", "l2", "sdn", "guest"]);
  });

  it("preserves an existing URLSearchParams' other keys when composing", () => {
    const params = new URLSearchParams("foo=bar");
    encodeViewToSearch(FULL_STATE, params);
    expect(params.get("foo")).toBe("bar");
    expect(decodeViewFromSearch(params)).toEqual(FULL_STATE);
  });
});
