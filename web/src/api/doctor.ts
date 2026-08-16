// `GET /doctor/live` (T-2406; internal/api/doctor.go, internal/doctor/live.go).
//
// SCOPE, because the number is easy to overstate. `vnproxctl doctor` runs TEN
// checks. This route runs exactly `internal/doctor.LiveChecks` — the FOUR that
// cannot be answered without a credential only the running daemon holds
// (`pve_reachable`, `pve_privileges`, `peer_secret`, `clock_skew`). The other
// six are CLI-local observations of the machine the CLI is standing on (file
// modes, port conflicts, disk headroom, ...) and the daemon deliberately does
// not answer them on a CLI's behalf — `RunLive`'s own doc comment says mixing
// the two "would blur which half of a report came from where".
//
// Two of the four still report `skip` by design on a normal deployment
// (`T-2406-followup-01`/`-02`). That is the reason this module refuses to
// collapse the status space: a skipped check is not a passing one, and the
// generalisation in planning/tasks/phase-29.md's wave-4 record — "an absent,
// skipped, or unknown state rendering as a definite one" — is the defect
// family this arc keeps finding.
import { apiFetch } from "./client";
import type { DoctorLiveResponse, DoctorResult, DoctorStatus } from "./types";

/** The four statuses `internal/doctor.Status` defines, in report order. */
export const DOCTOR_STATUSES: readonly DoctorStatus[] = ["pass", "warn", "fail", "skip"];

/** Runtime narrowing for the wire's `status` string. Returns `undefined` for
 * anything this build does not recognise, so an unfamiliar status renders as
 * an explicit unknown rather than falling through a `switch` default into
 * whatever styling happens to be last. `Report.Validate` rejects an unknown
 * status server-side, but the client must not *depend* on that: this route
 * hands back `RunLive`'s slice directly, without going through `Validate`. */
export function asDoctorStatus(status: string): DoctorStatus | undefined {
  return DOCTOR_STATUSES.find((s) => s === status);
}

/** GET /doctor/live (`audit` capability) — the four daemon-credentialed check
 * results, in `LiveChecks` order.
 *
 * Rejects with `ApiError`: 403 when the session lacks the `audit` capability
 * (the same gate `/audit` and the support bundle use), 404 when the route is
 * not mounted at all (no live-check service wired). Neither is a check
 * failure, and the caller must not render either as one. */
export function fetchDoctorLive(): Promise<DoctorResult[]> {
  return apiFetch<DoctorLiveResponse>("/doctor/live").then((r) => r.results);
}
