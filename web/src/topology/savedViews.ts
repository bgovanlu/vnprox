// SPDX-License-Identifier: Apache-2.0

// T-907's named saved views: capturing/restoring the topology page's own
// layer/filter/zoom/selection/view-mode state, either as a `layouts`-table
// row (a named saved view, GET/PUT /layouts/{name}) or as flat URL query
// params (a shareable link that round-trips the same state without any
// server-side row — docs/api.md's Saved views & annotations section).
// Deliberately framework-free (no react-router import, no zustand import)
// so it's plain-object/URLSearchParams-testable, following
// web/src/simulator/urlState.ts's established convention exactly (that
// file's own doc comment: "T-107's layout/filter persistence... established
// convention of encoding page state in the URL").
import type { Layer, SavedViewPayload } from "../api/types";
import { ALL_LAYERS } from "../api/types";

/** The capturable subset of "current topology page state" — the same
 * fields as SavedViewPayload minus its `kind` discriminator (that tag only
 * matters once the state is written into a `layouts` row alongside the
 * reserved "topology"/"onboarding" blobs; the URL encoding below has no
 * such ambiguity to resolve, so it's omitted there as redundant). */
export type SavedViewState = Omit<SavedViewPayload, "kind">;

/** Runtime guard for a `layouts` list item's opaque `layout` value: true
 * iff it looks like a T-907 named saved view (kind: "view") rather than
 * the reserved "topology"/"onboarding" auto-layout blobs, which have no
 * `kind` field at all. Used by the saved-views picker to filter
 * `GET /layouts`' items down to actual named views. */
export function isSavedViewPayload(value: unknown): value is SavedViewPayload {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  if (v.kind !== "view") return false;
  if (!Array.isArray(v.layers) || !v.layers.every((l) => typeof l === "string")) return false;
  if (typeof v.zoom !== "number") return false;
  if (typeof v.viewport !== "object" || v.viewport === null) return false;
  const vp = v.viewport as Record<string, unknown>;
  if (typeof vp.x !== "number" || typeof vp.y !== "number") return false;
  if (v.view !== "graph" && v.view !== "switch") return false;
  return true;
}

/** Wraps captured state as the `layouts`-table blob T-907 saves under a
 * user-chosen name (PUT /layouts/{name}). */
export function toSavedViewPayload(state: SavedViewState): SavedViewPayload {
  return { kind: "view", ...state };
}

/** Strips the `kind` discriminator off a fetched saved view so it can be
 * applied to the live page state the same way a URL-decoded state is. */
export function fromSavedViewPayload(payload: SavedViewPayload): SavedViewState {
  const { kind: _kind, ...rest } = payload;
  return rest;
}

const PARAM_LAYERS = "svLayers";
const PARAM_VLAN = "svVlan";
const PARAM_ZOOM = "svZoom";
const PARAM_X = "svX";
const PARAM_Y = "svY";
const PARAM_SEL = "svSel";
const PARAM_VIEW = "svView";

/** Builds the query params for `state` (docs/api.md's Saved views &
 * annotations section: "flat per-field params, not an opaque blob") —
 * omits any optional field that isn't set, so a partial/default state
 * round-trips exactly as partial, mirroring urlState.ts's
 * encodeSimState convention. Starts from (and mutates) an optional
 * existing URLSearchParams so a caller can compose this alongside other
 * page query state without a second round trip. */
export function encodeViewToSearch(state: SavedViewState, params: URLSearchParams = new URLSearchParams()): URLSearchParams {
  params.set(PARAM_LAYERS, state.layers.join(","));
  if (state.vlanFilter !== undefined) {
    params.set(PARAM_VLAN, String(state.vlanFilter));
  } else {
    params.delete(PARAM_VLAN);
  }
  params.set(PARAM_ZOOM, String(state.zoom));
  params.set(PARAM_X, String(state.viewport.x));
  params.set(PARAM_Y, String(state.viewport.y));
  if (state.selection !== undefined) {
    params.set(PARAM_SEL, state.selection);
  } else {
    params.delete(PARAM_SEL);
  }
  params.set(PARAM_VIEW, state.view);
  return params;
}

/** Parses a query string (or an already-constructed URLSearchParams) back
 * into a SavedViewState. Returns undefined if the params carry none of
 * this feature's keys at all (nothing to restore — the normal case for
 * every URL that isn't a saved-view share link); a URL that carries
 * `svLayers` but has malformed/missing companions degrades field-by-field
 * to this module's own defaults rather than discarding the whole state,
 * so a hand-edited or partially-stale link still restores what it can. */
export function decodeViewFromSearch(search: string | URLSearchParams): SavedViewState | undefined {
  const params = typeof search === "string" ? new URLSearchParams(search) : search;
  if (!params.has(PARAM_LAYERS)) return undefined;

  const layersRaw = params.get(PARAM_LAYERS) ?? "";
  const layers = layersRaw
    .split(",")
    .map((l) => l.trim())
    .filter((l): l is Layer => (ALL_LAYERS as readonly string[]).includes(l));

  const vlanRaw = params.get(PARAM_VLAN);
  const vlanFilter = vlanRaw !== null && vlanRaw !== "" && Number.isFinite(Number(vlanRaw)) ? Number(vlanRaw) : undefined;

  const zoomRaw = Number(params.get(PARAM_ZOOM));
  const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : 1;

  const x = Number(params.get(PARAM_X));
  const y = Number(params.get(PARAM_Y));

  const selRaw = params.get(PARAM_SEL);
  const selection = selRaw !== null && selRaw !== "" ? selRaw : undefined;

  const viewRaw = params.get(PARAM_VIEW);
  const view = viewRaw === "switch" ? "switch" : "graph";

  return {
    layers: layers.length > 0 ? layers : Array.from(ALL_LAYERS),
    vlanFilter,
    zoom,
    viewport: { x: Number.isFinite(x) ? x : 0, y: Number.isFinite(y) ? y : 0 },
    selection,
    view,
  };
}
