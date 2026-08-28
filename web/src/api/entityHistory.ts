// SPDX-License-Identifier: Apache-2.0

// T-2403's entity change history: `GET /inventory/history?ref=…`.
//
// The ref travels as a QUERY parameter, not a path segment. Refs legitimately
// contain "/" (an SDN vnet path, a subnet CIDR) and ":", and `/inventory/*` is
// a wildcard route that would swallow a `{ref}/history` shape. Carrying it in
// the query means decoding is net/url's job on the server rather than a
// handler's — the exact step T-1304's guest-interior routes omitted, which made
// that feature return 400 to every browser request from the day it shipped.
import { apiFetch } from "./client";
import type { EntityHistoryPage } from "./types";

export function fetchEntityHistory(ref: string, limit?: number): Promise<EntityHistoryPage> {
  const params = new URLSearchParams({ ref });
  if (limit !== undefined) params.set("limit", String(limit));
  return apiFetch<EntityHistoryPage>(`/inventory/history?${params.toString()}`);
}
