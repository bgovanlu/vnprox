// SPDX-License-Identifier: Apache-2.0

// Switch counters (SNMP) API calls (docs/api.md's "Switch counters (SNMP)"
// section, T-4013; internal/api/ifcounters.go). Node-local only (no cluster
// fan-out) — the same documented scope api/mtuprobe.ts/api/latmesh.ts
// already carry. Mirrors alertrules.ts's convention: one function per
// route, mutations carry the CSRF token.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { SNMPCounterResult, SNMPCounterResults, SNMPTarget, SNMPTargetRequest, SNMPTargetsResponse } from "./types";

/** GET /snmp/counters — every currently-known polled port result (current
 * state, not a history). A switch/port not yet polled, or with no enabled
 * target, simply has no item with `state: "ok"` — see SNMPCounterState's
 * doc comment for the honest not_configured/unreachable/no_counters split. */
export function fetchSNMPCounterResults(): Promise<SNMPCounterResult[]> {
  return apiFetch<SNMPCounterResults>("/snmp/counters").then((r) => r.items);
}

/** GET /snmp/targets — every configured per-switch poll target (community
 * never included, see SNMPTarget.hasCommunity). */
export function fetchSNMPTargets(): Promise<SNMPTarget[]> {
  return apiFetch<SNMPTargetsResponse>("/snmp/targets").then((r) => r.items);
}

/** PUT /snmp/targets/{chassisId} — create or replace one switch's poll
 * config, keyed by its LLDP chassisId. `req.community` is three-way-
 * nullable: omit to leave the existing community untouched (update only —
 * a first-time create with no community starts unpollable), `""` to clear
 * it, non-empty to replace it. */
export function putSNMPTarget(chassisId: string, req: SNMPTargetRequest): Promise<SNMPTarget> {
  return apiFetch<SNMPTarget>(`/snmp/targets/${encodeURIComponent(chassisId)}`, {
    method: "PUT",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /snmp/targets/{chassisId} — remove a switch's poll config;
 * resolves whether or not it previously existed (the backend's delete is
 * idempotent). */
export async function deleteSNMPTarget(chassisId: string): Promise<void> {
  await apiFetch(`/snmp/targets/${encodeURIComponent(chassisId)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}
