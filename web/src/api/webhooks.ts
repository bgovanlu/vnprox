// Webhook-registration API calls (T-1104/T-2905; docs/api.md's "Tokens &
// Webhooks" section, internal/api/webhooks.go).
//
// READ THIS BEFORE WIRING A FORM TO IT. All three routes are mounted behind
// `auth.RequireCap("automation")`, and `automation` is the one capability
// `internal/auth.DeriveCapabilities` can never produce — docs/api.md states
// the consequence outright: "these three routes are automation-token-only in
// practice: a browser session alone can never reach them, only a token minted
// via POST /tokens with `automation` in its scopes can."
//
// So a cookie-authenticated SPA gets 403 `forbidden` from every call here.
// This module still exists, and the Settings panel still calls it, because
// the *refusal itself* is the information an operator needs: T-2905 changed
// what a webhook may point at, and an operator whose webhook stopped
// delivering after an upgrade must be able to see which policy refused it
// rather than a generic failure. The panel renders the daemon's own message
// verbatim in both cases — the capability refusal and the destination-policy
// refusal — and never re-implements either rule client-side.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Webhook, WebhookCreateRequest, WebhooksListResponse } from "./types";

/** GET /webhooks — every registration (secret never returned). Rejects with
 * `ApiError` 403 for any caller that does not hold `automation`; the caller is
 * expected to render that refusal, not swallow it into an empty list. An
 * empty list and "you may not look" are different facts. */
export function fetchWebhooks(): Promise<Webhook[]> {
  return apiFetch<WebhooksListResponse>("/webhooks").then((r) => r.items);
}

/** POST /webhooks — register a delivery target.
 *
 * T-2905's destination policy is enforced server-side, twice: `ValidateURL`
 * refuses at registration (plain http, or an IP-literal host that is
 * loopback/RFC1918/link-local/unspecified), and `GuardedClient`'s dial hook
 * re-checks the *resolved* address at every delivery so a rebinding hostname
 * cannot slip past the URL check. Both refusals carry a message naming the
 * config knob that would permit them (`[webhooks] allow_private_targets` /
 * `allow_insecure_targets`). This client performs no address classification
 * of its own — surfacing the daemon's reason is the whole point. */
export function createWebhook(req: WebhookCreateRequest): Promise<Webhook> {
  return apiFetch<Webhook>("/webhooks", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** DELETE /webhooks/{id} — remove a registration; 404 for an unknown id. */
export async function deleteWebhook(id: string): Promise<void> {
  await apiFetch(`/webhooks/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}
