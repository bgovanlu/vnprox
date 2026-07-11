// IPAM API calls (docs/features/ipam.md; internal/api/ipam.go's
// GET /ipam/subnets, GET /ipam/subnets/{cidr}/allocations).
import { API_BASE, apiFetch } from "./client";
import type { IpamAllocationGrid, IpamSubnetsResponse } from "./types";

/** GET /ipam/subnets: every SDN subnet plus detected non-SDN subnets, with
 * utilization counts. */
export function fetchIpamSubnets(): Promise<IpamSubnetsResponse> {
  return apiFetch<IpamSubnetsResponse>("/ipam/subnets");
}

/** GET /ipam/subnets/{cidr}/allocations[?block=]: the allocation grid (or,
 * for a subnet bigger than 256 addresses with no `block` given, the paged
 * block-summary view — docs/features/ipam.md §2). `cidr` is passed with its
 * literal '/' (the dev proxy and vnproxd both percent-decode the path
 * before routing, matching /inventory/{ref}'s Ref-with-slash convention —
 * see internal/api/ipam.go's mountIPAMRoutes doc comment). */
export function fetchIpamAllocations(cidr: string, block?: string): Promise<IpamAllocationGrid> {
  const query = block ? `?block=${encodeURIComponent(block)}` : "";
  return apiFetch<IpamAllocationGrid>(`/ipam/subnets/${cidr}/allocations${query}`);
}

/** The `?format=csv` export's direct download URL (docs/features/ipam.md
 * §3: "CSV export per subnet") — a plain same-origin GET, not a fetch
 * response, so the browser can drive the download itself (Content-
 * Disposition: attachment, set by internal/api/ipam.go's
 * handleIPAMAllocations). */
export function ipamAllocationsCsvUrl(cidr: string): string {
  return `${API_BASE}/ipam/subnets/${cidr}/allocations?format=csv`;
}
