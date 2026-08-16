// IPv6 segment visibility (docs/api.md's IPv6 section, T-1404;
// internal/api/ipv6.go's GET /ipv6/segments).
//
// netRead-gated rather than sdnRead: an RA/DHCPv6 observation is host-local
// diagnostic data, the same category GET /conntrack and GET /latmesh/heatmap
// sit in, not SDN configuration. Read-only — changing a segment's addressing
// is an ordinary `sdn.subnet.create` changeset (the dual-stack wizard).
import { apiFetch } from "./client";
import type { IPv6SegmentsView } from "./types";

/** GET /ipv6/segments — per-VLAN/VNet RA/SLAAC/DHCPv6 visibility, fanned
 * out across the cluster. `partial`/`failedNodes` carry the fan-out's own
 * honesty: one node's RA read failing never blanks every other node's, and
 * a caller must render the gap rather than the remainder alone. */
export function fetchIPv6Segments(): Promise<IPv6SegmentsView> {
  return apiFetch<IPv6SegmentsView>("/ipv6/segments");
}
