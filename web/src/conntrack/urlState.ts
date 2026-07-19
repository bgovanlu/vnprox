// Shareable Conntrack Explorer state — the same "state lives in the URL"
// convention this codebase already established for the Flow Explorer
// (flows/urlState.ts): a plain, framework-free encode/decode pair over
// URLSearchParams, so a link into this page (the map's right-click "View
// live connections" entry, the Flow Explorer's own "View live connections"
// drill-down) reproduces the exact same filtered view, and it's testable
// without rendering anything.
import type { ConntrackFilter } from "../api/conntrack";

export interface ConntrackExplorerUrlState {
  filter: ConntrackFilter;
}

/** Builds the query string for filter — omits any field that isn't set. */
export function encodeConntrackExplorerState(filter: ConntrackFilter): URLSearchParams {
  const params = new URLSearchParams();
  if (filter.node) params.set("node", filter.node);
  if (filter.guest) params.set("guest", filter.guest);
  if (filter.srcIp) params.set("srcIp", filter.srcIp);
  if (filter.dstIp) params.set("dstIp", filter.dstIp);
  if (filter.port !== undefined) params.set("port", String(filter.port));
  if (filter.state) params.set("state", filter.state);
  return params;
}

/** Parses a query string (or an already-constructed URLSearchParams) back
 * into a ConntrackFilter. Every field degrades to "unset" on a
 * missing/malformed value — a corrupted/hand-edited URL never throws. */
export function decodeConntrackExplorerState(search: string | URLSearchParams): ConntrackExplorerUrlState {
  const params = typeof search === "string" ? new URLSearchParams(search) : search;
  const portRaw = params.get("port");
  const port = portRaw !== null && Number.isFinite(Number(portRaw)) ? Number(portRaw) : undefined;
  return {
    filter: {
      node: params.get("node") ?? undefined,
      guest: params.get("guest") ?? undefined,
      srcIp: params.get("srcIp") ?? undefined,
      dstIp: params.get("dstIp") ?? undefined,
      port,
      state: params.get("state") ?? undefined,
    },
  };
}

/** Builds a deep link into the Conntrack Explorer scoped to one cluster
 * node — the map's right-click "View live connections" entry on a
 * bridge/bond/guest-NIC and the Flow Explorer's own pair drill-down both
 * go through this (see ConntrackExplorer.tsx's/FlowPairPanel.tsx's own doc
 * comments on why node-scoping, not exact IP/guest matching, is the honest
 * link this codebase can build from those call sites today: a flow
 * conversation's endpoints are resolved Bridge/SdnVnet refs, never a
 * conntrack-matchable IP or a Guest ref — GET /flows' own documented
 * "never guessed" gap). */
export function conntrackNodeLinkPath(basePath: string, node: string | undefined): string {
  if (!node) return basePath;
  const qs = encodeConntrackExplorerState({ node }).toString();
  return qs ? `${basePath}?${qs}` : basePath;
}
