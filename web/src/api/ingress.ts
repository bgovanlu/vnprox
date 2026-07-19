// Ingress visibility API calls (docs/api.md's "Ingress visibility" section;
// internal/api/ingress.go, T-1406). Read-only discovery: only
// POST/DELETE /ingress/targets mutate anything, and only the operator's own
// target list, never the discovered proxy itself. Mirrors alertrules.ts's
// convention: one function per route, mutations carry the CSRF token.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { IngressStatusView, IngressTarget, IngressTargetCreateRequest, IngressTargetsListResponse } from "./types";

/** GET /ingress/targets — every configured discovery target (credential
 * never included, see IngressTarget.hasCredential). */
export function fetchIngressTargets(): Promise<IngressTargetsListResponse> {
  return apiFetch<IngressTargetsListResponse>("/ingress/targets");
}

/** POST /ingress/targets — add a target. */
export function createIngressTarget(req: IngressTargetCreateRequest): Promise<IngressTarget> {
  return apiFetch<IngressTarget>("/ingress/targets", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** DELETE /ingress/targets/{id} — remove a target; resolves whether or not
 * it previously existed (the backend's delete is idempotent). */
export async function deleteIngressTarget(id: string): Promise<void> {
  await apiFetch(`/ingress/targets/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}

/** GET /ingress/status — discover every target fresh and correlate the
 * WAN -> port-forward -> proxy guest -> backend guest chain. */
export function fetchIngressStatus(): Promise<IngressStatusView> {
  return apiFetch<IngressStatusView>("/ingress/status");
}
