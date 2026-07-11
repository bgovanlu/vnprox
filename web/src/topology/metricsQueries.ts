// Live per-ref metrics for the map's "traffic" paint mode and the entity
// inspector's sparklines/counters (docs/features/monitoring.md §1):
// GET /metrics/live seeds the initial snapshot, then the shared /api/ws
// connection's `metrics:<ref>` topic (one per ref, per docs/api.md's
// "subscription-scoped — only subscribed refs stream" contract) keeps it
// live without polling. Mirrors queries.ts's useTopologyWsBridge pattern.
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchMetricsHistory, fetchMetricsLive } from "../api/metrics";
import { createWsClient, defaultWsUrl, type WsClient, type WsServerEvent } from "../api/ws";
import type { HistoryPoint, LiveMetric, MetricsSampleEvent, Rates } from "../api/types";
import { computeUtilizationPct } from "./trafficMode";

export const metricsLiveKey = (refs: readonly string[]) => ["metrics-live", ...refs] as const;
export const metricsHistoryKey = (ref: string) => ["metrics-history", ref] as const;

/** How far back the inspector's sparkline looks (docs/features/monitoring.md
 * §2: "24h ring ... nothing longer" is the *storage* ceiling; the sparkline
 * itself defaults to the last hour, a readable window for spotting a
 * recent spike — the history endpoint is happy to serve up to 24h if a
 * future wider view wants it). */
const HISTORY_LOOKBACK_SEC = 3600;
const HISTORY_REFETCH_MS = 30_000;

/** GET /metrics/history for one entity's last hour, refetched periodically
 * while the inspector is open (there is no WS event for history — only the
 * live rate stream is pushed). Disabled entirely for a ref with no
 * metrics-bearing kind (see METRICS_KINDS) or no ref selected. */
export function useMetricsHistoryQuery(ref: string | undefined, enabled: boolean): { data: HistoryPoint[] | undefined; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: metricsHistoryKey(ref ?? ""),
    queryFn: () => {
      const now = Math.floor(Date.now() / 1000);
      return fetchMetricsHistory(ref ?? "", now - HISTORY_LOOKBACK_SEC, now);
    },
    enabled: enabled && ref !== undefined,
    staleTime: 15_000,
    refetchInterval: enabled && ref !== undefined ? HISTORY_REFETCH_MS : false,
  });
  return { data, isLoading };
}

/** Safety-net refetch interval: the WS bridge is the primary freshness
 * mechanism, but a missed/raced subscribe (e.g. right after a reconnect)
 * shouldn't leave a ref's data stale forever. */
const SAFETY_REFETCH_MS = 30_000;

function isRates(v: unknown): v is Rates {
  return (
    typeof v === "object" &&
    v !== null &&
    typeof (v as Rates).rxBps === "number" &&
    typeof (v as Rates).txBps === "number"
  );
}

/** Runtime guard for the `metrics.sample` WS event (docs/api.md:
 * `{ref, at, rates}`), the same isXxxEvent idiom queries.ts's
 * isTopologyDeltaEvent uses. Exported for direct unit testing. */
export function isMetricsSampleEvent(evt: WsServerEvent): evt is WsServerEvent & MetricsSampleEvent {
  return (
    evt.event === "metrics.sample" &&
    typeof evt.ref === "string" &&
    typeof evt.at === "number" &&
    isRates(evt.rates)
  );
}

let sharedWsClient: WsClient | undefined;
function getSharedWsClient(): WsClient {
  sharedWsClient ??= createWsClient({ url: defaultWsUrl });
  return sharedWsClient;
}

/**
 * Seeds `refs`' current LiveMetric from GET /metrics/live, then keeps them
 * fresh via one `metrics:<ref>` WS subscription per ref. A `metrics.sample`
 * push carries only `{ref, at, rates}` (no speed/utilization), so
 * utilization is recomputed client-side (computeUtilizationPct) from the
 * link speed the last full fetch reported — see that function's doc
 * comment. Returns an empty map when `enabled` is false or `refs` is empty
 * (traffic mode off, or nothing visible to sample), and unsubscribes from
 * every topic once disabled/unmounted. Pass `client` in tests to inject a
 * client wired to a real `ws` test server instead of the shared singleton.
 */
export function useLiveMetrics(refs: readonly string[], enabled: boolean, client?: WsClient): ReadonlyMap<string, LiveMetric> {
  const sortedRefs = useMemo(() => Array.from(new Set(refs)).sort(), [refs]);
  const refsKey = sortedRefs.join(",");

  const { data } = useQuery({
    queryKey: metricsLiveKey(sortedRefs),
    queryFn: () => fetchMetricsLive(sortedRefs),
    enabled: enabled && sortedRefs.length > 0,
    staleTime: 5_000,
    refetchInterval: enabled && sortedRefs.length > 0 ? SAFETY_REFETCH_MS : false,
  });

  const [live, setLive] = useState<Map<string, LiveMetric>>(new Map());

  // Drop stale entries and seed/replace from every fresh fetch.
  useEffect(() => {
    if (!enabled || sortedRefs.length === 0) {
      setLive(new Map());
      return;
    }
    if (!data) return;
    setLive((prev) => {
      const next = new Map(prev);
      for (const m of data) next.set(m.ref, m);
      return next;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, enabled, refsKey]);

  useEffect(() => {
    if (!enabled || sortedRefs.length === 0) return;
    const ws = client ?? getSharedWsClient();
    const topics = sortedRefs.map((ref) => `metrics:${ref}`);
    const unsubscribe = ws.subscribe(topics, (evt) => {
      if (!isMetricsSampleEvent(evt)) return;
      setLive((prev) => {
        const existing = prev.get(evt.ref);
        const speedMbps = existing?.speedMbps;
        const rxUtilPct = computeUtilizationPct(evt.rates.rxBps, speedMbps);
        const txUtilPct = computeUtilizationPct(evt.rates.txBps, speedMbps);
        const next = new Map(prev);
        next.set(evt.ref, {
          ref: evt.ref,
          at: evt.at,
          rates: evt.rates,
          speedMbps,
          rxUtilPct,
          txUtilPct,
          utilizationPct: Math.max(rxUtilPct, txUtilPct),
          slaves: existing?.slaves,
        });
        return next;
      });
    });
    return unsubscribe;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, refsKey, client]);

  return live;
}

/** Projects a live-metrics map down to ref -> utilizationPct (traffic
 * mode's toFlowElements input) — a plain function, not a hook, so
 * TopologyPage can memoize it independently of useLiveMetrics' own
 * re-render cadence. */
export function utilizationMap(live: ReadonlyMap<string, LiveMetric>): ReadonlyMap<string, number> {
  const out = new Map<string, number>();
  for (const [ref, m] of live) {
    if (m.utilizationPct !== undefined) out.set(ref, m.utilizationPct);
  }
  return out;
}
