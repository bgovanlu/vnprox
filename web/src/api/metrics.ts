// Metrics API calls (docs/features/monitoring.md §1-2; internal/api/metrics.go's
// GET /metrics/live, GET /metrics/history).
import { apiFetch } from "./client";
import type { HistoryPoint, LiveMetric, MetricsHistoryResponse, MetricsLiveResponse } from "./types";

/** GET /metrics/live?refs=a,b,c — current rates for the given entities. An
 * empty `refs` array short-circuits to `[]` without a request: there is
 * nothing meaningful to ask the server for. Refs the sampler hasn't yet
 * seen twice (no rate computable) are simply absent from the result — see
 * internal/metrics.Sampler.Live's doc comment — not an error. */
export function fetchMetricsLive(refs: string[]): Promise<LiveMetric[]> {
  if (refs.length === 0) return Promise.resolve([]);
  const qs = `?refs=${encodeURIComponent(refs.join(","))}`;
  return apiFetch<MetricsLiveResponse>(`/metrics/live${qs}`).then((r) => r.items);
}

/** GET /metrics/history?ref=&fromTs=&toTs= — 24h-ring rate history for one
 * entity. fromTs/toTs are unix seconds; both optional (server-side
 * defaults cover "everything since"/"everything up to"). */
export function fetchMetricsHistory(ref: string, fromTs?: number, toTs?: number): Promise<HistoryPoint[]> {
  const params = new URLSearchParams({ ref });
  if (fromTs !== undefined) params.set("fromTs", String(fromTs));
  if (toTs !== undefined) params.set("toTs", String(toTs));
  return apiFetch<MetricsHistoryResponse>(`/metrics/history?${params.toString()}`).then((r) => r.items);
}
