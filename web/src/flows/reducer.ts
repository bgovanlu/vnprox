// SPDX-License-Identifier: Apache-2.0

// Pure state machine for the Flow Explorer (T-1003): a bounded, client-
// side-capped record buffer fed by both the initial GET /flows page
// (already server-filtered) and the `flow.batch` WS follow stream (which
// pushes every record ingested on this node, unfiltered) — the exact same
// "REST for the snapshot, WS for live increments, client re-applies the
// same filter to the live half" shape web/src/fwlog/reducer.ts already
// established for the firewall log viewer. Kept entirely free of
// React/DOM/WS so it's directly Vitest-able with no timers, no jsdom, no
// network.
import type { FlowRecord } from "../api/types";
import { PROTO_NAME_TO_NUMBER } from "./proto";

/** Client-side history cap — independent of the server's own flow_samples
 * ring bound (internal/flow's retention window / row cap): this is "how
 * much do we keep in the browser tab for the live-follow view", not "how
 * much does the daemon remember". */
export const CLIENT_BUFFER_CAP = 2000;

/** How many of the (already filtered/sorted) rows FlowExplorer actually
 * mounts into the DOM at once — mirrors fwlog/reducer.ts's RENDER_CAP. */
export const RENDER_CAP = 300;

export type FlowSortKey = "recency" | "bytes" | "packets";
export type FlowViewMode = "raw" | "conversations";

/** Free-text filter fields mirroring GET /flows' own query params
 * (docs/api.md's Flows section) — every field optional/ANDed, "" = any.
 * Numeric-looking fields are kept as strings for form-input ergonomics
 * (matchesFlowFilter parses them), the same convention FwLogFilterState's
 * `vmid` field uses. */
export interface FlowFilterState {
  guest: string;
  vlan: string;
  subnet: string;
  port: string;
  protocol: string;
}

export const emptyFlowFilter: FlowFilterState = { guest: "", vlan: "", subnet: "", port: "", protocol: "" };

export interface FlowViewState {
  /** Oldest-first accumulated buffer (bounded to CLIENT_BUFFER_CAP). */
  records: FlowRecord[];
  /** The daemon's own cumulative rate-cap drop count, mirrored verbatim
   * from the most recent REST page (nextCursor/partial aside) or WS
   * batch's droppedTotal. */
  serverDroppedTotal: number;
  /** Records evicted from the client buffer to stay within
   * CLIENT_BUFFER_CAP — the client-side half of the drop indicator. */
  clientDroppedTotal: number;
  filter: FlowFilterState;
  sort: FlowSortKey;
  view: FlowViewMode;
}

export const initialFlowViewState: FlowViewState = {
  records: [],
  serverDroppedTotal: 0,
  clientDroppedTotal: 0,
  filter: emptyFlowFilter,
  sort: "recency",
  view: "raw",
};

export type FlowAction =
  | { type: "loaded"; items: FlowRecord[] }
  | { type: "batch"; entries: FlowRecord[]; droppedTotal: number }
  | { type: "setFilter"; filter: Partial<FlowFilterState> }
  | { type: "setSort"; sort: FlowSortKey }
  | { type: "setView"; view: FlowViewMode }
  | { type: "clear" };

function pushBounded(records: FlowRecord[], incoming: readonly FlowRecord[], cap: number): { records: FlowRecord[]; dropped: number } {
  if (incoming.length === 0) return { records, dropped: 0 };
  const combined = records.concat(incoming);
  if (combined.length <= cap) return { records: combined, dropped: 0 };
  const dropped = combined.length - cap;
  return { records: combined.slice(dropped), dropped };
}

export function flowReducer(state: FlowViewState, action: FlowAction): FlowViewState {
  switch (action.type) {
    case "loaded": {
      const { records, dropped } = pushBounded([], action.items, CLIENT_BUFFER_CAP);
      return { ...state, records, clientDroppedTotal: state.clientDroppedTotal + dropped };
    }
    case "batch": {
      if (action.entries.length === 0) {
        return { ...state, serverDroppedTotal: action.droppedTotal };
      }
      const { records, dropped } = pushBounded(state.records, action.entries, CLIENT_BUFFER_CAP);
      return {
        ...state,
        records,
        clientDroppedTotal: state.clientDroppedTotal + dropped,
        serverDroppedTotal: action.droppedTotal,
      };
    }
    case "setFilter":
      return { ...state, filter: { ...state.filter, ...action.filter } };
    case "setSort":
      return { ...state, sort: action.sort };
    case "setView":
      return { ...state, view: action.view };
    case "clear":
      return { ...state, records: [], clientDroppedTotal: 0 };
    default:
      return state;
  }
}

// --- Client-side filter matching (mirrors GET /flows' server-side filter
// semantics exactly — docs/api.md's Flows section — so a live WS record
// merges into the currently-filtered view identically to how a fresh
// server-filtered fetch would have included/excluded it) ------------------

function parseIPv4(ip: string): number | undefined {
  const parts = ip.trim().split(".");
  if (parts.length !== 4) return undefined;
  let out = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return undefined;
    const n = Number(part);
    if (n < 0 || n > 255) return undefined;
    out = (out << 8) | n;
  }
  return out >>> 0;
}

function parseCIDR(cidr: string): { base: number; prefix: number } | undefined {
  const [addr, prefixStr] = cidr.split("/", 2);
  if (!addr || prefixStr === undefined) return undefined;
  const prefix = Number(prefixStr);
  if (!Number.isInteger(prefix) || prefix < 0 || prefix > 32) return undefined;
  const base = parseIPv4(addr);
  if (base === undefined) return undefined;
  return { base, prefix };
}

