// Protected-interfaces API calls (docs/api.md §"Protected interfaces";
// internal/api/protected.go). Backs the onboarding walkthrough's step 2
// (docs/user-guide.md §1.2: "vnprox detected which interfaces carry each
// node's management IP and corosync traffic. Confirm these...").
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type {
  ProtectedInterfacesPutRequest,
  ProtectedInterfacesResponse,
  ProtectedInterfacesSuggestResponse,
} from "./types";

/** GET /protected-interfaces — the currently confirmed set (empty `nodes`
 * map before anyone has ever confirmed one, not a 404 — see
 * internal/api/protected.go's handleGetProtected). */
export function fetchProtectedInterfaces(): Promise<ProtectedInterfacesResponse> {
  return apiFetch<ProtectedInterfacesResponse>("/protected-interfaces");
}

/** GET /protected-interfaces/suggest — the detection-suggested set
 * (inventory + corosync.conf), in the same shape the PUT accepts. */
export function fetchProtectedInterfacesSuggest(): Promise<ProtectedInterfacesSuggestResponse> {
  return apiFetch<ProtectedInterfacesSuggestResponse>("/protected-interfaces/suggest");
}

/** PUT /protected-interfaces — replace the confirmed set (netWrite + CSRF).
 * A `400 validation_failed` with `details.refs` on a bad ref is surfaced as
 * a plain ApiError; callers show it as-is. */
export function saveProtectedInterfaces(req: ProtectedInterfacesPutRequest): Promise<ProtectedInterfacesResponse> {
  return apiFetch<ProtectedInterfacesResponse>("/protected-interfaces", {
    method: "PUT",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}
