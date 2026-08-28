// SPDX-License-Identifier: Apache-2.0

// Blueprint & plugin hub API calls (T-1705; docs/api.md's Hub section).
// The exact contract is internal/hub + internal/api/hub.go.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { HubEntryType, HubIndexResponse, HubInstallRequest, HubInstallResponse } from "./types";

/** GET /hub/index?type= — the registry catalog, optionally filtered to one
 * artifact kind. Each entry is annotated with the informational "vetted"
 * badge; the actual install still runs the full signature + trust gate. */
export function fetchHubIndex(type?: HubEntryType): Promise<HubIndexResponse> {
  const q = type ? `?type=${encodeURIComponent(type)}` : "";
  return apiFetch<HubIndexResponse>(`/hub/index${q}`);
}

/** POST /hub/install — download and install the named entry. For a
 * blueprint this runs T-1107's exact import path; for a plugin it registers
 * through T-1702's capability-scoped registry. An unsigned/untrusted-signer
 * artifact returns a non-success status (unsigned / untrustedSignature)
 * unless the caller re-submits with the matching explicit-trust flag. */
export function installHubItem(req: HubInstallRequest): Promise<HubInstallResponse> {
  return apiFetch<HubInstallResponse>("/hub/install", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}
