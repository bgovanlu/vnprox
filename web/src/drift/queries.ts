// TanStack Query hooks for T-305's drift findings: the list fetch, the
// "create fixing changeset" mutation, and the WS bridge that keeps the
// list fresh on `drift.changed` — the same "targeted invalidation on a WS
// event" pattern topology/queries.ts's useTopologyWsBridge established.
import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { fetchDrift, fixDriftFinding } from "../api/drift";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { DriftChangedEvent } from "../api/types";

export const DRIFT_QUERY_KEY = ["drift"] as const;

export function useDriftQuery() {
  return useQuery({
    queryKey: DRIFT_QUERY_KEY,
    queryFn: fetchDrift,
    staleTime: 15_000,
  });
}

export function useFixDriftMutation() {
  return useMutation({
    mutationFn: (id: string) => fixDriftFinding(id),
  });
}

/** Runtime guard for the `drift.changed` payload (docs/api.md: `{count}`).
 * See topology/queries.ts's isTopologyDeltaEvent for why the narrowed type
 * is the `WsServerEvent &` intersection rather than plain DriftChangedEvent.
 * Exported for direct unit testing. */
export function isDriftChangedEvent(evt: WsServerEvent): evt is WsServerEvent & DriftChangedEvent {
  return evt.event === "drift.changed" && typeof evt.count === "number";
}

/** Applies one drift.changed event to the query cache: a targeted refetch
 * of the drift list (docs/api.md's WS section's own convention — see
 * applyTopologyDelta's doc comment for the same reasoning). Exported
 * standalone so it's directly unit-testable against a plain QueryClient. */
export function applyDriftChanged(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: DRIFT_QUERY_KEY });
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/** Subscribes to the `/api/ws` "drift" topic for the component's lifetime
 * and invalidates the drift query on every `drift.changed` push. Pass
 * `client` in tests to inject a client wired to a real `ws` test server
 * instead of the shared browser singleton (see topology/queries.ts's
 * useTopologyWsBridge, the same pattern). Subscribing to "drift" is
 * additive to whatever other topics (e.g. "topology") other components
 * already subscribe to on the same shared connection — WsClient unions
 * every active subscriber's topics into one `{"subscribe":[...]}` message
 * (see api/ws.ts's currentTopics), so this never clobbers them. */
export function useDriftWsBridge(client?: WsClient): void {
  const queryClient = useQueryClient();
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  useEffect(() => {
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["drift"], (evt) => {
      if (isDriftChangedEvent(evt)) {
        applyDriftChanged(queryClientRef.current);
      }
    });
    return unsubscribe;
  }, [client]);
}
