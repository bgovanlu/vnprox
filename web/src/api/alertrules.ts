// Alert Rules API calls (docs/api.md's "Alert Rules" section; the exact
// contract is internal/api/alertrules.go). Mirrors blueprints.ts's
// convention: one function per route, mutations carry the CSRF token.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type {
  AlertDeliveriesListResponse,
  AlertRule,
  AlertRuleRequest,
  AlertRuleTestResponse,
  AlertRulesListResponse,
} from "./types";

/** GET /alert-rules — every configured routing rule (secrets never
 * included, see AlertRule.hasSecret). */
export function fetchAlertRules(): Promise<AlertRulesListResponse> {
  return apiFetch<AlertRulesListResponse>("/alert-rules");
}

/** GET /alert-rules/{id} — single rule detail. */
export function fetchAlertRule(id: string): Promise<AlertRule> {
  return apiFetch<AlertRule>(`/alert-rules/${encodeURIComponent(id)}`);
}

/** POST /alert-rules — create a rule. */
export function createAlertRule(req: AlertRuleRequest): Promise<AlertRule> {
  return apiFetch<AlertRule>("/alert-rules", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** PUT /alert-rules/{id} — update a rule. `req.targetSecret` is
 * three-way-nullable: omit to leave the existing secret untouched, pass
 * `""` to clear it, or a non-empty string to replace it. */
export function updateAlertRule(id: string, req: AlertRuleRequest): Promise<AlertRule> {
  return apiFetch<AlertRule>(`/alert-rules/${encodeURIComponent(id)}`, {
    method: "PUT",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /alert-rules/{id} — remove a rule; resolves whether or not it
 * previously existed (the backend's delete is idempotent). */
export async function deleteAlertRule(id: string): Promise<void> {
  await apiFetch(`/alert-rules/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}

/** POST /alert-rules/{id}/test — deliver a synthetic test finding through
 * the rule's target now; always resolves with the outcome (HTTP 200 either
 * way once the rule is found — a failed test delivery is not thrown as an
 * ApiError). */
export function testAlertRule(id: string): Promise<AlertRuleTestResponse> {
  return apiFetch<AlertRuleTestResponse>(`/alert-rules/${encodeURIComponent(id)}/test`, {
    method: "POST",
    csrfToken: readCsrfCookie(),
  });
}

/** GET /alert-deliveries?ruleId=&status= — the delivery log, both filters
 * optional. */
export function fetchAlertDeliveries(filters: { ruleId?: string; status?: string } = {}): Promise<AlertDeliveriesListResponse> {
  const params = new URLSearchParams();
  if (filters.ruleId) params.set("ruleId", filters.ruleId);
  if (filters.status) params.set("status", filters.status);
  const qs = params.toString();
  return apiFetch<AlertDeliveriesListResponse>(`/alert-deliveries${qs ? `?${qs}` : ""}`);
}
