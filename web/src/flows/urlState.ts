// Shareable Flow Explorer state — the same "state lives in the URL"
// convention this codebase already established for the path simulator
// (simulator/urlState.ts) and the firewall log rule deep link
// (fwlog/deeplink.ts): a plain, framework-free encode/decode pair over
// URLSearchParams so it's testable without rendering anything, and a path
// builder the map's "view in Flow Explorer" drill-down link
// (topology/FlowPairPanel.tsx) and FlowExplorer.tsx's own "Copy link"
// affordance both go through — one mechanism, never two independently
// hand-rolled query strings.
import type { FlowFilterState } from "./reducer";
import type { FlowSortKey, FlowViewMode } from "./reducer";

export interface FlowExplorerUrlState {
  filter: FlowFilterState;
  sort: FlowSortKey;
  view: FlowViewMode;
  /** Set only by a map drill-down deep link (topology/FlowPairPanel.tsx):
   * narrows the (already guest-filtered) result set to exactly this one
   * src/dst pair client-side — GET /flows has no dual-ref filter, so
   * `filter.guest` alone would return every conversation touching either
   * endpoint, not just this specific pair (see FlowExplorer.tsx's
   * doc comment on how pairSrc/pairDst compose with the server filter). */
  pairSrc?: string;
  pairDst?: string;
}

const SORT_VALUES: readonly FlowSortKey[] = ["recency", "bytes", "packets"];
const VIEW_VALUES: readonly FlowViewMode[] = ["raw", "conversations"];

function isSortKey(v: string | null): v is FlowSortKey {
  return v !== null && (SORT_VALUES as readonly string[]).includes(v);
}

function isViewMode(v: string | null): v is FlowViewMode {
  return v !== null && (VIEW_VALUES as readonly string[]).includes(v);
}

/** Builds the query string for `state` — omits any field that isn't set,
 * mirroring encodeSimState's "partial state round-trips exactly as
 * partial" contract. */
export function encodeFlowExplorerState(state: FlowExplorerUrlState): URLSearchParams {
  const params = new URLSearchParams();
  if (state.filter.guest) params.set("guest", state.filter.guest);
  if (state.filter.vlan) params.set("vlan", state.filter.vlan);
  if (state.filter.subnet) params.set("subnet", state.filter.subnet);
  if (state.filter.port) params.set("port", state.filter.port);
  if (state.filter.protocol) params.set("protocol", state.filter.protocol);
  if (state.sort !== "recency") params.set("sort", state.sort);
  if (state.view !== "raw") params.set("view", state.view);
  if (state.pairSrc) params.set("pairSrc", state.pairSrc);
  if (state.pairDst) params.set("pairDst", state.pairDst);
  return params;
}

/** Parses a query string (or an already-constructed URLSearchParams) back
 * into a FlowExplorerUrlState. Every field degrades to its default on a
 * missing/malformed value — a corrupted/hand-edited URL never throws. */
export function decodeFlowExplorerState(search: string | URLSearchParams): FlowExplorerUrlState {
  const params = typeof search === "string" ? new URLSearchParams(search) : search;
  return {
    filter: {
      guest: params.get("guest") ?? "",
      vlan: params.get("vlan") ?? "",
      subnet: params.get("subnet") ?? "",
      port: params.get("port") ?? "",
      protocol: params.get("protocol") ?? "",
    },
    sort: isSortKey(params.get("sort")) ? (params.get("sort") as FlowSortKey) : "recency",
    view: isViewMode(params.get("view")) ? (params.get("view") as FlowViewMode) : "raw",
    pairSrc: params.get("pairSrc") ?? undefined,
    pairDst: params.get("pairDst") ?? undefined,
  };
}

/** Builds the map's "view in Flow Explorer" deep link for one src/dst
 * conversation pair (topology/FlowPairPanel.tsx): narrows the server-side
 * `guest` filter to `srcRef` (both endpoints of a resolved flow edge are
 * always inventory refs — flowEdges.ts only ever builds edges from
 * resolved records) and carries `pairDst` for FlowExplorer's own exact-pair
 * client-side narrowing. */
export function flowPairExplorerPath(basePath: string, srcRef: string, dstRef: string): string {
  const qs = encodeFlowExplorerState({
    filter: { ...{ guest: srcRef, vlan: "", subnet: "", port: "", protocol: "" } },
    sort: "recency",
    view: "raw",
    pairSrc: srcRef,
    pairDst: dstRef,
  }).toString();
  return qs ? `${basePath}?${qs}` : basePath;
}
