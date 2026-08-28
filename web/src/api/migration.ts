// SPDX-License-Identifier: Apache-2.0

// T-1507's migration network planner API call (docs/api.md's "Migration
// planner" section; internal/api/migration.go). `POST /migration/preflight`
// is a netRead-gated read (a purely advisory, read-only pre-flight check —
// it never stages, applies, or triggers anything), so — unlike
// api/diagnose.ts's postDiagnose, which may start a real capture session —
// this call sends no CSRF token, mirroring every other plain-read POST
// route in this codebase (e.g. api/simulate.ts's postSimulatePath).
import { apiFetch } from "./client";
import type { MigrationAssessment } from "./types";

export interface MigrationPreflightRequest {
  guest: string;
  targetNode: string;
}

/** POST /migration/preflight {guest, targetNode} — a bandwidth-headroom
 * pre-flight assessment for a live migration/evacuation the operator is
 * about to trigger in PVE itself. Always returns a complete Assessment
 * (verdict/caveats fold in every failure mode server-side) rather than
 * throwing for anything short of a genuine transport/validation error. */
export function postMigrationPreflight(req: MigrationPreflightRequest): Promise<MigrationAssessment> {
  return apiFetch<MigrationAssessment>("/migration/preflight", {
    method: "POST",
    json: req,
  });
}
