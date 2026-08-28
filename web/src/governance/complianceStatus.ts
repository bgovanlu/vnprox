// SPDX-License-Identifier: Apache-2.0

// T-2706's control-status vocabulary, classified in one place.
//
// The contract's own words (docs/api.md): "Four values, and exactly one of
// them is a pass." The Go side already enforces that through
// `compliance.Status.IsPassing`, deliberately a function rather than a `==
// StatusPass` at each call site, so that "an unmapped control can never be
// rendered as passing" is enforceable. This module is the browser's half of
// the same arrangement.
//
// `unknown` is a FIFTH member on this side and is not a mistake: the wire
// value is a string the daemon versions independently of this client, and a
// status this build has never heard of must render as unrecognised rather
// than fall through to whichever branch happens to be last. It is not
// passing.
import type { ComplianceControl, ComplianceReport } from "../api/compliance";

export type ControlStatus = "pass" | "fail" | "not_evaluated" | "unmapped" | "unknown";

/** Every status this build models, in report order. `unknown` is absent: it
 * is a classification outcome, never something to enumerate as a column. */
export const KNOWN_CONTROL_STATUSES: readonly ControlStatus[] = ["pass", "fail", "not_evaluated", "unmapped"];

export function classifyControlStatus(raw: string): ControlStatus {
  switch (raw) {
    case "pass":
    case "fail":
    case "not_evaluated":
    case "unmapped":
      return raw;
    default:
      return "unknown";
  }
}

/** The single predicate anything in this module uses to decide whether a
 * control passed. Mirrors `compliance.Status.IsPassing` — one status is a
 * pass, and `unknown` is emphatically not it. */
export function isPassing(status: ControlStatus): boolean {
  return status === "pass";
}

export const CONTROL_STATUS_LABEL: Record<ControlStatus, string> = {
  pass: "pass",
  fail: "fail",
  not_evaluated: "not evaluated",
  unmapped: "unmapped",
  unknown: "unrecognised",
};

export const CONTROL_STATUS_MEANING: Record<ControlStatus, string> = {
  pass: "Every mapped evidence item was evaluated and satisfied.",
  fail: "At least one mapped evidence item was evaluated and is not satisfied. The failing item is named below it.",
  not_evaluated:
    "The control has a mapping, but at least one item could not be evaluated. Absence of evidence is not evidence of compliance.",
  unmapped:
    "The control has no mapped evidence at all — vnprox observes nothing that speaks to it, and says so rather than passing it.",
  unknown:
    "This build does not recognise the status the daemon reported, so it cannot say what the control's state is. It is not being counted as a pass.",
};

/** T-4204 semantic status scale classes per status. `unmapped` and
 * `not_evaluated` deliberately do not share `pass`'s colour family — the
 * whole point of the four-value vocabulary is that "we have nothing to say"
 * must not look like "fine": `pass`/`fail`/`not_evaluated`/`unmapped` map
 * onto the scale's ok/critical/degraded/unknown respectively (`unmapped`
 * fits `unknown` well — "no mapped evidence" IS "nothing to say").
 *
 * The fifth member, this module's own `unknown` (a wire status this build
 * does not recognise at all), deliberately stays OFF the six-state scale
 * rather than doubling up on `status-unknown`: it needs to visually pop out
 * from `unmapped` specifically, not blend with it, because "vnprox has
 * nothing to say about this control" (`unmapped`) and "vnprox cannot even
 * parse what the daemon said" (this `unknown`) are different failure modes
 * an operator must not conflate. Left as its own fuchsia, same as it was. */
export const CONTROL_STATUS_CLASS: Record<ControlStatus, string> = {
  pass: "border-status-ok",
  fail: "border-status-critical",
  not_evaluated: "border-status-degraded",
  unmapped: "border-status-unknown",
  unknown: "border-fuchsia-400 dark:border-fuchsia-600",
};

export interface ClassifiedControl extends ComplianceControl {
  classified: ControlStatus;
}

export function classifyControls(report: ComplianceReport | undefined): ClassifiedControl[] {
  return (report?.controls ?? []).map((c) => ({ ...c, classified: classifyControlStatus(c.status) }));
}

/** Recomputed per-status counts, from the controls themselves rather than
 * from `report.summary`.
 *
 * The daemon's `summary` has four buckets and no room for a status this build
 * cannot classify, so a report containing one would have a summary whose
 * numbers do not add up against what is on screen. Counting locally keeps the
 * table and the tally describing the same thing, and surfaces the discrepancy
 * instead of hiding it. */
export function countByStatus(controls: readonly ClassifiedControl[]): Record<ControlStatus, number> {
  const counts: Record<ControlStatus, number> = {
    pass: 0,
    fail: 0,
    not_evaluated: 0,
    unmapped: 0,
    unknown: 0,
  };
  for (const c of controls) {
    counts[c.classified] += 1;
  }
  return counts;
}

/** True when the daemon's own summary disagrees with what this build counted
 * — which happens exactly when a status arrived that this build cannot
 * classify. Reported to the operator rather than resolved silently. */
export function summaryDisagrees(report: ComplianceReport | undefined, counts: Record<ControlStatus, number>): boolean {
  if (report === undefined) return false;
  const s = report.summary;
  const classified = counts.pass + counts.fail + counts.not_evaluated + counts.unmapped + counts.unknown;
  return (
    s.pass !== counts.pass ||
    s.fail !== counts.fail ||
    s.notEvaluated !== counts.not_evaluated ||
    s.unmapped !== counts.unmapped ||
    // `total` is the bucket an unclassifiable status shows up in: the four
    // named counts can agree exactly while the totals do not, which is
    // precisely what happens when the daemon reports a fifth status.
    s.total !== classified
  );
}
