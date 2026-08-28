// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { fetchPorts } from "../api/lldp";

export const PORTS_QUERY_KEY = ["ports"] as const;

/** The flat ports table (GET /ports). Refetches on the same cadence as the
 * rest of the discovery data — LLDP neighbors change on the order of cable
 * moves, so a modest refresh interval is plenty. */
export function usePortsQuery() {
  return useQuery({
    queryKey: PORTS_QUERY_KEY,
    queryFn: fetchPorts,
    refetchInterval: 30_000,
  });
}
