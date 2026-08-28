// SPDX-License-Identifier: Apache-2.0

// Counting and verdict rules for `GET /doctor/live`'s results.
//
// This module exists so that the single most important assertion on the
// T-3003 card — a skipped check is never counted or styled as a passing one —
// is testable without rendering anything, and so the counting rule lives in
// exactly one place rather than being re-derived by each renderer.
//
// The precedent being copied is `vnproxctl verify`'s own, verbatim:
// `internal/verify/verify.go` prints "Nothing was validated: every check
// skipped. A skipped check is not a passing one." whenever `Passed == 0`,
// *including* when `Failed == 0`, because "a wall of skips with a '0 failed'
// footer reads as success to a tired operator". `cmd/vnproxctl/verifycmd.go`
// states the same rule as behaviour #2 of the command.
import { asDoctorStatus } from "../api/doctor";
import type { DoctorResult, DoctorStatus } from "../api/types";

/** Per-status counts. `unknown` counts results whose wire status this build
 * does not recognise; it is its own bucket rather than being folded into any
 * of the four, for the same reason `skip` is. */
export interface DoctorSummary {
  pass: number;
  warn: number;
  fail: number;
  skip: number;
  unknown: number;
  total: number;
}

export function summarizeDoctorResults(results: readonly DoctorResult[]): DoctorSummary {
  const summary: DoctorSummary = { pass: 0, warn: 0, fail: 0, skip: 0, unknown: 0, total: results.length };
  for (const result of results) {
    const status = asDoctorStatus(result.status);
    switch (status) {
      case "pass":
        summary.pass += 1;
        break;
      case "warn":
        summary.warn += 1;
        break;
      case "fail":
        summary.fail += 1;
        break;
      case "skip":
        summary.skip += 1;
        break;
      case undefined:
        summary.unknown += 1;
        break;
    }
  }
  return summary;
}

/** Which statuses this UI treats as "checked and healthy". Exported so a test
 * can assert its membership directly: `skip` and `unknown` must never appear
 * in it, whatever the styling happens to look like. */
export const HEALTHY_STATUSES: readonly DoctorStatus[] = ["pass"];

/** The one-line verdict under the results table.
 *
 * Ordering matters and mirrors `verify.go`'s switch: a failure is reported
 * first, then the everything-skipped case, then the ordinary success. The
 * middle branch is the one that exists so that `0 failed` can never be read
 * as `everything is fine`. */
export function doctorVerdict(summary: DoctorSummary): string {
  if (summary.total === 0) {
    return "The daemon ran no live checks. That is not a clean bill of health — it means nothing was checked.";
  }
  if (summary.fail > 0) {
    return "At least one check failed. See the failing checks above and their remediation.";
  }
  if (summary.pass === 0) {
    return "Nothing was checked: every check skipped or could not be classified. A skipped check is not a passing one.";
  }
  if (summary.skip > 0 || summary.unknown > 0) {
    return "Every check that could run, passed — but some could not run, and say why. A skipped check is not a passing one.";
  }
  if (summary.warn > 0) {
    return "Every check passed or warned. A warning is degraded-but-working, not a failure.";
  }
  return "Every check that could run, passed.";
}

/** The counts line, always naming all four statuses (plus unknown when any
 * appears) so a zero is stated rather than inferred from an absence. */
export function doctorCountsLine(summary: DoctorSummary): string {
  const parts = [
    `${String(summary.pass)} passed`,
    `${String(summary.warn)} warned`,
    `${String(summary.fail)} failed`,
    `${String(summary.skip)} skipped`,
  ];
  if (summary.unknown > 0) {
    parts.push(`${String(summary.unknown)} unrecognised`);
  }
  return parts.join(", ");
}