/** IPv4-only CIDR containment (this codebase's fixtures are all IPv4 —
 * mirrors simulator/ipSubnetContext.ts's identical helper). An unparsable
 * CIDR or IP matches nothing rather than throwing. */
function ipInCidr(ip: string, cidr: string): boolean {
  const parsedCidr = parseCIDR(cidr);
  const parsedIp = parseIPv4(ip);
  if (!parsedCidr || parsedIp === undefined) return false;
  if (parsedCidr.prefix === 0) return true;
  const mask = parsedCidr.prefix === 32 ? 0xffffffff : (0xffffffff << (32 - parsedCidr.prefix)) >>> 0;
  return (parsedCidr.base & mask) === (parsedIp & mask);
}

/** Resolves a `?protocol=` filter value to a numeric IP protocol, per
 * ProtoNumberFromName's Go counterpart (internal/flow/record.go): a
 * recognized name (tcp/udp/icmp/icmpv6/sctp) or a raw number. An
 * unrecognized value resolves to -1 (never matches any real proto, 0-255)
 * — "matches nothing" rather than a 400/throw, mirroring the server. */
function resolveProtoFilter(value: string): number {
  const trimmed = value.trim().toLowerCase();
  if (trimmed === "") return -1;
  const named = PROTO_NAME_TO_NUMBER[trimmed];
  if (named !== undefined) return named;
  const n = Number(trimmed);
  return Number.isInteger(n) ? n : -1;
}

/** Reports whether record satisfies every non-empty field of filter — the
 * same "every set field ANDed" composition GET /flows applies server-side. */
export function matchesFlowFilter(record: FlowRecord, filter: FlowFilterState): boolean {
  if (filter.guest && record.srcRef !== filter.guest && record.dstRef !== filter.guest) return false;
  if (filter.vlan) {
    const want = Number(filter.vlan);
    if (!Number.isFinite(want) || record.vlan !== want) return false;
  }
  if (filter.subnet) {
    if (!ipInCidr(record.srcIp, filter.subnet) && !ipInCidr(record.dstIp, filter.subnet)) return false;
  }
  if (filter.port) {
    const want = Number(filter.port);
    if (!Number.isFinite(want) || (record.srcPort !== want && record.dstPort !== want)) return false;
  }
  if (filter.protocol) {
    const want = resolveProtoFilter(filter.protocol);
    if (record.proto !== want) return false;
  }
  return true;
}

/** Selects the currently-visible (filtered) records — pure and cheap
 * enough to recompute on every render, mirroring fwlog's
 * selectVisibleEntries. */
export function selectVisibleFlows(state: FlowViewState): FlowRecord[] {
  const hasFilter = Boolean(state.filter.guest || state.filter.vlan || state.filter.subnet || state.filter.port || state.filter.protocol);
  return hasFilter ? state.records.filter((r) => matchesFlowFilter(r, state.filter)) : state.records;
}

/** Sorts a record list per FlowSortKey — "recency" is newest-first (most
 * useful default for a live-following table), "bytes"/"packets" are
 * largest-first. Never mutates its input. */
export function sortFlows(records: readonly FlowRecord[], sort: FlowSortKey): FlowRecord[] {
  const copy = records.slice();
  switch (sort) {
    case "bytes":
      return copy.sort((a, b) => b.bytes - a.bytes);
    case "packets":
      return copy.sort((a, b) => b.packets - a.packets);
    case "recency":
    default:
      return copy.sort((a, b) => b.at - a.at);
  }
}

/** One conversation row: every record sharing the same (srcRef||srcIp,
 * dstRef||dstIp) pair (direction-sensitive — a->b and b->a are distinct
 * rows, mirroring how the map's flowEdges.ts treats direction), summed
 * over the currently-visible record set. `key` identifies the pair the
 * same way regardless of which endpoint resolved to an inventory ref, so
 * grouping is stable even when only one side resolved. */
export interface ConversationRow {
  key: string;
  srcIp: string;
  dstIp: string;
  srcRef?: string;
  dstRef?: string;
  bytes: number;
  packets: number;
  recordCount: number;
  lastAt: number;
}

function conversationKey(r: FlowRecord): string {
  return `${r.srcRef ?? r.srcIp}=>${r.dstRef ?? r.dstIp}`;
}

/** Groups records into per-conversation-pair aggregates, summing
 * bytes/packets and tracking the most recent `at` — the "filter/sort/
 * aggregate by conversation" requirement (docs/roadmap-next.md, echoed by
 * this task's card). Pure, order-independent of the input. */
export function aggregateConversations(records: readonly FlowRecord[]): ConversationRow[] {
  const byKey = new Map<string, ConversationRow>();
  for (const r of records) {
    const key = conversationKey(r);
    const existing = byKey.get(key);
    if (existing) {
      existing.bytes += r.bytes;
      existing.packets += r.packets;
      existing.recordCount += 1;
      existing.lastAt = Math.max(existing.lastAt, r.at);
    } else {
      byKey.set(key, {
        key,
        srcIp: r.srcIp,
        dstIp: r.dstIp,
        srcRef: r.srcRef,
        dstRef: r.dstRef,
        bytes: r.bytes,
        packets: r.packets,
        recordCount: 1,
        lastAt: r.at,
      });
    }
  }
  return Array.from(byKey.values());
}

/** Sorts conversation rows — mirrors sortFlows' vocabulary ("recency" =
 * lastAt descending). */
export function sortConversations(rows: readonly ConversationRow[], sort: FlowSortKey): ConversationRow[] {
  const copy = rows.slice();
  switch (sort) {
    case "bytes":
      return copy.sort((a, b) => b.bytes - a.bytes);
    case "packets":
      return copy.sort((a, b) => b.packets - a.packets);
    case "recency":
    default:
      return copy.sort((a, b) => b.lastAt - a.lastAt);
  }
}
