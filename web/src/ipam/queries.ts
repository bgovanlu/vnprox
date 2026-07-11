// TanStack Query hooks for IPAM (docs/development.md's TypeScript
// standards: "server state via TanStack Query only — no fetch in
// components").
import { useQuery } from "@tanstack/react-query";
import { fetchIpamAllocations, fetchIpamSubnets } from "../api/ipam";
import type { IpamAllocationGrid, IpamSubnetsResponse } from "../api/types";

export const IPAM_SUBNETS_QUERY_KEY = ["ipam", "subnets"] as const;

export function useIpamSubnetsQuery() {
  return useQuery<IpamSubnetsResponse>({
    queryKey: IPAM_SUBNETS_QUERY_KEY,
    queryFn: fetchIpamSubnets,
    staleTime: 10_000,
  });
}

export function ipamAllocationsQueryKey(cidr: string, block?: string): readonly unknown[] {
  return ["ipam", "allocations", cidr, block ?? null] as const;
}

export function useIpamAllocationsQuery(cidr: string | undefined, block?: string) {
  return useQuery<IpamAllocationGrid>({
    queryKey: ipamAllocationsQueryKey(cidr ?? "", block),
    queryFn: () => {
      if (!cidr) {
        return Promise.reject(new Error("useIpamAllocationsQuery: cidr is required"));
      }
      return fetchIpamAllocations(cidr, block);
    },
    enabled: cidr !== undefined && cidr !== "",
    staleTime: 5_000,
  });
}
