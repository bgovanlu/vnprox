// TanStack Query hook for GET /ipv6/segments (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch in
// components").
//
// An RA observation is read fresh on every request — internal/ipv6 keeps no
// history or flap state — and each read solicits a router advertisement per
// bridge/VLAN interface on every cluster node with a per-interface timeout.
// That is genuinely expensive, so this polls slowly and is left to the
// panel's own mount/unmount to stop.
import { useQuery } from "@tanstack/react-query";
import { fetchIPv6Segments } from "../api/ipv6";
import type { IPv6Segment, IPv6SegmentsView } from "../api/types";

export const IPV6_SEGMENTS_QUERY_KEY = ["ipv6", "segments"] as const;

/** Slower than any other analysis read in this app: each refetch is a
 * cluster-wide fan-out of one `rdisc6` solicit per bridge/VLAN interface. */
export const IPV6_SEGMENTS_REFETCH_MS = 120_000;

export function useIPv6SegmentsQuery() {
  return useQuery<IPv6SegmentsView>({
    queryKey: IPV6_SEGMENTS_QUERY_KEY,
    queryFn: fetchIPv6Segments,
    refetchInterval: IPV6_SEGMENTS_REFETCH_MS,
  });
}

/** Every segment observed on a given VNet, across every node.
 *
 * Note what an empty result does and does not mean. `GET /ipv6/segments`
 * carries one entry per interface where an RA was **actually observed**
 * (internal/host's IPv6RA skips an interface whose solicit times out), so a
 * VNet with no entry is a VNet with no router advertisement — which is
 * exactly the state the dual-stack wizard exists to change. It is never
 * evidence that the VNet does not exist. */
export function segmentsForVnet(segments: readonly IPv6Segment[], vnet: string): IPv6Segment[] {
  return segments.filter((s) => s.vnet === vnet);
}
