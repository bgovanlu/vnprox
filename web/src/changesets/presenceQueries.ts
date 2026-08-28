// SPDX-License-Identifier: Apache-2.0

// T-2805 — the presence query and its WS bridge.
//
// Subscribing to `presence:<scope>` is BOTH the declaration "I am looking at
// this" and the delivery channel for the scope's `presence.changed` events —
// one subscription, on the connection the app already has. There is no
// second push channel and no "I am here" call: the subscription itself is
// the signal, and dropping the connection is what retracts it.
//
// The event carries a count and no identities (docs/api.md's WebSocket
// section), so it is used purely as an invalidation trigger; the names, when
// this session may see them, come from GET /presence — the same "push the
// count, fetch the detail" split drift.changed/findings.changed use.
import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getPresence, presenceTopic } from "../api/locks";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { PresenceResponse } from "../api/types";

export const presenceKey = (scope: string) => ["presence", scope] as const;

export function usePresenceQuery(scope: string | undefined) {
  return useQuery<PresenceResponse>({
    queryKey: presenceKey(scope ?? ""),
    queryFn: () => getPresence(scope ?? ""),
    enabled: scope !== undefined && scope !== "",
    // Presence is push-driven (the bridge below); this is only the fallback
    // for a connection that is down.
    staleTime: 10_000,
  });
}

function isPresenceChangedEvent(evt: WsServerEvent): evt is WsServerEvent & { scope: string; count: number } {
  return evt.event === "presence.changed" && typeof evt.scope === "string" && typeof evt.count === "number";
}

let sharedPresenceWsClient: WsClient | undefined;

function sharedClient(): WsClient {
  sharedPresenceWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedPresenceWsClient;
}

/**
 * Declares this client's presence on `scope` for the component's lifetime,
 * and refetches the scope's viewer list whenever the server says it changed.
 * Unmounting (or navigating away) drops the subscription, which is how
 * leaving is reported — no explicit "goodbye" that a closed laptop would
 * never send.
 *
 * Pass `client` in tests to inject one wired to a real `ws` test server.
 */
export function usePresenceWsBridge(scope: string | undefined, client?: WsClient): void {
  const queryClient = useQueryClient();
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  useEffect(() => {
    if (!scope) return;
    const ws = client ?? sharedClient();
    const unsubscribe = ws.subscribe([presenceTopic(scope)], (evt) => {
      if (isPresenceChangedEvent(evt) && evt.scope === scope) {
        void queryClientRef.current.invalidateQueries({ queryKey: presenceKey(scope) });
      }
    });
    return unsubscribe;
  }, [scope, client]);
}
