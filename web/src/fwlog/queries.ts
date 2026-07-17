// TanStack Query hook (initial page) + WS bridge (follow mode) for T-505's
// firewall log viewer — the same "REST for the initial snapshot, WS for
// live increments" split topology/queries.ts and drift/queries.ts already
// establish, reusing the one shared /api/ws connection (createWsClient).
import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchFwAnalytics, fetchFwLog, type FwAnalyticsFilter, type FwLogFilter } from "../api/fwlog";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { FwAnalyticsResponse, FwLogBatchEvent, FwLogPage } from "../api/types";

export function useFwLogQuery(filter: FwLogFilter) {
  return useQuery<FwLogPage>({
    queryKey: ["firewall-log", filter],
    queryFn: () => fetchFwLog(filter),
    staleTime: 0, // always a fresh tail on mount/filter change — this is a live view, not cacheable app state
  });
}

/** T-1006's analytics tab query: GET /firewall/analytics, re-fetched
 * whenever `filter` changes (window/scope selection). Unlike the log
 * viewer's own useFwLogQuery, this has no WS-follow half — analytics is a
 * point-in-time aggregate a user explicitly refreshes/re-windows, not a
 * live-appending stream. */
export function useFwAnalyticsQuery(filter: FwAnalyticsFilter) {
  return useQuery<FwAnalyticsResponse>({
    queryKey: ["firewall-analytics", filter],
    queryFn: () => fetchFwAnalytics(filter),
  });
}

/** Runtime guard for the `firewall.log.batch` payload (docs/api.md's
 * WebSocket section) — same pattern as topology/queries.ts's
 * isTopologyDeltaEvent / drift/queries.ts's isDriftChangedEvent. Exported
 * for direct unit testing. */
export function isFwLogBatchEvent(evt: WsServerEvent): evt is WsServerEvent & FwLogBatchEvent {
  return evt.event === "firewall.log.batch" && Array.isArray(evt.entries) && typeof evt.droppedTotal === "number";
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/** Subscribes to the `/api/ws` "firewall.log" topic for the component's
 * lifetime, calling onBatch for every `firewall.log.batch` push. Pass
 * `client` in tests to inject a client wired to a real `ws` test server
 * instead of the shared browser singleton (topology/queries.ts's
 * useTopologyWsBridge established this same injection seam). onBatch is
 * read through a ref so callers can pass an inline closure without
 * resubscribing every render. */
export function useFwLogWsBridge(onBatch: (evt: FwLogBatchEvent) => void, client?: WsClient): void {
  const onBatchRef = useRef(onBatch);
  onBatchRef.current = onBatch;

  useEffect(() => {
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["firewall.log"], (evt) => {
      if (isFwLogBatchEvent(evt)) {
        onBatchRef.current(evt);
      }
    });
    return unsubscribe;
  }, [client]);
}
