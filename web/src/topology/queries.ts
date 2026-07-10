// TanStack Query hooks for the topology page: the full topology fetch, one
// entity's detail, spotlight search, guest-group expansion, saved-layout
// persistence, and the WS bridge that keeps the topology query fresh on
// `topology.delta` (docs/api.md: "client refetches affected" — see this
// file's useTopologyWsBridge doc comment for why a targeted refetch, not a
// hand-rolled cache patch, is the correct reading of that contract).
import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { fetchInventoryDetail, fetchTopology, searchInventory } from "../api/topology";
import { fetchLayout, saveLayout } from "../api/layouts";
import { ApiError } from "../api/client";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { EntityDetail, SearchResult, TopologyDeltaEvent, TopologyLayoutPayload, TopologyResponse } from "../api/types";
import { expandGuestGroup } from "./expand";
import { isGuestGroupId } from "./projection";

export const TOPOLOGY_QUERY_KEY = ["topology"] as const;
export const inventoryDetailKey = (ref: string) => ["inventory", ref] as const;
export const searchKey = (q: string) => ["inventory-search", q] as const;
export const guestGroupExpandKey = (groupId: string) => ["guest-group-expand", groupId] as const;
export const LAYOUT_NAME = "topology";
export const layoutKey = (name: string) => ["layouts", name] as const;

/** Always fetches the complete, unfiltered topology (no `?layers=`/`?vlan=`
 * query params). Layer visibility and VLAN dimming are both client-side
 * concerns (see projection.ts) applied on top of this one cached response —
 * deliberately, since the data is already fetched and re-requesting it on
 * every filter toggle would cost a round trip for no benefit the client
 * can't already compute itself. */
export function useTopologyQuery() {
  return useQuery<TopologyResponse>({
    queryKey: TOPOLOGY_QUERY_KEY,
    queryFn: () => fetchTopology({}),
    staleTime: 15_000,
  });
}

/** ref may be a guest-group synthetic id (not a real inventory ref) or
 * undefined (nothing selected); the query is simply disabled in both cases
 * — callers must route a guest-group click to expand/collapse instead of
 * the inspector (see TopologyPage). */
export function useInventoryDetailQuery(ref: string | undefined) {
  return useQuery<EntityDetail>({
    queryKey: inventoryDetailKey(ref ?? ""),
    queryFn: () => fetchInventoryDetail(ref ?? ""),
    enabled: ref !== undefined && !isGuestGroupId(ref),
    staleTime: 10_000,
  });
}

export function useSearchQuery(query: string) {
  const trimmed = query.trim();
  return useQuery<{ results: SearchResult[] }>({
    queryKey: searchKey(trimmed),
    queryFn: () => searchInventory(trimmed),
    enabled: trimmed.length > 0,
    staleTime: 5_000,
  });
}

/** Expands one guest-group pill (see expand.ts's doc comment for why this
 * needs a follow-up API call at all, and why /inventory/{targetRef} is
 * enough — no backend change required). Disabled unless the group is
 * actually toggled open, so collapsed pills never trigger extra fetches. */
export function useGuestGroupExpandQuery(groupId: string, enabled: boolean) {
  return useQuery({
    queryKey: guestGroupExpandKey(groupId),
    queryFn: () => expandGuestGroup(groupId, { fetchDetail: fetchInventoryDetail }),
    enabled,
    staleTime: 15_000,
  });
}

/** GET /layouts/{name} — 404 (no saved layout yet) is treated as "no data",
 * not an error the UI should surface, per fetchLayout's doc comment. */
export function useLayoutQuery(name: string = LAYOUT_NAME) {
  return useQuery<TopologyLayoutPayload | undefined>({
    queryKey: layoutKey(name),
    queryFn: async () => {
      try {
        const res = await fetchLayout(name);
        return res.layout;
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          return undefined;
        }
        throw err;
      }
    },
    staleTime: Infinity,
    retry: false,
  });
}

export function useSaveLayoutMutation(name: string = LAYOUT_NAME) {
  return useMutation({
    mutationFn: (payload: TopologyLayoutPayload) => saveLayout(name, payload),
  });
}

function isStringArray(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((item) => typeof item === "string");
}

/**
 * Runtime guard for the full `topology.delta` payload shape (docs/api.md's
 * WebSocket section: `{added, updated, removed: [Ref]}`) — every element of
 * all three arrays is verified to be a string, mirroring how client.ts's
 * isErrorEnvelope validates the error envelope rather than trusting the
 * wire. The predicate narrows to `WsServerEvent & TopologyDeltaEvent`
 * (rather than plain TopologyDeltaEvent) because TypeScript requires a
 * predicate's narrowed type to be assignable to the parameter type, and a
 * specific interface without an index signature never is to WsServerEvent's
 * `[key: string]: unknown` — the intersection satisfies both sides with no
 * cast anywhere. Exported for direct unit testing (queries.test.tsx).
 */
export function isTopologyDeltaEvent(evt: WsServerEvent): evt is WsServerEvent & TopologyDeltaEvent {
  return (
    evt.event === "topology.delta" &&
    isStringArray(evt.added) &&
    isStringArray(evt.updated) &&
    isStringArray(evt.removed)
  );
}

/** Applies one topology.delta event to the query cache: invalidates the
 * topology list query (targeted refetch — docs/api.md: "client refetches
 * affected" — rather than a full page reload) plus any open inventory-
 * detail queries for entities the delta named, and drops detail queries for
 * removed refs outright so a stale inspector panel can't linger on a
 * deleted entity. Exported standalone (not just inlined in the hook below)
 * so it's directly unit-testable against a plain QueryClient without
 * standing up a WS connection at all. */
export function applyTopologyDelta(queryClient: QueryClient, evt: TopologyDeltaEvent): void {
  void queryClient.invalidateQueries({ queryKey: TOPOLOGY_QUERY_KEY });
  for (const ref of [...evt.added, ...evt.updated]) {
    void queryClient.invalidateQueries({ queryKey: inventoryDetailKey(ref) });
  }
  for (const ref of evt.removed) {
    queryClient.removeQueries({ queryKey: inventoryDetailKey(ref) });
  }
  // A guest-group pill's membership can shift on any guest add/remove/move;
  // cheap enough to invalidate every open expansion rather than compute
  // which specific pill(s) are affected.
  void queryClient.invalidateQueries({ queryKey: ["guest-group-expand"] });
}

let sharedWsClient: WsClient | undefined;

function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/**
 * Subscribes to the `/api/ws` "topology" topic for the component's
 * lifetime and applies every `topology.delta` event to the query cache
 * (deliverable #9: "delta-driven live updates via the WS bridge into query
 * cache"). Pass `client` in tests to inject a client wired to a real `ws`
 * test server (see ws.test.ts's pattern) instead of the shared browser
 * singleton.
 */
export function useTopologyWsBridge(client?: WsClient): void {
  const queryClient = useQueryClient();
  // Keep the latest queryClient in a ref so the subscription effect below
  // only needs to run once per client instance, not on every render.
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  useEffect(() => {
    const ws = client ?? getSharedWsClient();
    const unsubscribe = ws.subscribe(["topology"], (evt) => {
      if (isTopologyDeltaEvent(evt)) {
        applyTopologyDelta(queryClientRef.current, evt);
      }
    });
    return unsubscribe;
  }, [client]);
}
