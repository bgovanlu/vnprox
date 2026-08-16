// Automation-token API calls (T-1104/T-2903; docs/api.md's "Tokens &
// Webhooks" section, internal/api/tokens.go).
//
// Until T-3003 these three routes had no client at all: minting a bearer
// token meant a hand-written `curl` carrying the CSRF double-submit header.
// The exact contract, restated because two of its properties are easy to get
// silently wrong:
//
//   - GET /tokens returns only the *caller's own* tokens. The handler filters
//     by `created_by`; a token another user minted is neither listed here nor
//     revocable through DELETE (which 404s rather than 403s, so it does not
//     confirm the id exists).
//   - POST /tokens' `expiresAt` is three-valued. See ApiTokenCreateRequest.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { ApiToken, ApiTokenCreateRequest, ApiTokenCreateResponse, ApiTokensListResponse } from "./types";

/** GET /tokens — the caller's own tokens, newest-first order as the store
 * returns them. No secret is ever included. */
export function fetchTokens(): Promise<ApiToken[]> {
  return apiFetch<ApiTokensListResponse>("/tokens").then((r) => r.items);
}

/** POST /tokens — mint a token. The resolved `token` field in the response is
 * the ONLY time the raw bearer value exists outside the daemon's hash; a
 * caller that drops it cannot recover it.
 *
 * Omitting `expiresAt` from `req` is what produces the documented 90-day
 * default: this function deliberately does not substitute a client-computed
 * timestamp, so the default lives in exactly one place (the daemon's
 * `defaultTokenTTL`) and the UI shows back whatever the daemon decided. */
export function mintToken(req: ApiTokenCreateRequest): Promise<ApiTokenCreateResponse> {
  return apiFetch<ApiTokenCreateResponse>("/tokens", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** DELETE /tokens/{id} — revoke one of the caller's own tokens. Also
 * force-closes any live WS subscription that token authenticated, within the
 * same request. 404 for an id that is not the caller's. */
export async function revokeToken(id: string): Promise<void> {
  await apiFetch(`/tokens/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}
