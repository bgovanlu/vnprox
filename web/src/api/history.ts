// History playback API calls (docs/api.md's "History" section, T-1007;
// internal/api/history.go's GET /history/events).
import { apiFetch } from "./client";
import type { HistoryEvent, HistoryEventsResponse } from "./types";

/** GET /history/events?fromTs=&toTs= — merged changeset-lifecycle +
 * finding-transition timeline markers, ascending by `at`. fromTs/toTs are
 * unix seconds; both optional (server-side defaults cover "everything
 * since"/"everything up to"), mirroring fetchMetricsHistory's convention. */
export function fetchHistoryEvents(fromTs?: number, toTs?: number): Promise<HistoryEvent[]> {
  const params = new URLSearchParams();
  if (fromTs !== undefined) params.set("fromTs", String(fromTs));
  if (toTs !== undefined) params.set("toTs", String(toTs));
  const qs = params.toString();
  return apiFetch<HistoryEventsResponse>(`/history/events${qs ? `?${qs}` : ""}`).then((r) => r.items);
}
