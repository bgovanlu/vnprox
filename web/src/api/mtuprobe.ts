// SPDX-License-Identifier: Apache-2.0

// Path MTU prober API calls (docs/api.md's Path MTU prober section, T-1306;
// internal/api/mtuprobe.go's GET /mtuprobe/results). Node-local only (no
// cluster fan-out) — see that section's own doc comment for why, mirroring
// api/latmesh.ts's identical scope note.
import { apiFetch } from "./client";
import type { MTUProbeResult, MTUProbeResults } from "./types";

/** GET /mtuprobe/results — every currently-known verified per-link MTU
 * reading this node has probed. */
export function fetchMTUProbeResults(): Promise<MTUProbeResult[]> {
  return apiFetch<MTUProbeResults>("/mtuprobe/results").then((r) => r.items);
}
