import type { AddressInfo } from "node:net";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { WebSocket as NodeWebSocketClient, WebSocketServer } from "ws";
import { createWsClient, type WsClient } from "../api/ws";
import type { TopologyDeltaEvent, TopologyResponse } from "../api/types";
import {
  TOPOLOGY_QUERY_KEY,
  applyTopologyDelta,
  guestGroupExpandKey,
  inventoryDetailKey,
  useTopologyWsBridge,
} from "./queries";

async function waitFor(predicate: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error("waitFor: timed out waiting for condition");
    }
    await new Promise((resolve) => setTimeout(resolve, 15));
  }
}

const emptyTopology: TopologyResponse = { nodes: [], edges: [], layers: [], generatedAt: 1 };

describe("applyTopologyDelta", () => {
  it("invalidates the topology query and any detail queries for added/updated refs", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(TOPOLOGY_QUERY_KEY, emptyTopology);
    queryClient.setQueryData(inventoryDetailKey("bridge:pve1:vmbr0"), { ref: "bridge:pve1:vmbr0" });
    queryClient.setQueryData(inventoryDetailKey("bond:pve1:bond0"), { ref: "bond:pve1:bond0" });
    queryClient.setQueryData(guestGroupExpandKey("guest-group:pve1:bridge:pve1:vmbr0"), { nodes: [], edges: [] });

    const evt: TopologyDeltaEvent = {
      event: "topology.delta",
      added: [],
      updated: ["bridge:pve1:vmbr0"],
      removed: ["bond:pve1:bond0"],
    };
    applyTopologyDelta(queryClient, evt);

    expect(queryClient.getQueryState(TOPOLOGY_QUERY_KEY)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(inventoryDetailKey("bridge:pve1:vmbr0"))?.isInvalidated).toBe(true);
    // Removed refs are dropped outright, not merely invalidated.
    expect(queryClient.getQueryData(inventoryDetailKey("bond:pve1:bond0"))).toBeUndefined();
    expect(
      queryClient.getQueryState(guestGroupExpandKey("guest-group:pve1:bridge:pve1:vmbr0"))?.isInvalidated,
    ).toBe(true);
  });

  it("ignores an empty delta gracefully (still invalidates topology, no crash on empty arrays)", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(TOPOLOGY_QUERY_KEY, emptyTopology);
    expect(() => {
      applyTopologyDelta(queryClient, { event: "topology.delta", added: [], updated: [], removed: [] });
    }).not.toThrow();
    expect(queryClient.getQueryState(TOPOLOGY_QUERY_KEY)?.isInvalidated).toBe(true);
  });
});

describe("useTopologyWsBridge (real WebSocket connection)", () => {
  const servers: WebSocketServer[] = [];
  const clients: WsClient[] = [];

  afterEach(() => {
    for (const client of clients.splice(0)) client.close();
    for (const server of servers.splice(0)) server.close();
  });

  it("applies a real topology.delta pushed over a live WS connection to the query cache", async () => {
    const wss = new WebSocketServer({ port: 0 });
    servers.push(wss);
    const port = (wss.address() as AddressInfo).port;

    const client = createWsClient({
      url: `ws://127.0.0.1:${String(port)}`,
      WebSocketImpl: NodeWebSocketClient as unknown as typeof WebSocket,
      minBackoffMs: 20,
      maxBackoffMs: 80,
    });
    clients.push(client);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(TOPOLOGY_QUERY_KEY, emptyTopology);
    queryClient.setQueryData(inventoryDetailKey("bridge:pve1:vmbr0"), { ref: "bridge:pve1:vmbr0" });

    renderHook(() => { useTopologyWsBridge(client); }, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    });

    await waitFor(() => client.status() === "open");
    await waitFor(() => wss.clients.size > 0);

    for (const socket of wss.clients) {
      socket.send(
        JSON.stringify({ event: "topology.delta", added: [], updated: ["bridge:pve1:vmbr0"], removed: [] }),
      );
    }

    await waitFor(() => queryClient.getQueryState(TOPOLOGY_QUERY_KEY)?.isInvalidated === true);
    expect(queryClient.getQueryState(inventoryDetailKey("bridge:pve1:vmbr0"))?.isInvalidated).toBe(true);
  }, 10000);

  it("ignores non-topology.delta events (e.g. a future changeset.status push) without touching the cache", async () => {
    const wss = new WebSocketServer({ port: 0 });
    servers.push(wss);
    const port = (wss.address() as AddressInfo).port;

    const client = createWsClient({
      url: `ws://127.0.0.1:${String(port)}`,
      WebSocketImpl: NodeWebSocketClient as unknown as typeof WebSocket,
      minBackoffMs: 20,
      maxBackoffMs: 80,
    });
    clients.push(client);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(TOPOLOGY_QUERY_KEY, emptyTopology);

    renderHook(() => { useTopologyWsBridge(client); }, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    });

    await waitFor(() => client.status() === "open");
    await waitFor(() => wss.clients.size > 0);

    for (const socket of wss.clients) {
      socket.send(JSON.stringify({ event: "changeset.status", id: "abc", status: "applying" }));
    }
    // Give the (non-)handler a moment to (not) run.
    await new Promise((resolve) => setTimeout(resolve, 100));

    expect(queryClient.getQueryState(TOPOLOGY_QUERY_KEY)?.isInvalidated).toBeFalsy();
  }, 10000);
});
