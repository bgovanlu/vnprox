// WebSocket client for /api/ws (docs/api.md §WebSocket). Implements the
// documented subscription protocol — client sends
// `{"subscribe": ["topology", "changesets", "metrics:<ref>", "tasks"]}`,
// server pushes named events (`topology.delta`, `changeset.status`,
// `metrics.sample`, `drift.changed`) — behind reconnect-with-backoff so
// callers get a `subscribe(topics, onEvent)` API and never touch the raw
// socket, a reconnect timer, or resubscription bookkeeping themselves.
//
// docs/api.md doesn't spell out the exact JSON shape of a server push
// beyond the event-name/payload table, so this client assumes each
// message is a flat JSON object with an `event` string field alongside
// its payload fields (e.g. `{"event":"topology.delta","added":[...],...}`)
// and hands the whole object to subscribers, who already know (from
// docs/api.md) which event names to expect for the topics they asked
// for. T-1xx (whichever task implements the server side of the hub)
// should confirm this envelope shape against what it actually emits.

export type WsStatus = "connecting" | "open" | "reconnecting" | "closed";

/** A single server-pushed message: the documented `event` name plus
 * whatever payload fields came with it. */
export interface WsServerEvent {
  event: string;
  [key: string]: unknown;
}

export interface WsClientOptions {
  /** WS URL, e.g. `wss://host/api/ws`. Can be a thunk so callers (and
   * tests) can defer resolving `location` until connect time. */
  url: string | (() => string);
  /** Injectable WebSocket constructor — defaults to the browser global.
   * Tests pass the `ws` package's client class, which implements the
   * same `addEventListener`/`send`/`close`/`readyState` surface, so the
   * client under test never depends on a browser environment. */
  WebSocketImpl?: typeof WebSocket;
  /** Backoff floor/ceiling in ms. Real usage wants seconds-scale delays;
   * tests override these to keep runtime short. */
  minBackoffMs?: number;
  maxBackoffMs?: number;
  onStatusChange?: (status: WsStatus) => void;
}

export interface WsClient {
  /** Declare interest in one or more subscription topics (per
   * docs/api.md: `topology`, `changesets`, `metrics:<ref>`, `tasks`).
   * `onEvent` fires for every server message for as long as this
   * subscription is active (and any others sharing the connection).
   * Returns an unsubscribe function; once the last subscriber for a
   * topic unsubscribes, the topic is dropped from the next
   * `{"subscribe":[...]}` sent to the server. */
  subscribe(topics: string[], onEvent: (evt: WsServerEvent) => void): () => void;
  status(): WsStatus;
  /** Force-closes the socket and stops reconnecting. For app teardown /
   * tests; not needed in normal use. */
  close(): void;
}

interface Subscription {
  id: number;
  topics: string[];
  onEvent: (evt: WsServerEvent) => void;
}

const DEFAULT_MIN_BACKOFF_MS = 500;
const DEFAULT_MAX_BACKOFF_MS = 15_000;

/** Builds the browser-facing `/api/ws` URL from the current page location
 * (ws: for http:, wss: for https:) so callers don't have to. */
export function defaultWsUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/ws`;
}

export function createWsClient(options: WsClientOptions): WsClient {
  // Falls back to the browser global (always present in real usage);
  // tests inject the `ws` package's client instead, since plain Node has
  // no global WebSocket. If neither is available, `new WebSocketImpl(...)`
  // below throws its own clear TypeError — not worth fighting the type
  // checker (globalThis.WebSocket's DOM-lib type is always-present) for a
  // marginally nicer message in a case that shouldn't occur in practice.
  const WebSocketImpl = options.WebSocketImpl ?? globalThis.WebSocket;
  const minBackoffMs = options.minBackoffMs ?? DEFAULT_MIN_BACKOFF_MS;
  const maxBackoffMs = options.maxBackoffMs ?? DEFAULT_MAX_BACKOFF_MS;

  let socket: WebSocket | undefined;
  let status: WsStatus = "connecting";
  let closedByCaller = false;
  let reconnectAttempt = 0;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let nextSubId = 0;
  const subscriptions = new Map<number, Subscription>();

  function setStatus(next: WsStatus): void {
    if (status === next) return;
    status = next;
    options.onStatusChange?.(next);
  }

  function resolveUrl(): string {
    return typeof options.url === "function" ? options.url() : options.url;
  }

  function currentTopics(): string[] {
    const set = new Set<string>();
    for (const sub of subscriptions.values()) {
      for (const topic of sub.topics) set.add(topic);
    }
    return Array.from(set);
  }

  function sendSubscribe(): void {
    if (socket?.readyState !== WebSocketImpl.OPEN) return;
    socket.send(JSON.stringify({ subscribe: currentTopics() }));
  }

  function scheduleReconnect(): void {
    if (closedByCaller) return;
    setStatus("reconnecting");
    const backoff = Math.min(maxBackoffMs, minBackoffMs * 2 ** reconnectAttempt);
    const jitter = backoff * (0.5 + Math.random() * 0.5);
    reconnectAttempt += 1;
    reconnectTimer = setTimeout(connect, jitter);
  }

  function connect(): void {
    if (closedByCaller) return;
    setStatus(reconnectAttempt === 0 ? "connecting" : "reconnecting");

    const ws = new WebSocketImpl(resolveUrl());
    socket = ws;

    ws.addEventListener("open", () => {
      reconnectAttempt = 0;
      setStatus("open");
      // Resubscribe to everything active subscribers still want — this
      // is what makes reconnection transparent to callers: a subscriber
      // that registered before a disconnect keeps receiving events after
      // the client silently reconnects, with no action on its part.
      if (currentTopics().length > 0) {
        sendSubscribe();
      }
    });

    ws.addEventListener("message", (ev: MessageEvent<unknown>) => {
      const raw = typeof ev.data === "string" ? ev.data : undefined;
      if (raw === undefined) return;
      let parsed: unknown;
      try {
        parsed = JSON.parse(raw);
      } catch {
        return; // not a JSON message; ignore rather than crash the app
      }
      if (
        typeof parsed !== "object" ||
        parsed === null ||
        typeof (parsed as { event?: unknown }).event !== "string"
      ) {
        return;
      }
      const msg = parsed as WsServerEvent;
      for (const sub of subscriptions.values()) {
        sub.onEvent(msg);
      }
    });

    ws.addEventListener("close", () => {
      if (socket === ws) socket = undefined;
      if (!closedByCaller) scheduleReconnect();
    });

    // "error" is always followed by "close" on both the browser
    // WebSocket and the `ws` package's client, so reconnect scheduling
    // lives solely in the close handler above — this handler exists
    // only so an error event is never an unhandled/uncaught rejection.
    ws.addEventListener("error", () => {
      /* no-op: close handles reconnect scheduling */
    });
  }

  connect();

  return {
    subscribe(topics, onEvent) {
      const id = nextSubId++;
      subscriptions.set(id, { id, topics, onEvent });
      sendSubscribe();
      return () => {
        subscriptions.delete(id);
        sendSubscribe();
      };
    },
    status() {
      return status;
    },
    close() {
      closedByCaller = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      socket?.close();
      setStatus("closed");
    },
  };
}
