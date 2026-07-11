// T-602's unified findings-stream API calls (docs/api.md's `GET /findings`
// / `POST /findings/{id}/fix` entries; the exact contract is
// internal/findings.Finding + internal/api/findings.go). Mirrors
// api/drift.ts's shape one-for-one — see that file's own doc comment.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Changeset, StreamFinding } from "./types";

export interface FindingsFilter {
  source?: string;
  severity?: string;
  node?: string;
}

/** GET /findings?source=&severity=&node= — the current unified findings
 * stream, optionally filtered (AC2: filter by source/severity/node works
 * uniformly across every producer). */
export function fetchFindings(filter: FindingsFilter = {}): Promise<StreamFinding[]> {
  const params = new URLSearchParams();
  if (filter.source) params.set("source", filter.source);
  if (filter.severity) params.set("severity", filter.severity);
  if (filter.node) params.set("node", filter.node);
  const qs = params.toString();
  return apiFetch<{ items: StreamFinding[] }>(`/findings${qs ? `?${qs}` : ""}`).then((r) => r.items);
}

/** POST /findings/{id}/fix — creates (and returns) a draft changeset from a
 * fixable finding's server-computed op patch, same contract as
 * fixDriftFinding. */
export function fixFinding(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/findings/${encodeURIComponent(id)}/fix`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}
