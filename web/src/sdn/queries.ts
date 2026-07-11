// TanStack Query hook for the SDN cockpit (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch
// in components").
import { useQuery } from "@tanstack/react-query";
import { fetchSdnTree } from "../api/sdn";
import type { SdnTree } from "../api/types";

export const SDN_QUERY_KEY = ["sdn"] as const;

export function useSdnQuery() {
  return useQuery<SdnTree>({
    queryKey: SDN_QUERY_KEY,
    queryFn: fetchSdnTree,
    staleTime: 15_000,
  });
}
