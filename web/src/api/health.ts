// SPDX-License-Identifier: Apache-2.0

import { apiFetch } from "./client";
import type { HealthResponse } from "./types";

/** GET /health — the daemon's liveness/version/collector-staleness
 * snapshot, and (T-2801) whether it is running in demo mode.
 *
 * The one unauthenticated route in the API, which is why the demo flag
 * lives on it: the demo banner has to render on the login screen too, and
 * "log in to find out whether this is real" tells a visitor nothing. */
export function fetchHealth(): Promise<HealthResponse> {
  return apiFetch<HealthResponse>("/health");
}
