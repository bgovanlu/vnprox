// SPDX-License-Identifier: Apache-2.0

// IPAM API calls (docs/features/ipam.md; internal/api/ipam.go's
// GET /ipam/subnets, GET /ipam/subnets/{cidr}/allocations).
import { API_BASE, apiFetch } from "./client";
import type { IpamAllocationList, IpamSubnetsResponse } from "./types";

/** GET /ipam/subnets: every SDN subnet plus detected non-SDN subnets, with
 * utilization counts. */
export function fetchIpamSubnets(): Promise<IpamSubnetsResponse> {
  return apiFetch<IpamSubnetsResponse>("/ipam/subnets");
}

/** GET /ipam/subnets/{cidr}/allocations: the address list — occupied entries
 * plus collapsed free ranges (docs/features/ipam.md §2), for the whole
 * subnet at any size. `cidr` is passed with its literal '/' (the dev proxy
 * and vnproxd both percent-decode the path before routing, matching
 * /inventory/{ref}'s Ref-with-slash convention — see internal/api/ipam.go's
 * mountIPAMRoutes doc comment). */
export function fetchIpamAllocations(cidr: string): Promise<IpamAllocationList> {
  return apiFetch<IpamAllocationList>(`/ipam/subnets/${cidr}/allocations`);
}

/** The `?format=csv` export's direct download URL (docs/features/ipam.md
 * §3: "CSV export per subnet") — a plain same-origin GET, not a fetch
 * response, so the browser can drive the download itself (Content-
 * Disposition: attachment, set by internal/api/ipam.go's
 * handleIPAMAllocations). */
export function ipamAllocationsCsvUrl(cidr: string): string {
  return `${API_BASE}/ipam/subnets/${cidr}/allocations?format=csv`;
}
