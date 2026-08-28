// SPDX-License-Identifier: Apache-2.0

// Multicast/MDB browser API calls (docs/api.md's Multicast/MDB section;
// internal/api/mdb.go's GET /mdb, T-3902). Sibling of fdb.ts, but a live
// cluster-wide fan-out (like conntrack.ts's GET /conntrack), not an
// inventory-backed search — see mdb.go's/types.ts's doc comments for why.
import { apiFetch } from "./client";
import type { MDBResponse } from "./types";

/** Filter params GET /mdb accepts — both optional and ANDed together,
 * mirroring GET /conntrack's convention. */
export interface MDBFilter {
  node?: string;
  group?: string;
}

/** GET /mdb?node=&group= — a live, cluster-fanned-out bridge MDB read plus
 * per-bridge IGMP/MLD-snooping config. */
export function fetchMDB(filter: MDBFilter = {}): Promise<MDBResponse> {
  const params = new URLSearchParams();
  if (filter.node) params.set("node", filter.node);
  if (filter.group) params.set("group", filter.group);
  const qs = params.toString();
  return apiFetch<MDBResponse>(`/mdb${qs ? `?${qs}` : ""}`);
}
