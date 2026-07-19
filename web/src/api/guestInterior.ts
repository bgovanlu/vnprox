// Guest network interior inspector API calls (docs/api.md's Guest interior
// section, T-1304; internal/api/guestinterior.go). Like the annotations/
// layouts routes, the toggle is app-owned UI preference state, not a
// network-config mutation — mountGuestInteriorRoutes never installs
// CSRFMiddleware, so PUT still sends whatever CSRF cookie is available
// (harmless, matching annotations.ts's own precedent) rather than
// asserting one is required.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { GuestInterior, GuestInteriorToggle } from "./types";

/** GET /guests/{ref}/interior-toggle — current opt-in state (off by
 * default for a guest that has never been toggled). */
export function fetchGuestInteriorToggle(ref: string): Promise<GuestInteriorToggle> {
  return apiFetch<GuestInteriorToggle>(`/guests/${encodeURIComponent(ref)}/interior-toggle`);
}

/** PUT /guests/{ref}/interior-toggle {enabled} — flips the opt-in. */
export function setGuestInteriorToggle(ref: string, enabled: boolean): Promise<GuestInteriorToggle> {
  return apiFetch<GuestInteriorToggle>(`/guests/${encodeURIComponent(ref)}/interior-toggle`, {
    method: "PUT",
    json: { enabled },
    csrfToken: readCsrfCookie(),
  });
}

/** GET /guests/{ref}/interior — the interior read set. Throws ApiError
 * (code `interior_not_enabled`, status 404) when the toggle is off; the
 * caller (useGuestInteriorQuery) only ever fetches this once the toggle
 * query already reports `enabled: true`, but a stale cache can still race
 * this, so callers must handle the 404 rather than assume it can't
 * happen. */
export function fetchGuestInterior(ref: string): Promise<GuestInterior> {
  return apiFetch<GuestInterior>(`/guests/${encodeURIComponent(ref)}/interior`);
}
