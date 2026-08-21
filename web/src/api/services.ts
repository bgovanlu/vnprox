// T-3604: start a stopped SDN service on a node.
//
// The most powerful thing Phase 36 adds, and the narrowest useful version:
// only `start` (never restart/stop/enable), and only the two units
// internal/host.WatchedServices allow-lists. The unit name is validated by
// the daemon and again by the node that runs the command — this client
// sending a plausible-looking name is not what makes it safe.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { ServiceStartResponse } from "./types";

/** POST /services/start — netWrite + CSRF, explicit confirmation, audited
 * per attempt including refusals. `confirm` is hardcoded because the only
 * caller reaches here after the operator has confirmed in a dialog. */
export function startService(node: string, unit: string): Promise<ServiceStartResponse> {
  return apiFetch<ServiceStartResponse>("/services/start", {
    method: "POST",
    json: { node, unit, confirm: true },
    csrfToken: readCsrfCookie(),
  });
}
