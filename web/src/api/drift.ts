// SPDX-License-Identifier: Apache-2.0

// Drift detection API calls (docs/api.md §Inventory & topology's `/drift`
// entries; the exact contract is internal/drift.Finding + internal/api/drift.go).
import { ApiError, apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { SpecProposal } from "./gitsync";
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

/** POST /drift/{id}/restore-intent (T-2703) — "bring the cluster back to what
 * the spec declares". Stages a DRAFT changeset and stops; the ops are looked
 * up server-side by finding id and are never sent from here. Validating,
 * applying and confirming remain the operator's separate steps. */
export function restoreIntent(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/drift/${encodeURIComponent(id)}/restore-intent`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}

/** POST /drift/{id}/adopt-reality (T-2703) — "rewrite the document to describe
 * the cluster as it is". Opens (or brings up to date) a pull request and
 * **changes nothing about the cluster**. Answers `501 not_implemented` when the
 * deployment has no write-capable spec repository, `422 nothing_to_propose`
 * when the document already says what the cluster says. */
export function adoptReality(id: string): Promise<SpecProposal> {
  return apiFetch<SpecProposal>(`/drift/${encodeURIComponent(id)}/adopt-reality`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}

/** GET /drift/{id}/adoption — the pull request this finding was already
 * adopted as, or `null` for the daemon's documented `404 not_found` ("this
 * finding has not been adopted").
 *
 * Only a 404 is translated into "not adopted". Every other failure — 403, a
 * 5xx, an unreachable daemon — is rethrown, because "we could not ask" must
 * not render as the definite answer "there is no pull request". */
export async function fetchAdoption(id: string): Promise<SpecProposal | null> {
  try {
    return await apiFetch<SpecProposal>(`/drift/${encodeURIComponent(id)}/adoption`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return null;
    }
    throw err;
  }
}
