// SPDX-License-Identifier: Apache-2.0

// T-3906's guest ego view: thin query composition over hooks/endpoints that
// already exist — this file adds no new backend route and wraps no new
// fetch of its own beyond what GET /flows/GET /conntrack already serve.
// See GuestEgoView.tsx's doc comment for the full reuse map.
import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { fetchFlows } from "../api/flows";
import { flowsQueryKey, useFlowsQuery } from "../flows/flowsQueries";
import { useConntrackQuery } from "../conntrack/conntrackQueries";
import type { ConntrackFilter } from "../api/conntrack";
import type { FlowRecord } from "../api/types";

/** A cheap, unfiltered `GET /flows?limit=1` probe — the same "no items from
 * the initial fetch" cluster-wide inference flows/FlowExplorer.tsx already
 * uses to detect "no flow source configured", reused here as its own tiny
 * query rather than duplicated so both surfaces answer from the identical
 * `GET /flows` contract. */
export function useClusterHasAnyFlowsProbe() {
  const { data, isLoading, isError } = useFlowsQuery({ limit: 1 });
  return {
    hasAny: data ? data.items.length > 0 : undefined,
    isLoading,
    isError,
  };
}

/** One `GET /flows?guest=<target>` query per resolved bridge/VNet target
 * this guest's NICs attach to, merged. GET /flows' own `guest=` filter only
 * ever narrows by ONE ref per request (guests/guestNics.ts's/api/flows.ts's
 * documented "guest matches either srcRef or dstRef" — always a Bridge or
 * SdnVnet ref, never a guest-nic-level match), so a multi-NIC guest
 * attached to more than one bridge/VNet needs one request per target — the
 * same `useQueries` fan-out guests/queries.ts's useAllGuestNicsQuery
 * already uses for expanding multiple collapsed guest-group pills. */
export function useGuestFlows(targets: readonly string[]) {
  const results = useQueries({
    queries: targets.map((t) => ({
      queryKey: flowsQueryKey({ guest: t, limit: 50 }),
      queryFn: () => fetchFlows({ guest: t, limit: 50 }),
      staleTime: 5_000,
    })),
  });

  const items = useMemo(() => {
    const merged: FlowRecord[] = results.flatMap((r) => r.data?.items ?? []);
    return merged.sort((a, b) => b.at - a.at);
    // results' array identity changes every render (useQueries); keying off
    // each query's own dataUpdatedAt avoids recomputing on every render
    // while still reacting to real data changes — same pattern
    // guests/queries.ts's useAllGuestNicsQuery already uses.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [results.map((r) => r.dataUpdatedAt).join(",")]);

  return {
    items,
    isLoading: targets.length > 0 && results.some((r) => r.isLoading),
    isError: results.some((r) => r.isError),
  };
}

/** `GET /conntrack?guest=<guestRef>` — the server resolves the guest ref to
 * its known IPs itself (internal/api/conntrack.go's `ConntrackGuestResolver`),
 * so this needs no IP of its own. */
export function useGuestConntrack(guestRef: string) {
  const filter: ConntrackFilter = { guest: guestRef };
  return useConntrackQuery(filter);
}
