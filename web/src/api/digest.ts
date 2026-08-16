// T-2807's scheduled digest: `GET`/`PUT /digest/schedule` (docs/api.md
// §"Scheduled digests").
//
// Two contract details this client must not smooth over:
//
//   * `everySec: 0` is **not a cadence**. A daemon that has never had a
//     schedule written answers `200` with the disabled default rather than
//     `404`, and the runner's weekly fallback is only reached for an ENABLED
//     schedule — so a client must never render `0` as "weekly is configured".
//   * `ruleIds` is a **filter** over the existing alert rules, not a second
//     address book. Empty means "every rule", the same no-filter convention
//     the rest of the API uses. A digest carries no target of its own.
//
// `PUT` is a full replace, but every field is optional and an omitted field
// keeps its stored value — so the panel reads first and writes the whole
// object back, which is what the contract asks callers to do.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";

/** The last tick's outcome. `quiet` marks a run that found nothing to
 * report — one line, by design, not a failure. */
export interface DigestRun {
  status: string;
  detail: string;
  periodStart: number;
  periodEnd: number;
  generatedAt: number;
  quiet: boolean;
}

/** `GET`/`PUT /digest/schedule`. `lastRun` is `null` until the first tick has
 * run — typed as nullable rather than optional because the daemon emits the
 * key explicitly. */
export interface DigestSchedule {
  enabled: boolean;
  everySec: number;
  ruleIds: string[];
  updatedBy: string;
  updatedAt: number;
  lastRun: DigestRun | null;
}

/** `PUT /digest/schedule`'s body. Every field optional; an omitted one keeps
 * the stored value. */
export interface DigestScheduleUpdate {
  enabled?: boolean;
  everySec?: number;
  ruleIds?: string[];
}

/** The server's floor for an ENABLED schedule (internal/api/digest.go's
 * minDigestEverySec). A disabled schedule may carry any cadence, including
 * none — disabling is how an operator silences a digest without losing the
 * cadence they chose. */
export const MIN_DIGEST_EVERY_SEC = 3600;

/** GET /digest/schedule — `netRead`. A deployment with no digest store
 * answers `501 not_implemented`; that is a deployment fact, not an empty
 * schedule, and must not render as one. */
export function fetchDigestSchedule(): Promise<DigestSchedule> {
  return apiFetch<DigestSchedule>("/digest/schedule");
}

/** PUT /digest/schedule — `netWrite` + CSRF. Returns the stored value, which
 * is what the caller should re-render rather than the body it just sent. */
export function putDigestSchedule(update: DigestScheduleUpdate): Promise<DigestSchedule> {
  return apiFetch<DigestSchedule>("/digest/schedule", {
    method: "PUT",
    json: update,
    csrfToken: readCsrfCookie(),
  });
}
