// SPDX-License-Identifier: Apache-2.0

// Topology & inventory API calls (docs/api.md §Inventory & topology; the
// exact JSON contract is internal/topology/types.go + internal/api/topology.go).
import { apiFetch } from "./client";
import type { EntityDetail, SearchResponse, TopologyFilter, TopologyResponse } from "./types";

function buildTopologyQuery(filter: TopologyFilter): string {
  const params = new URLSearchParams();
  if (filter.layers && filter.layers.length > 0) {
    params.set("layers", filter.layers.join(","));
  }
  if (filter.node) {
    params.set("node", filter.node);
  }
  if (filter.vlan !== undefined && filter.vlan > 0) {
    params.set("vlan", String(filter.vlan));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

/** GET /topology — full projected topology, optionally server-side filtered.
 * See docs/features/topology.md §3: the frontend owns layout, the backend
 * owns structure and status. */
export function fetchTopology(filter: TopologyFilter = {}): Promise<TopologyResponse> {
  return apiFetch<TopologyResponse>(`/topology${buildTopologyQuery(filter)}`);
}

/** GET /inventory/{ref} — full detail for one entity: normalized fields,
 * per-field provenance, per-source raw source text (docs/api.md's
 * `rawSource`), and related entities. `ref` is a Ref triplet
 * string ("kind:node:id"); it may contain '/' and ':' verbatim (see
 * inventory.Ref.String's doc comment) so it's percent-encoded as a whole
 * path segment here — the server's wildcard route and net/http's path
 * decoding recover it exactly (internal/api/topology.go's doc comment on
 * mountTopologyRoutes explains why a plain '/' would also work, but
 * percent-encoding is the least surprising choice for a URL builder). */
export function fetchInventoryDetail(ref: string): Promise<EntityDetail> {
  return apiFetch<EntityDetail>(`/inventory/${encodeURIComponent(ref)}`);
}

/** GET /inventory/search?q= — fuzzy search across names, MACs, IPs, VMIDs,
 * comments; ranked, capped at 50 server-side. Returns an empty result set
 * (rather than calling the API) for a blank query so callers don't need to
 * special-case "nothing typed yet" themselves. */
export function searchInventory(q: string): Promise<SearchResponse> {
  const trimmed = q.trim();
  if (!trimmed) {
    return Promise.resolve({ results: [] });
  }
  return apiFetch<SearchResponse>(`/inventory/search?q=${encodeURIComponent(trimmed)}`);
}
