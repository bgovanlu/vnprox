// SPDX-License-Identifier: Apache-2.0

// T-602's unified findings-stream API calls (docs/api.md's `GET /findings`
// / `POST /findings/{id}/fix` entries; the exact contract is
// internal/findings.Finding + internal/api/findings.go). Mirrors
// api/drift.ts's shape one-for-one — see that file's own doc comment.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Changeset, FindingAck, StreamFinding } from "./types";

export interface FindingsFilter {
  source?: string;
  severity?: string;
  node?: string;
  /** T-2402. Omitted (the default) returns everything with acknowledgements
   * attached — acknowledgement is not suppression, so the unfiltered stream
   * must never hide a finding. */
  acked?: "only" | "exclude";
}

/** GET /findings?source=&severity=&node= — the current unified findings
 * stream, optionally filtered (AC2: filter by source/severity/node works
 * uniformly across every producer). */
export function fetchFindings(filter: FindingsFilter = {}): Promise<StreamFinding[]> {
  const params = new URLSearchParams();
  if (filter.source) params.set("source", filter.source);
  if (filter.severity) params.set("severity", filter.severity);
  if (filter.node) params.set("node", filter.node);
  if (filter.acked) params.set("acked", filter.acked);
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

/** POST /findings/{id}/ack — records an acknowledgement (T-2402).
 *
 * `reason` is required by the server; `expiresAt` is unix seconds, or omitted
 * for "until explicitly un-acknowledged". The server refuses an expiry that
 * has already passed rather than storing an acknowledgement that never
 * applies. */
export function ackFinding(id: string, reason: string, expiresAt?: number): Promise<FindingAck> {
  return apiFetch<FindingAck>(`/findings/${encodeURIComponent(id)}/ack`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
    json: { reason, expiresAt: expiresAt ?? 0 },
  });
}

/** DELETE /findings/{id}/ack — clears an acknowledgement (T-2402). Works even
 * for a finding that is no longer reported, so a stale row is always
 * clearable. */
export async function unackFinding(id: string): Promise<void> {
  await apiFetch(`/findings/${encodeURIComponent(id)}/ack`, {
    method: "DELETE",
    csrfToken: readCsrfCookie(),
  });
}

/** POST /findings/fix — stages EVERY listed fixable finding into ONE
 * changeset (T-2408).
 *
 * All-or-nothing: the server refuses the whole batch if any id is unknown,
 * unfixable, acknowledged, or conflicts with another in the list. There is no
 * partial-success response to interpret. */
export function batchFixFindings(ids: string[]): Promise<Changeset> {
  return apiFetch<Changeset>("/findings/fix", {
    method: "POST",
    csrfToken: readCsrfCookie(),
    json: { ids },
  });
}
