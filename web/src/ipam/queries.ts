// TanStack Query hooks for IPAM (docs/development.md's TypeScript
// standards: "server state via TanStack Query only — no fetch in
// components").
import { useQuery } from "@tanstack/react-query";
import { fetchIpamAllocations, fetchIpamSubnets } from "../api/ipam";
import type { IpamAllocationList, IpamSubnetsResponse } from "../api/types";

export const IPAM_SUBNETS_QUERY_KEY = ["ipam", "subnets"] as const;

export function useIpamSubnetsQuery() {
  return useQuery<IpamSubnetsResponse>({
    queryKey: IPAM_SUBNETS_QUERY_KEY,
    queryFn: fetchIpamSubnets,
    staleTime: 10_000,
  });
}

export function ipamAllocationsQueryKey(cidr: string): readonly unknown[] {
  return ["ipam", "allocations", cidr] as const;
}

export function useIpamAllocationsQuery(cidr: string | undefined) {
  return useQuery<IpamAllocationList>({
    queryKey: ipamAllocationsQueryKey(cidr ?? ""),
    queryFn: () => {
      if (!cidr) {
        return Promise.reject(new Error("useIpamAllocationsQuery: cidr is required"));
      }
      return fetchIpamAllocations(cidr);
    },
    enabled: cidr !== undefined && cidr !== "",
    staleTime: 5_000,
  });
}
