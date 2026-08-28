// SPDX-License-Identifier: Apache-2.0

import type { AddressInfo } from "node:net";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WebSocket as NodeWebSocketClient, WebSocketServer } from "ws";
import { createWsClient, type WsClient } from "../api/ws";
import * as metricsApi from "../api/metrics";
import { isMetricsSampleEvent, useLiveMetrics, utilizationMap } from "./metricsQueries";

async function waitFor(predicate: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error("waitFor: timed out waiting for condition");
    }
    await new Promise((resolve) => setTimeout(resolve, 15));
  }
}

function withQueryClient(queryClient: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("isMetricsSampleEvent", () => {
  it("accepts a well-formed metrics.sample payload", () => {
    expect(
      isMetricsSampleEvent({
        event: "metrics.sample",
        ref: "physnic:pve1:eno1",
        at: 100,
        rates: { rxBps: 1, txBps: 2, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 },
      }),
    ).toBe(true);
  });

  it("rejects other event names", () => {
    expect(isMetricsSampleEvent({ event: "topology.delta" })).toBe(false);
  });

  it("rejects a payload missing rates or with the wrong shape", () => {
    expect(isMetricsSampleEvent({ event: "metrics.sample", ref: "x", at: 1 })).toBe(false);
    expect(isMetricsSampleEvent({ event: "metrics.sample", ref: "x", at: 1, rates: "nope" })).toBe(false);
  });
});

describe("utilizationMap", () => {
  it("projects a live-metrics map down to ref -> utilizationPct, dropping entries with none", () => {
    const live = new Map([
      ["a", { ref: "a", at: 1, rates: {} as never, utilizationPct: 42 }],
      ["b", { ref: "b", at: 1, rates: {} as never }],
    ]);
    expect(utilizationMap(live)).toEqual(new Map([["a", 42]]));
  });
});

describe("useLiveMetrics (real WebSocket connection)", () => {
  const servers: WebSocketServer[] = [];
  const clients: WsClient[] = [];

  afterEach(() => {
    for (const client of clients.splice(0)) client.close();
    for (const server of servers.splice(0)) server.close();
    vi.restoreAllMocks();
  });

  it("seeds from GET /metrics/live, then applies a real metrics.sample push, recomputing utilization from the cached speed", async () => {
    vi.spyOn(metricsApi, "fetchMetricsLive").mockResolvedValue([
      { ref: "physnic:pve1:eno1", at: 0, rates: { rxBps: 0, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 }, speedMbps: 1000 },
    ]);

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
    const { result } = renderHook(() => useLiveMetrics(["physnic:pve1:eno1"], true, client), {
      wrapper: withQueryClient(queryClient),
    });

    await waitFor(() => result.current.get("physnic:pve1:eno1")?.speedMbps === 1000);

    await waitFor(() => client.status() === "open");
    await waitFor(() => wss.clients.size > 0);
    for (const socket of wss.clients) {
      socket.send(
        JSON.stringify({
          event: "metrics.sample",
          ref: "physnic:pve1:eno1",
          at: 5,
          rates: { rxBps: 500_000_000, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 },
        }),
      );
    }

    await waitFor(() => result.current.get("physnic:pve1:eno1")?.utilizationPct === 50);
    const m = result.current.get("physnic:pve1:eno1");
    expect(m?.speedMbps).toBe(1000); // preserved across the WS-only update
    expect(m?.at).toBe(5);
  }, 10000);

  it("returns an empty map when disabled, without calling fetchMetricsLive", () => {
    const spy = vi.spyOn(metricsApi, "fetchMetricsLive");
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { result } = renderHook(() => useLiveMetrics(["physnic:pve1:eno1"], false), {
      wrapper: withQueryClient(queryClient),
    });
    expect(result.current.size).toBe(0);
    expect(spy).not.toHaveBeenCalled();
  });
});
