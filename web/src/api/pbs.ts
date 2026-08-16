// PBS backup-path awareness (T-1206, internal/api/pbs.go's GET /pbs).
//
// Read-only forever, and read-only in a specific sense: vnprox stores no PBS
// credentials and never talks to a PBS host. Everything here is PVE's own
// knowledge of itself (storage.cfg + backup jobs), correlated to the
// inventory graph. There is no write route and no `pbs.*` changeset op.
//
// docs/api.md has no section for this route (docs/user-guide.md §7.5 is its
// only prose home) — the field notes below are read off
// internal/api/pbs.go's response structs.
import { apiFetch } from "./client";
import type { PbsOverlay } from "./types";

/** GET /pbs — discovered PBS hosts plus every resolved node -> host backup
 * path with its sizing hint. A host whose egress carrier vnprox could not
 * resolve still appears as a host, with no path — the honest "host known,
 * path unresolved" state, which is why `PbsPath.carrier` is optional. */
export function fetchPbsOverlay(): Promise<PbsOverlay> {
  return apiFetch<PbsOverlay>("/pbs");
}
