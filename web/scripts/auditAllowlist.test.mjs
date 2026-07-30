// T-1806 AC3: "the npm audit allowlist has a rationale and an expiry per
// entry, and an expired entry fails the build (tested by setting one to a
// past date)." These tests exercise the pure logic (auditAllowlist.mjs)
// directly against fixture JSON — no real `npm audit` invocation — so they
// run fast and deterministically as part of `npm run test`.
import { describe, expect, it } from "vitest";
import { advisoriesFromReport, evaluate, parseAllowlist } from "./auditAllowlist.mjs";

const VALID_ENTRY = {
  id: "GHSA-aaaa-bbbb-cccc",
  package: "some-pkg",
  rationale: "test rationale",
  expires: "2099-01-01",
};

function reportWith(advisories) {
  const vulnerabilities = {};
  for (const adv of advisories) {
    vulnerabilities[adv.package] = {
      name: adv.package,
      severity: adv.severity ?? "high",
      via: [
        {
          source: 1,
          name: adv.package,
          dependency: adv.package,
          title: adv.title ?? "some vulnerability",
          url: `https://github.com/advisories/${adv.id}`,
          severity: adv.severity ?? "high",
        },
      ],
    };
  }
  return { vulnerabilities };
}

describe("parseAllowlist", () => {
  it("parses a well-formed allowlist", () => {
    const map = parseAllowlist(JSON.stringify([VALID_ENTRY]));
    expect(map.get("GHSA-aaaa-bbbb-cccc")).toEqual(VALID_ENTRY);
  });

  it("rejects non-array JSON", () => {
    expect(() => parseAllowlist(JSON.stringify({ not: "an array" }))).toThrow(/must be a JSON array/);
  });

  it("rejects invalid JSON", () => {
    expect(() => parseAllowlist("{not json")).toThrow(/not valid JSON/);
  });

  it("rejects an entry missing a rationale", () => {
    const bad = { ...VALID_ENTRY, rationale: "" };
    expect(() => parseAllowlist(JSON.stringify([bad]))).toThrow(/rationale/);
  });

  it("rejects an entry missing an expires date", () => {
    const { expires, ...rest } = VALID_ENTRY;
    expect(() => parseAllowlist(JSON.stringify([rest]))).toThrow(/expires/);
  });

  it("rejects a malformed expires date", () => {
    const bad = { ...VALID_ENTRY, expires: "not-a-date" };
    expect(() => parseAllowlist(JSON.stringify([bad]))).toThrow(/malformed "expires"/);
  });

  it("rejects a non-GHSA id", () => {
    const bad = { ...VALID_ENTRY, id: "CVE-2024-0001" };
    expect(() => parseAllowlist(JSON.stringify([bad]))).toThrow(/non-GHSA id/);
  });

  it("rejects duplicate ids", () => {
    expect(() => parseAllowlist(JSON.stringify([VALID_ENTRY, VALID_ENTRY]))).toThrow(/duplicate entry/);
  });
});

describe("advisoriesFromReport", () => {
  it("extracts high/critical root advisories only", () => {
    const report = {
      vulnerabilities: {
        highPkg: {
          severity: "high",
          via: [{ url: "https://github.com/advisories/GHSA-high1-high1-high1", title: "high one" }],
        },
        lowPkg: {
          severity: "low",
          via: [{ url: "https://github.com/advisories/GHSA-low1-low1-low1x", title: "low one" }],
        },
        dependentPkg: {
          // npm audit represents "depends on a vulnerable package" via a bare
          // string reference to the dependency name, not a GHSA record.
          severity: "high",
          via: ["highPkg"],
        },
      },
    };
    const advisories = advisoriesFromReport(report);
    expect(advisories).toHaveLength(1);
    expect(advisories[0]).toMatchObject({ id: "GHSA-high1-high1-high1", package: "highPkg" });
  });

  it("returns an empty list for a clean report", () => {
    expect(advisoriesFromReport({ vulnerabilities: {} })).toEqual([]);
  });

  it("tolerates a missing vulnerabilities key", () => {
    expect(advisoriesFromReport({})).toEqual([]);
  });
});

describe("evaluate", () => {
  it("passes when every found advisory is allowlisted and unexpired", () => {
    const allowlist = parseAllowlist(JSON.stringify([{ ...VALID_ENTRY, expires: "2099-01-01" }]));
    const report = reportWith([{ id: "GHSA-aaaa-bbbb-cccc", package: "some-pkg" }]);
    const { failures } = evaluate(advisoriesFromReport(report), allowlist, "2026-07-30");
    expect(failures).toEqual([]);
  });

  it("fails on an advisory that isn't in the allowlist at all", () => {
    const allowlist = parseAllowlist(JSON.stringify([]));
    const report = reportWith([{ id: "GHSA-new1-new1-new1x", package: "surprise-pkg" }]);
    const { failures } = evaluate(advisoriesFromReport(report), allowlist, "2026-07-30");
    expect(failures).toHaveLength(1);
    expect(failures[0]).toMatch(/GHSA-new1-new1-new1x/);
    expect(failures[0]).toMatch(/not in audit-allowlist\.json/);
  });

  // The AC3 case: "tested by setting one to a past date."
  it("fails on an allowlisted advisory whose entry has expired", () => {
    const expired = { ...VALID_ENTRY, expires: "2020-01-01" };
    const allowlist = parseAllowlist(JSON.stringify([expired]));
    const report = reportWith([{ id: expired.id, package: expired.package }]);
    const { failures } = evaluate(advisoriesFromReport(report), allowlist, "2026-07-30");
    expect(failures).toHaveLength(1);
    expect(failures[0]).toMatch(/expired 2020-01-01/);
  });

  it("does not fail on an entry expiring exactly today", () => {
    const allowlist = parseAllowlist(JSON.stringify([{ ...VALID_ENTRY, expires: "2026-07-30" }]));
    const report = reportWith([{ id: VALID_ENTRY.id, package: VALID_ENTRY.package }]);
    const { failures } = evaluate(advisoriesFromReport(report), allowlist, "2026-07-30");
    expect(failures).toEqual([]);
  });

  it("reports allowlist entries no longer found by npm audit as stale, not as failures", () => {
    const allowlist = parseAllowlist(JSON.stringify([{ ...VALID_ENTRY, expires: "2099-01-01" }]));
    const { failures, staleEntryIds } = evaluate([], allowlist, "2026-07-30");
    expect(failures).toEqual([]);
    expect(staleEntryIds).toEqual([VALID_ENTRY.id]);
  });
});
