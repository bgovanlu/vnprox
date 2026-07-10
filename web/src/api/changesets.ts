// Changesets API calls (docs/api.md §Changesets — "the only write path").
// Every mutation in the app must go through one of these functions; nothing
// else in web/src may construct a raw fetch against /changesets*.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type {
  ApplyChangesetRequest,
  Changeset,
  ChangesetDiff,
  CreateChangesetRequest,
  UpdateChangesetRequest,
} from "./types";

/** GET /changesets?status= — list, optionally filtered by status. An empty
 * status means "no filter" (every non-terminal and terminal changeset). */
export function listChangesets(status?: string): Promise<Changeset[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : "";
  return apiFetch<Changeset[]>(`/changesets${qs}`);
}

/** GET /changesets/{id} — full changeset incl. findings, plan, apply log. */
export function getChangeset(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/changesets/${encodeURIComponent(id)}`);
}

/** POST /changesets — create a draft `{title, ops}`; the response already
 * carries freshly-computed findings (T-202: validation runs on every draft
 * change). */
export function createChangeset(req: CreateChangesetRequest): Promise<Changeset> {
  return apiFetch<Changeset>("/changesets", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** PUT /changesets/{id} — replace a draft's ops (and optionally rename it).
 * Revalidates automatically; only legal while the changeset is still
 * draft/validated (internal/change.Changeset.Editable). */
export function updateChangeset(id: string, req: UpdateChangesetRequest): Promise<Changeset> {
  return apiFetch<Changeset>(`/changesets/${encodeURIComponent(id)}`, {
    method: "PUT",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /changesets/{id} — discard a draft. */
export async function discardChangeset(id: string): Promise<void> {
  await apiFetch(`/changesets/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}

/** POST /changesets/{id}/validate — re-run validation (state may have moved
 * since the draft was last edited), returns the changeset with fresh
 * findings and, if clean, promotes status draft -> validated. */
export function validateChangeset(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/changesets/${encodeURIComponent(id)}/validate`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}

/** GET /changesets/{id}/diff — rendered diff: per-file unified diffs (one
 * per touched node) + structured op summaries, for the review screen's
 * File diff / Summary tabs. */
export function diffChangeset(id: string): Promise<ChangesetDiff> {
  return apiFetch<ChangesetDiff>(`/changesets/${encodeURIComponent(id)}/diff`);
}

/** POST /changesets/{id}/apply — `{confirmTimeoutSec}` -> 202, status
 * `applying` -> `awaiting_confirm`. Defaults to the documented 120s window
 * (docs/features/change-management.md §4: "Default confirm window 120s"). */
export function applyChangeset(id: string, req: ApplyChangesetRequest = { confirmTimeoutSec: 120 }): Promise<Changeset> {
  return apiFetch<Changeset>(`/changesets/${encodeURIComponent(id)}/apply`, {
    method: "POST",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}

/** POST /changesets/{id}/confirm — commit within the countdown window. */
export function confirmChangeset(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/changesets/${encodeURIComponent(id)}/confirm`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}

/** POST /changesets/{id}/rollback — manual rollback; also valid on an
 * already-`committed` changeset within the 7-day retention window
 * (docs/features/change-management.md §4), where it creates a brand-new
 * restoring changeset rather than mutating this one. */
export function rollbackChangeset(id: string): Promise<Changeset> {
  return apiFetch<Changeset>(`/changesets/${encodeURIComponent(id)}/rollback`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}
