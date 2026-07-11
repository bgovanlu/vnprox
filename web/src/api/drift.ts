// Drift detection API calls (docs/api.md §Inventory & topology's `/drift`
// entries; the exact contract is internal/drift.Finding + internal/api/drift.go).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Changeset, DriftFinding } from "./types";

/** GET /drift — the current cross-node consistency report. A bare array
 * (not an {items:[...]} envelope), matching docs/api.md's documented
 * shape exactly. */
export function fetchDrift(): Promise<DriftFinding[]> {
  return apiFetch<DriftFinding[]>("/drift");
}

/** POST /drift/{id}/fix — creates (and returns) a draft changeset from a
 * fixable finding's server-computed op patch. The draft still goes through
 * the normal review/validate/apply/confirm flow; this call never applies
 * anything by itself. */
export function fixDriftFinding(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/drift/${encodeURIComponent(id)}/fix`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}
