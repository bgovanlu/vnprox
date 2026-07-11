// Path simulator API call (docs/api.md §"Path simulator"; T-503's
// `POST /simulate/path`). netRead-gated but read-only — a static analysis
// over the poll-cached inventory snapshot that mutates nothing, so unlike
// every write in web/src/api/changesets.ts this deliberately does not send
// an X-VNPROX-CSRF token: internal/api/simulate.go's mountSimulateRoutes
// never installs CSRFMiddleware on this route (confirmed by reading that
// file — it only requires the session + netRead capability), matching its
// read-only nature.
import { apiFetch } from "./client";
import type { SimulateRequest, SimulateResult } from "./types";

export function simulatePath(req: SimulateRequest): Promise<SimulateResult> {
  return apiFetch<SimulateResult>("/simulate/path", { method: "POST", json: req });
}
