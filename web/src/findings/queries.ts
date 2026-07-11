// TanStack Query hooks for T-602's unified findings stream: the list fetch,
// the "create fixing changeset" mutation, and the WS bridge that keeps the
// list fresh on `findings.changed` — mirrors drift/queries.ts's own
// pattern (the same "targeted invalidation on a WS event" convention
// topology/queries.ts established) one-for-one, generalized across every
// producer.
import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { fetchFindings, fixFinding } from "../api/findings";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { FindingsChangedEvent } from "../api/types";

export const FINDINGS_QUERY_KEY = ["findings"] as const;

/** Fetches the full unified stream (no server-side filter — filtering
 * happens client-side via findings/filters.ts, so switching filters never
 * triggers a refetch). */
export function useFindingsQuery() {
  return useQuery({
    queryKey: FINDINGS_QUERY_KEY,
    queryFn: () => fetchFindings(),
    staleTime: 15_000,
  });
}

export function useFixFindingMutation() {
  return useMutation({
    mutationFn: (id: string) => fixFinding(id),
  });
}

/** Runtime guard for the `findings.changed` payload (docs/api.md:
 * `{count}`) — see drift/queries.ts's isDriftChangedEvent for why the
 * narrowed type is the `WsServerEvent &` intersection. */
export function isFindingsChangedEvent(evt: WsServerEvent): evt is WsServerEvent & FindingsChangedEvent {
  return evt.event === "findings.changed" && typeof evt.count === "number";
}

/** Applies one findings.changed event to the query cache: a targeted
 * refetch of the findings list. */
export function applyFindingsChanged(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: FINDINGS_QUERY_KEY });
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/** Subscribes to the `/api/ws` "findings" topic for the component's
 * lifetime and invalidates the findings query on every `findings.changed`
 * push. Pass `client` in tests to inject a client wired to a real `ws` test
 * server instead of the shared browser singleton. */
export function useFindingsWsBridge(client?: WsClient): void {
  const queryClient = useQueryClient();
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  useEffect(() => {
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["findings"], (evt) => {
      if (isFindingsChangedEvent(evt)) {
        applyFindingsChanged(queryClientRef.current);
      }
    });
    return unsubscribe;
  }, [client]);
}
