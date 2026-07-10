// Audit-log API calls (docs/api.md "Audit", T-206).
import { apiFetch } from "./client";

/** One audit row (docs/api.md audit shape). `detail` is the
 * action-specific structured detail object, opaque to the client. */
export interface AuditEntry {
  id: number;
  at: number;
  username: string;
  action: string;
  target?: string;
  changesetId?: string;
  result: string;
  detail?: Record<string, unknown>;
}

export interface AuditListResponse {
  items: AuditEntry[];
  nextCursor?: string;
}

/** Filters for GET /audit (docs/features/change-management.md §8: user,
 * date range, target, result). All optional; from/to are unix seconds. */
export interface AuditFilter {
  user?: string;
  action?: string;
  target?: string;
  result?: string;
  changesetId?: string;
  from?: number;
  to?: number;
}

/** Builds the /audit query string for a filter + cursor page request.
 * Exported for unit tests. */
export function auditQueryString(filter: AuditFilter, cursor?: string, limit = 50): string {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  if (filter.user) params.set("user", filter.user);
  if (filter.action) params.set("action", filter.action);
  if (filter.target) params.set("target", filter.target);
  if (filter.result) params.set("result", filter.result);
  if (filter.changesetId) params.set("changesetId", filter.changesetId);
  if (filter.from !== undefined) params.set("from", String(filter.from));
  if (filter.to !== undefined) params.set("to", String(filter.to));
  if (cursor) params.set("cursor", cursor);
  return params.toString();
}

/** GET /audit — one filtered page, newest first. */
export function fetchAudit(filter: AuditFilter, cursor?: string, limit = 50): Promise<AuditListResponse> {
  return apiFetch<AuditListResponse>(`/audit?${auditQueryString(filter, cursor, limit)}`);
}
