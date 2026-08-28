// SPDX-License-Identifier: Apache-2.0

// Pure client-side grouping/flap-highlighting logic for the neighbor
// binding timeline (Tools page, T-3905). This is a presentation-layer
// convenience over already-fetched GET /neighbors/history pages — it makes
// a flap sequence visually distinguishable from a single clean rebind
// (T-3905 AC3) without a second network round-trip to the findings stream.
//
// The AUTHORITATIVE flap signal is the backend's `neighbor_binding_flap`
// finding (internal/neighbor.HistoryRecorder.Flaps, docs/api.md's "Neighbor
// binding history" section) — this module deliberately mirrors that same
// threshold/window pair (IPFlapThreshold=3/IPFlapWindow=2m,
// MACClaimThreshold=5/MACClaimWindow=2m, internal/neighbor/history.go) so
// the UI's own highlighting agrees with what the findings stream would
// report, but it is a redundant, best-effort client computation over
// whatever page of history is currently loaded — NOT a replacement for
// checking GET /findings. If the two thresholds ever drift apart, update
// them together; there is no single shared source of truth across the Go
// backend and this TypeScript module.
import type { NeighborBinding } from "../api/types";

/** Trailing window (seconds — NeighborBinding.at is unix seconds) both flap
 * directions evaluate over. Matches internal/neighbor.IPFlapWindow /
 * MACClaimWindow exactly (both 2 minutes). */
export const IP_FLAP_WINDOW_SECONDS = 2 * 60;
export const MAC_CLAIM_WINDOW_SECONDS = 2 * 60;

/** Matches internal/neighbor.IPFlapThreshold: 3 or more genuine transitions
 * (a first-seen row, prevMac unset, never counts) within IP_FLAP_WINDOW_SECONDS
 * of the group's own newest event. */
export const IP_FLAP_THRESHOLD = 3;

/** Matches internal/neighbor.MACClaimThreshold: 5 or more distinct IPs
 * recorded for one MAC within MAC_CLAIM_WINDOW_SECONDS. */
export const MAC_CLAIM_THRESHOLD = 5;

/** One (node, ip) pair's binding history, newest event first. */
export interface BindingGroup {
  key: string;
  node: string;
  ip: string;
  /** Newest-first. */
  events: NeighborBinding[];
  /** True when this group crosses IP_FLAP_THRESHOLD within
   * IP_FLAP_WINDOW_SECONDS of its own newest event — the ip_churn
   * direction. */
  isFlapping: boolean;
}

/** Groups a GET /neighbors/history page by (node, ip), newest-first within
 * each group and groups ordered by their own newest event, newest group
 * first — turning a flat transition list into per-binding timelines. */
export function groupNeighborHistory(items: NeighborBinding[]): BindingGroup[] {
  const byKey = new Map<string, NeighborBinding[]>();
  for (const item of items) {
    const key = `${item.node}|${item.ip}`;
    const list = byKey.get(key);
    if (list) {
      list.push(item);
    } else {
      byKey.set(key, [item]);
    }
  }

  const groups: BindingGroup[] = [];
  for (const [key, events] of byKey) {
    const sorted = [...events].sort((a, b) => b.at - a.at);
    const [newest] = sorted;
    if (!newest) {
      continue; // unreachable: every list has at least the one item that created it
    }
    groups.push({ key, node: newest.node, ip: newest.ip, events: sorted, isFlapping: isIPChurnFlapping(sorted) });
  }
  groups.sort((a, b) => (b.events[0]?.at ?? 0) - (a.events[0]?.at ?? 0));
  return groups;
}

/** Reports whether a (node, ip) group's own events (newest-first) cross
 * IP_FLAP_THRESHOLD within IP_FLAP_WINDOW_SECONDS of the newest one — a
 * flapping binding, distinct from a single clean rebind (which produces
 * exactly one counted transition and never crosses a threshold of 3). */
export function isIPChurnFlapping(eventsNewestFirst: NeighborBinding[]): boolean {
  const [newest] = eventsNewestFirst;
  if (!newest) {
    return false;
  }
  const cutoff = newest.at - IP_FLAP_WINDOW_SECONDS;
  let count = 0;
  for (const e of eventsNewestFirst) {
    // A first-seen row (no prevMac) is a discovery, not a transition — the
    // same exclusion internal/store.NeighborBindingRepo.CountSince applies.
    if (e.prevMac && e.at >= cutoff) {
      count++;
    }
  }
  return count >= IP_FLAP_THRESHOLD;
}

/** One MAC recorded as the owner of MAC_CLAIM_THRESHOLD or more distinct
 * IPs within MAC_CLAIM_WINDOW_SECONDS of the page's own newest event — the
 * "one MAC claiming many IPs" flap direction. */
export interface MacClaim {
  key: string;
  node: string;
  mac: string;
  /** Sorted, deduplicated. */
  ips: string[];
}

/** Finds every MAC currently over MAC_CLAIM_THRESHOLD across the whole
 * fetched page, windowed from the page's own newest event (not wall-clock
 * "now" — this runs over whatever page is currently loaded). */
export function findMacClaims(items: NeighborBinding[]): MacClaim[] {
  const [first] = items;
  if (!first) {
    return [];
  }
  const newestAt = items.reduce((max, i) => (i.at > max ? i.at : max), first.at);
  const cutoff = newestAt - MAC_CLAIM_WINDOW_SECONDS;

  const byKey = new Map<string, { node: string; mac: string; ips: Set<string> }>();
  for (const item of items) {
    if (item.at < cutoff) {
      continue;
    }
    const key = `${item.node}|${item.mac}`;
    const entry = byKey.get(key);
    if (entry) {
      entry.ips.add(item.ip);
    } else {
      byKey.set(key, { node: item.node, mac: item.mac, ips: new Set([item.ip]) });
    }
  }

  const out: MacClaim[] = [];
  for (const [key, entry] of byKey) {
    if (entry.ips.size >= MAC_CLAIM_THRESHOLD) {
      out.push({ key, node: entry.node, mac: entry.mac, ips: [...entry.ips].sort() });
    }
  }
  out.sort((a, b) => b.ips.length - a.ips.length);
  return out;
}
