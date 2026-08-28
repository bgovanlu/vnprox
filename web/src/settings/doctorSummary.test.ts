// SPDX-License-Identifier: Apache-2.0

// T-3003's single most important assertion, at the counting layer: a skipped
// check is never a passing one.
//
// `internal/verify`'s own rule is the precedent — a run in which everything
// skipped prints "0 passed" and says so outright, because "we could not look"
// read as "we looked and it was fine" is how a validation figure becomes
// fiction (cmd/vnproxctl/verifycmd.go, behaviour #2).
import { describe, expect, it } from "vitest";
import type { DoctorResult } from "../api/types";
import { doctorCountsLine, doctorVerdict, summarizeDoctorResults, HEALTHY_STATUSES } from "./doctorSummary";

function result(check: string, status: string, detail = "detail"): DoctorResult {
  return { check, status, detail };
}

/** The four checks GET /doctor/live actually returns (internal/doctor.LiveChecks). */
const LIVE_CHECKS = ["pve_reachable", "pve_privileges", "peer_secret", "clock_skew"];

describe("summarizeDoctorResults", () => {
  it("counts each of the four statuses in its own bucket", () => {
    const summary = summarizeDoctorResults([
      result("pve_reachable", "pass"),
      result("pve_privileges", "warn"),
      result("peer_secret", "skip"),
      result("clock_skew", "fail"),
    ]);
    expect(summary).toEqual({ pass: 1, warn: 1, fail: 1, skip: 1, unknown: 0, total: 4 });
  });

  it("NEVER folds a skip into the pass count", () => {
    const summary = summarizeDoctorResults(LIVE_CHECKS.map((c) => result(c, "skip")));
    expect(summary.skip).toBe(4);
    expect(summary.pass).toBe(0);
  });

  it("counts an unrecognised status as unknown rather than as a pass", () => {
    // Report.Validate rejects an unknown status server-side, but /doctor/live
    // returns RunLive's slice directly without going through it — so the
    // client must not depend on that.
    const summary = summarizeDoctorResults([result("pve_reachable", "probably-fine")]);
    expect(summary.unknown).toBe(1);
    expect(summary.pass).toBe(0);
  });

  it("counts nothing for an empty result set", () => {
    expect(summarizeDoctorResults([])).toEqual({ pass: 0, warn: 0, fail: 0, skip: 0, unknown: 0, total: 0 });
  });
});

describe("HEALTHY_STATUSES", () => {
  it("contains pass and nothing else", () => {
    // The gate on the gate: if `skip` or `warn` were ever added here, every
    // "skip is not a pass" assertion above would still hold while the UI
    // quietly started treating one as the other.
    expect([...HEALTHY_STATUSES]).toEqual(["pass"]);
  });
});

describe("doctorVerdict", () => {
  it("refuses to call an all-skipped run clean", () => {
    const verdict = doctorVerdict(summarizeDoctorResults(LIVE_CHECKS.map((c) => result(c, "skip"))));
    expect(verdict).toContain("A skipped check is not a passing one");
    expect(verdict).not.toMatch(/passed\.$/);
  });

  it("reports a failure first, even when skips are also present", () => {
    const verdict = doctorVerdict(
      summarizeDoctorResults([result("a", "fail"), result("b", "skip"), result("c", "pass")]),
    );
    expect(verdict).toContain("failed");
  });

  it("qualifies a passing run that still has skips", () => {
    const verdict = doctorVerdict(summarizeDoctorResults([result("a", "pass"), result("b", "skip")]));
    expect(verdict).toContain("some could not run");
    expect(verdict).toContain("A skipped check is not a passing one");
  });

  it("does not call an empty run healthy", () => {
    expect(doctorVerdict(summarizeDoctorResults([]))).toContain("nothing was checked");
  });

  it("says a warning is not a failure", () => {
    const verdict = doctorVerdict(summarizeDoctorResults([result("a", "pass"), result("b", "warn")]));
    expect(verdict).toContain("not a failure");
  });

  it("is unqualified only when every check ran and passed", () => {
    expect(doctorVerdict(summarizeDoctorResults(LIVE_CHECKS.map((c) => result(c, "pass"))))).toBe(
      "Every check that could run, passed.",
    );
  });
});

describe("doctorCountsLine", () => {
  it("always states all four counts, including the zeroes", () => {
    const line = doctorCountsLine(summarizeDoctorResults([result("a", "skip")]));
    expect(line).toBe("0 passed, 0 warned, 0 failed, 1 skipped");
  });

  it("appends the unrecognised count only when there is one", () => {
    expect(doctorCountsLine(summarizeDoctorResults([result("a", "pass")]))).not.toContain("unrecognised");
    expect(doctorCountsLine(summarizeDoctorResults([result("a", "???")]))).toContain("1 unrecognised");
  });
});
