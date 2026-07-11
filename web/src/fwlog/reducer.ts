// Pure state machine for the firewall log viewer (T-505): a bounded,
// client-side-capped entry buffer fed by both the initial REST page and
// the `firewall.log.batch` WS follow stream, pause/resume (freezing the
// view without losing what arrives while paused), filter composition, and
// the drop-indicator counters AC3 requires — kept entirely free of
// React/DOM/WS so it can be fed thousands of synthetic events in a tight
// loop in tests (see reducer.test.ts's storm test) with no timers, no
// jsdom, no network.
import type { FwLogEntry, FwLogPage } from "../api/types";

/** Client-side history cap — independent of the server's own
 * RingBuffer capacity (internal/fwlog.DefaultBufferCapacity): this is
 * "how much do we keep in the browser tab", not "how much does the
 * daemon remember". Chosen well above what a human filters/scrolls
 * through, comfortably below what would make DOM rendering (even
 * render-capped, see FwLogViewer) sluggish. */
export const CLIENT_BUFFER_CAP = 1000;

/** How many of the (already filtered) entries FwLogViewer actually mounts
 * into the DOM — AC3's "no browser lockup" applies to rendering, not just
 * the in-memory buffer, so this is deliberately much smaller than
 * CLIENT_BUFFER_CAP. Exported so the component and its tests agree on one
 * number. */
export const RENDER_CAP = 300;

export interface FwLogFilterState {
  node: string;
  vmid: string; // free-text input; "" = any, non-numeric input simply never matches (see matchesFilter)
  direction: "" | "in" | "out";
  action: string;
}

export const emptyFwLogFilter: FwLogFilterState = { node: "", vmid: "", direction: "", action: "" };

export interface FwLogViewState {
  /** Oldest-first, applied history (bounded to CLIENT_BUFFER_CAP). */
  entries: FwLogEntry[];
  /** Oldest-first entries received while paused, not yet merged into
   * `entries` — flushed on resume. Also bounded, so a long pause during a
   * storm can't grow memory unboundedly either. */
  pending: FwLogEntry[];
  paused: boolean;
  /** Entries evicted from the client buffer (either `entries` or
   * `pending`) to stay within CLIENT_BUFFER_CAP — the client-side half of
   * AC3's drop indicator (the server-side half is `serverDroppedTotal`). */
  clientDroppedTotal: number;
  /** The daemon's own cumulative rate-cap drop count, mirrored verbatim
   * from the most recent REST page or WS batch. */
  serverDroppedTotal: number;
  filter: FwLogFilterState;
}

export const initialFwLogViewState: FwLogViewState = {
  entries: [],
  pending: [],
  paused: false,
  clientDroppedTotal: 0,
  serverDroppedTotal: 0,
  filter: emptyFwLogFilter,
};

export type FwLogAction =
  | { type: "loaded"; page: FwLogPage }
  | { type: "batch"; entries: FwLogEntry[]; droppedTotal: number }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "setFilter"; filter: Partial<FwLogFilterState> }
  | { type: "clear" };

/** Appends incoming to entries, evicting the oldest items beyond cap.
 * Returns the (possibly trimmed) combined list and how many were
 * evicted — never mutates its inputs. */
function pushBounded(entries: FwLogEntry[], incoming: readonly FwLogEntry[], cap: number): { entries: FwLogEntry[]; dropped: number } {
  if (incoming.length === 0) return { entries, dropped: 0 };
  const combined = entries.concat(incoming);
  if (combined.length <= cap) return { entries: combined, dropped: 0 };
  const dropped = combined.length - cap;
  return { entries: combined.slice(dropped), dropped };
}

export function fwLogReducer(state: FwLogViewState, action: FwLogAction): FwLogViewState {
  switch (action.type) {
    case "loaded": {
      const { entries, dropped } = pushBounded([], action.page.items, CLIENT_BUFFER_CAP);
      return {
        ...state,
        entries,
        pending: [],
        paused: false,
        clientDroppedTotal: state.clientDroppedTotal + dropped,
        serverDroppedTotal: action.page.droppedTotal,
      };
    }
    case "batch": {
      if (action.entries.length === 0) {
        return { ...state, serverDroppedTotal: action.droppedTotal };
      }
      if (state.paused) {
        const { entries: pending, dropped } = pushBounded(state.pending, action.entries, CLIENT_BUFFER_CAP);
        return {
          ...state,
          pending,
          clientDroppedTotal: state.clientDroppedTotal + dropped,
          serverDroppedTotal: action.droppedTotal,
        };
      }
      const { entries, dropped } = pushBounded(state.entries, action.entries, CLIENT_BUFFER_CAP);
      return {
        ...state,
        entries,
        clientDroppedTotal: state.clientDroppedTotal + dropped,
        serverDroppedTotal: action.droppedTotal,
      };
    }
    case "pause":
      return state.paused ? state : { ...state, paused: true };
    case "resume": {
      if (!state.paused) return state;
      const { entries, dropped } = pushBounded(state.entries, state.pending, CLIENT_BUFFER_CAP);
      return { ...state, paused: false, entries, pending: [], clientDroppedTotal: state.clientDroppedTotal + dropped };
    }
    case "setFilter":
      return { ...state, filter: { ...state.filter, ...action.filter } };
    case "clear":
      return { ...state, entries: [], pending: [], clientDroppedTotal: 0 };
    default:
      return state;
  }
}

/** Reports whether entry satisfies every non-empty field of filter —
 * the exact same "every set field ANDed, case-insensitive" composition
 * internal/fwlog.Filter.Match applies server-side, mirrored client-side
 * so filtering the already-buffered stream doesn't require a round trip. */
export function matchesFilter(entry: FwLogEntry, filter: FwLogFilterState): boolean {
  if (filter.node && entry.node.toLowerCase() !== filter.node.toLowerCase()) return false;
  if (filter.vmid) {
    const want = Number(filter.vmid);
    if (!Number.isFinite(want) || entry.vmid !== want) return false;
  }
  if (filter.direction && entry.direction !== filter.direction) return false;
  if (filter.action && (entry.action ?? "").toLowerCase() !== filter.action.toLowerCase()) return false;
  return true;
}

/** Selects the currently-visible (filtered) entries, oldest first. Pure
 * and cheap enough to recompute on every render (an array filter over at
 * most CLIENT_BUFFER_CAP items) rather than requiring memoization
 * bookkeeping in the reducer itself. */
export function selectVisibleEntries(state: FwLogViewState): FwLogEntry[] {
  if (!state.filter.node && !state.filter.vmid && !state.filter.direction && !state.filter.action) {
    return state.entries;
  }
  return state.entries.filter((e) => matchesFilter(e, state.filter));
}
