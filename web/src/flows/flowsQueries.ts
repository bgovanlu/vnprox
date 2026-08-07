// TanStack Query hook (initial page) + WS bridge (live follow) for T-1003's
// Flow Explorer / map painting — the same "REST for the initial snapshot,
// WS for live increments over the one shared /api/ws connection" split
// topology/queries.ts's useTopologyWsBridge and fwlog/queries.ts's
// useFwLogWsBridge both already establish.
import { useEffect, useReducer, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchFlows, type FlowsFilter } from "../api/flows";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { FlowBatchEvent, FlowRecord, FlowsPage } from "../api/types";
import { flowReducer, initialFlowViewState } from "./reducer";

export function flowsQueryKey(filter: FlowsFilter) {
  return ["flows", filter] as const;
}

/** GET /flows?guest=&vlan=&subnet=&port=&protocol=&fromTs=&toTs=&limit=&cursor= —
 * always a fresh fetch on mount/filter change, matching useFwLogQuery's
 * "this is a live view, not cacheable app state" reasoning. `enabled`
 * (default true) skips the fetch entirely — the map's Flows layer passes
 * `false` while the layer is toggled off. */
export function useFlowsQuery(filter: FlowsFilter, enabled = true) {
  return useQuery<FlowsPage>({
    queryKey: flowsQueryKey(filter),
    queryFn: () => fetchFlows(filter),
    staleTime: 0,
    enabled,
  });
}

/** Runtime guard for the `flow.batch` payload (docs/api.md's WebSocket
 * section) — same pattern as fwlog/queries.ts's isFwLogBatchEvent /
 * topology/queries.ts's isTopologyDeltaEvent. Exported for direct unit
 * testing. */
export function isFlowBatchEvent(evt: WsServerEvent): evt is WsServerEvent & FlowBatchEvent {
  return evt.event === "flow.batch" && Array.isArray(evt.entries) && typeof evt.droppedTotal === "number";
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/** Subscribes to the `/api/ws` "flows" topic for the component's lifetime,
 * calling onBatch for every `flow.batch` push — callers (FlowExplorer's
 * reducer, the map's live-edges buffer) decide how to merge/filter/cap the
 * incoming records, exactly like useFwLogWsBridge leaves merging to
 * FwLogViewer's reducer. Pass `client` in tests to inject a client wired to
 * a real `ws` test server instead of the shared browser singleton.
 * `enabled` (default true) fully unsubscribes when false — the map's Flows
 * layer (useLiveFlowRecords below) uses this so it only streams flow
 * records while the layer is actually toggled on, mirroring
 * useLiveMetrics' "only subscribes... while the mode is on" convention.
 * onBatch is read through a ref so callers can pass an inline closure
 * without resubscribing every render. */
export function useFlowsWsBridge(onBatch: (evt: FlowBatchEvent) => void, client?: WsClient, enabled = true): void {
  const onBatchRef = useRef(onBatch);
  onBatchRef.current = onBatch;

  useEffect(() => {
    if (!enabled) return;
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["flows"], (evt) => {
      if (isFlowBatchEvent(evt)) {
        onBatchRef.current(evt);
      }
    });
    return unsubscribe;
  }, [client, enabled]);
}

/** The map's own live, unfiltered flow-record buffer (topology/
 * TopologyPage.tsx's Flows layer): the initial unfiltered GET /flows page
 * merged with every subsequent `flow.batch` push, bounded exactly like
 * FlowExplorer's own reducer — a wholly independent buffer/subscription
 * from any FlowExplorer instance that might also be mounted (each caller
 * of this hook gets its own useReducer + WS subscription; the underlying
 * `/api/ws` connection is still the one shared singleton, per
 * getSharedWsClient). `limit` defaults higher than FlowExplorer's own
 * per-fetch default (100) since the map wants enough of the current
 * window to paint every active conversation, not just a first page. */
export interface LiveFlowRecordsResult {
  records: FlowRecord[];
  isLoading: boolean;
}

/** The single empty buffer handed back while the Flows layer is off.
 *
 * This MUST be a module-level constant, not a `[]` literal in the return
 * below. A fresh literal is a new identity on every render, and this hook's
 * result feeds `HistoryTimeline`'s `liveFlowRecords` prop, which is a
 * dependency of the effect that calls `onPlaybackChange` -> `setPlayback` in
 * TopologyPage. New identity every render => that effect re-fires every
 * render => setState => render => new identity: an unbreakable render loop
 * that ran for the whole time the Graph view was mounted (T-2003-bug-01).
 *
 * The visible symptom was not a slow map. React Router v7 wraps location
 * updates in `startTransition`, and a transition can never commit while the
 * tree it is rendering re-invalidates itself every frame — so clicking any
 * nav-rail link changed the URL and left the Topology page on screen
 * forever. See web/e2e/nav-after-inspector.spec.ts. */
const NO_FLOW_RECORDS: FlowRecord[] = [];

export function useLiveFlowRecords(enabled: boolean, limit = 500, client?: WsClient): LiveFlowRecordsResult {
  const [state, dispatch] = useReducer(flowReducer, initialFlowViewState);
  const { data, isLoading } = useFlowsQuery({ limit }, enabled);

  useEffect(() => {
    if (enabled && data) dispatch({ type: "loaded", items: data.items });
  }, [data, enabled]);

  useFlowsWsBridge(
    (evt) => {
      dispatch({ type: "batch", entries: evt.entries, droppedTotal: evt.droppedTotal });
    },
    client,
    enabled,
  );

  if (!enabled) return { records: NO_FLOW_RECORDS, isLoading: false };
  return { records: state.records, isLoading };
}
