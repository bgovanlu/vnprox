// SPDX-License-Identifier: Apache-2.0

// Polls GET /snmp/counters while the "Switch counters" layer is active
// (T-4013) — like mtuProbeQueries.ts's poll, there is no WS event for this
// data (docs/api.md's "Switch counters (SNMP)" section documents no such
// push), so a plain refetch-interval poll is the whole freshness
// mechanism.
import { useQuery } from "@tanstack/react-query";
import { fetchSNMPCounterResults } from "../api/ifcounters";
import type { SNMPCounterResult } from "../api/types";

export const snmpCounterResultsKey = ["snmp-counter-results"] as const;

/** REFETCH_MS mirrors internal/ifcounters.DefaultPollIntervalSec (60s) —
 * polling faster than the server itself produces new readings would just
 * refetch the same data, the same reasoning mtuProbeQueries.ts's own
 * REFETCH_MS documents. */
const REFETCH_MS = 60_000;

export function useSNMPCounterResultsQuery(enabled: boolean): { data: SNMPCounterResult[] | undefined; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: snmpCounterResultsKey,
    queryFn: fetchSNMPCounterResults,
    enabled,
    staleTime: 30_000,
    refetchInterval: enabled ? REFETCH_MS : false,
  });
  return { data, isLoading };
}
