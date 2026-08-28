// SPDX-License-Identifier: Apache-2.0

// Neighbor binding timeline API calls (docs/api.md's "Neighbor binding
// history" section; internal/api/neighborhistory.go, T-3905). Read-only —
// this feature stages and applies nothing.
import { apiFetch } from "./client";
import type { NeighborHistoryPage } from "./types";

/** Filter params GET /neighbors/history accepts — every field optional and
 * ANDed together server-side, mirroring GET /flows' convention. `node`
 * narrows the already cluster-merged result to one node's own
 * contribution. */
export interface NeighborHistoryFilter {
  ip?: string;
  mac?: string;
  node?: string;
  fromTs?: number;
  toTs?: number;
  limit?: number;
  cursor?: string;
}

/** GET /neighbors/history?ip=&mac=&node=&fromTs=&toTs=&limit=&cursor= —
 * paginated, cluster-wide IP<->MAC binding transition history. */
export function fetchNeighborHistory(filter: NeighborHistoryFilter = {}): Promise<NeighborHistoryPage> {
  const params = new URLSearchParams();
  if (filter.ip) params.set("ip", filter.ip);
  if (filter.mac) params.set("mac", filter.mac);
  if (filter.node) params.set("node", filter.node);
  if (filter.fromTs !== undefined) params.set("fromTs", String(filter.fromTs));
  if (filter.toTs !== undefined) params.set("toTs", String(filter.toTs));
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.cursor) params.set("cursor", filter.cursor);
  const qs = params.toString();
  return apiFetch<NeighborHistoryPage>(`/neighbors/history${qs ? `?${qs}` : ""}`);
}
