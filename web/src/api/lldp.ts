// SPDX-License-Identifier: Apache-2.0

// LLDP neighbor list + guided install API calls (docs/api.md §"Inventory &
// topology"'s GET /lldp row and §"LLDP guided install (T-605)"). Backs the
// onboarding walkthrough's step 3 (docs/user-guide.md §1.3).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { LldpInstallResponse, LldpResponse, PortsResponse } from "./types";

/** GET /ports — the flat ports table (every cluster NIC ↔ the switch/port
 * LLDP reports for it). `?format=csv` is a separate download link, not this
 * JSON call. */
export function fetchPorts(): Promise<PortsResponse> {
  return apiFetch<PortsResponse>("/ports");
}

/** GET /lldp — all LLDP neighbors this daemon knows about. An empty
 * `items` array is the walkthrough's signal that lldpd may not be running
 * yet (docs/user-guide.md §1: "if lldpd isn't running..."), not an error. */
export function fetchLldpNeighbors(): Promise<LldpResponse> {
  return apiFetch<LldpResponse>("/lldp");
}

/** POST /lldp/install {"confirm": true} — installs and enables lldpd
 * locally, then fans out to every reachable peer (netWrite + CSRF).
 * `confirm` must literally be `true`; the caller (the walkthrough's LLDP
 * step) only calls this after an explicit user click, so it is hardcoded
 * here rather than accepted as a parameter. */
export function installLldp(): Promise<LldpInstallResponse> {
  return apiFetch<LldpInstallResponse>("/lldp/install", {
    method: "POST",
    json: { confirm: true },
    csrfToken: readCsrfCookie(),
  });
}
