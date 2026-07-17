// Path simulator API call (docs/api.md §"Path simulator"; T-503's
// `POST /simulate/path`). netRead-gated but read-only — a static analysis
// over the poll-cached inventory snapshot that mutates nothing, so unlike
// every write in web/src/api/changesets.ts this deliberately does not send
// an X-VNPROX-CSRF token: internal/api/simulate.go's mountSimulateRoutes
// never installs CSRFMiddleware on this route (confirmed by reading that
// file — it only requires the session + netRead capability), matching its
// read-only nature.
import { apiFetch } from "./client";
import type { SimulateRequest, SimulateResult, VerifyEligibility, VerifyResult } from "./types";

export function simulatePath(req: SimulateRequest): Promise<SimulateResult> {
  return apiFetch<SimulateResult>("/simulate/path", { method: "POST", json: req });
}

// T-806's "Verify live" action (docs/api.md §"Live path probe (T-802)").
// Like /simulate/path, mountSimulateRoutes never installs CSRFMiddleware on
// either of these routes (both are netRead-gated reads from this daemon's
// own perspective — the live probe reaches into a guest but never mutates
// network config), so neither call sends an X-VNPROX-CSRF token.

export function simulateVerify(req: SimulateRequest): Promise<VerifyResult> {
  return apiFetch<VerifyResult>("/simulate/verify", { method: "POST", json: req });
}

export function simulateVerifyEligibility(ref: string): Promise<VerifyEligibility> {
  return apiFetch<VerifyEligibility>(`/simulate/verify/eligibility?ref=${encodeURIComponent(ref)}`);
}
