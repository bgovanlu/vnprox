// SPDX-License-Identifier: Apache-2.0

// T-3603: re-run a collector poll now.
//
// Phase 36's read-only operational tier — vnprox re-reading its own inputs,
// not writing to any node — so this is netWrite + CSRF gated and audited,
// but carries no `confirm` flag: there is nothing to confirm, and a dialog
// asking "re-read the cluster?" would only dilute the ones that matter.
import { apiFetch, ApiError } from "./client";
import { readCsrfCookie } from "./auth";
import type { CollectorRefreshResponse } from "./types";

/** POST /collectors/refresh — re-runs the poll cycle, optionally scoped to
 * one node. A poll that fails still resolves (with `error` set): "it failed
 * again, and here is the same message" is the answer the retry button
 * exists to produce, and it is data rather than a transport failure. */
export function refreshCollectors(node?: string): Promise<CollectorRefreshResponse> {
  return apiFetch<CollectorRefreshResponse>("/collectors/refresh", {
    method: "POST",
    json: node === undefined || node === "" ? {} : { node },
    csrfToken: readCsrfCookie(),
  });
}

/** True when a rejected refresh was the server's rate limit rather than a
 * real failure. The limit is enforced server-side (a client-side throttle
 * would protect PVE only from clients that implement one), so the UI has to
 * be able to tell "too soon" from "broken" and say so. */
export function isRateLimited(err: unknown): boolean {
  return err instanceof ApiError && err.status === 429;
}
