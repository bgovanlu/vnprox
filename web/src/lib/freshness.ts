// T-2005's "cached views are labeled with their age" mechanism: tracks the
// timestamp of the most recent successful query response, app-wide, by
// subscribing to the shared QueryClient's cache — the one place every
// query/mutation in this app already flows through (queryClient.ts's own
// doc comment: "the one QueryClient for the app"). No per-page bookkeeping
// is needed because this hooks the SAME cache every page's useQuery already
// populates.
import { useSyncExternalStore } from "react";
import { queryClient } from "./queryClient";

let lastSuccessAt: number | null = null;
const listeners = new Set<() => void>();

queryClient.getQueryCache().subscribe((event) => {
  if (event.type !== "updated") return;
  const state = event.query.state;
  if (state.status !== "success" || !state.dataUpdatedAt) return;
  lastSuccessAt = state.dataUpdatedAt;
  for (const listener of listeners) listener();
});

function subscribe(onStoreChange: () => void): () => void {
  listeners.add(onStoreChange);
  return () => {
    listeners.delete(onStoreChange);
  };
}

function getSnapshot(): number | null {
  return lastSuccessAt;
}

/** The unix-millis timestamp of the most recent successful query response
 * anywhere in the app, or null before the first one lands. */
export function useLastSuccessAt(): number | null {
  return useSyncExternalStore(subscribe, getSnapshot);
}

/** True while the browser reports itself online (`navigator.onLine`),
 * updated live via the `online`/`offline` window events. This is a
 * NETWORK-REACHABILITY heuristic, not a guarantee — a browser can report
 * `true` while genuinely unable to reach vnproxd (e.g. Wi-Fi connected to
 * a router with no upstream) — which is exactly why OfflineShellBanner
 * additionally keys off query staleness/errors rather than trusting this
 * alone. It is the fast, zero-latency first signal; the slower, more
 * accurate one is "did the last request actually succeed".
 */
function subscribeOnline(onStoreChange: () => void): () => void {
  window.addEventListener("online", onStoreChange);
  window.addEventListener("offline", onStoreChange);
  return () => {
    window.removeEventListener("online", onStoreChange);
    window.removeEventListener("offline", onStoreChange);
  };
}

export function useOnlineStatus(): boolean {
  return useSyncExternalStore(subscribeOnline, () => navigator.onLine, () => true);
}

/** formatAge renders a unix-millis timestamp as a short, human relative
 * age ("just now", "3m ago", "2h ago", or a locale date/time beyond a
 * day) — the wording OfflineShellBanner uses so "stale" has a magnitude
 * attached rather than being a bare fact. now defaults to Date.now() but
 * is injectable for deterministic tests.
 */
export function formatAge(timestampMs: number, now: number = Date.now()): string {
  const deltaSec = Math.max(0, Math.floor((now - timestampMs) / 1000));
  if (deltaSec < 60) return "just now";
  const deltaMin = Math.floor(deltaSec / 60);
  if (deltaMin < 60) return `${String(deltaMin)}m ago`;
  const deltaHour = Math.floor(deltaMin / 60);
  if (deltaHour < 24) return `${String(deltaHour)}h ago`;
  return new Date(timestampMs).toLocaleString();
}
