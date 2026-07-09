import type { AddressInfo } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import { WebSocket as NodeWebSocketClient, WebSocketServer } from "ws";
import { createWsClient, type WsClient, type WsServerEvent, type WsStatus } from "./ws";

async function waitFor(predicate: () => boolean, timeoutMs = 3000): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error("waitFor: timed out waiting for condition");
    }
    await new Promise((resolve) => setTimeout(resolve, 15));
  }
}

describe("createWsClient", () => {
  const servers: WebSocketServer[] = [];
  const clients: WsClient[] = [];

  afterEach(() => {
    for (const client of clients.splice(0)) client.close();
    for (const server of servers.splice(0)) server.close();
  });

  it("subscribes on connect, then survives a server restart by reconnecting and resubscribing", async () => {
    // Round 1: the client's initial connection.
    const round1Messages: unknown[] = [];
    const wss1 = new WebSocketServer({ port: 0 });
    servers.push(wss1);
    wss1.on("connection", (socket) => {
      socket.on("message", (data: Buffer) => {
        round1Messages.push(JSON.parse(data.toString()) as unknown);
      });
    });
    const port = (wss1.address() as AddressInfo).port;

    const statuses: WsStatus[] = [];
    const client = createWsClient({
      url: `ws://127.0.0.1:${String(port)}`,
      // `ws`'s client class implements the same addEventListener/send/
      // close/readyState surface the browser WebSocket does, so it's a
      // drop-in for testing this DOM-facing client under Node.
      WebSocketImpl: NodeWebSocketClient as unknown as typeof WebSocket,
      minBackoffMs: 20,
      maxBackoffMs: 80,
      onStatusChange: (s) => statuses.push(s),
    });
    clients.push(client);

    const receivedEvents: WsServerEvent[] = [];
    client.subscribe(["topology"], (evt) => receivedEvents.push(evt));

    await waitFor(() => client.status() === "open");
    await waitFor(() => round1Messages.length > 0);
    expect(round1Messages[0]).toEqual({ subscribe: ["topology"] });

    // Simulate the vnproxd process restarting out from under the client:
    // forcibly drop every live connection and stop listening entirely,
    // leaving a window where the client's reconnect attempts fail with
    // connection-refused before anything is listening again.
    for (const socket of wss1.clients) socket.terminate();
    await new Promise<void>((resolve) => {
      wss1.close(() => {
        resolve();
      });
    });

    await waitFor(() => client.status() === "reconnecting");
    await new Promise((resolve) => setTimeout(resolve, 60));

    // Round 2: a fresh server bound to the same port, standing in for
    // vnproxd having restarted.
    const round2Messages: unknown[] = [];
    const wss2 = new WebSocketServer({ port });
    servers.push(wss2);
    wss2.on("connection", (socket) => {
      socket.on("message", (data: Buffer) => {
        round2Messages.push(JSON.parse(data.toString()) as unknown);
      });
    });

    await waitFor(() => client.status() === "open", 5000);
    await waitFor(() => round2Messages.length > 0, 5000);
    expect(round2Messages[0]).toEqual({ subscribe: ["topology"] });

    // And the resubscription is real end-to-end: a server event sent on
    // the *new* connection reaches the subscriber that was registered
    // before the restart, with no action required from calling code.
    for (const socket of wss2.clients) {
      socket.send(JSON.stringify({ event: "topology.delta", added: [], updated: [], removed: [] }));
    }
    await waitFor(() => receivedEvents.length > 0, 3000);
    expect(receivedEvents[0]).toMatchObject({ event: "topology.delta" });

    expect(statuses).toContain("reconnecting");
    expect(statuses).toContain("open");
  }, 20000);

  it("stops delivering events to a subscriber after it unsubscribes, and sends the shrunken topic set", async () => {
    const messages: { subscribe: string[] }[] = [];
    const wss = new WebSocketServer({ port: 0 });
    servers.push(wss);
    wss.on("connection", (socket) => {
      socket.on("message", (data: Buffer) => {
        messages.push(JSON.parse(data.toString()) as { subscribe: string[] });
      });
    });
    const port = (wss.address() as AddressInfo).port;

    const client = createWsClient({
      url: `ws://127.0.0.1:${String(port)}`,
      WebSocketImpl: NodeWebSocketClient as unknown as typeof WebSocket,
      minBackoffMs: 20,
      maxBackoffMs: 80,
    });
    clients.push(client);

    const a: WsServerEvent[] = [];
    const b: WsServerEvent[] = [];
    const unsubA = client.subscribe(["topology"], (evt) => a.push(evt));
    client.subscribe(["changesets"], (evt) => b.push(evt));

    // Both subscribe() calls above happen synchronously, before the
    // socket has finished connecting, so they coalesce into a single
    // `{"subscribe":[...]}` sent once the connection opens rather than
    // one message per call.
    await waitFor(() => client.status() === "open");
    await waitFor(() => messages.length >= 1);
    expect(messages[0]?.subscribe.slice().sort()).toEqual(["changesets", "topology"]);

    unsubA();
    await waitFor(() => messages.length >= 2);
    await waitFor(() => {
      const last = messages[messages.length - 1];
      return last !== undefined && !last.subscribe.includes("topology");
    });
    expect(messages[messages.length - 1]).toEqual({ subscribe: ["changesets"] });
  });
});
