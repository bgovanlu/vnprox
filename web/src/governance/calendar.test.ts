// SPDX-License-Identifier: Apache-2.0

// T-4006: pure rendering-helper coverage for the change-calendar view.
import { describe, expect, it } from "vitest";
import { freezeWindowSummary, scheduleInOneOffFreeze, sortSchedulesByWindowStart } from "./calendar";
import type { FreezeWindowView, Schedule } from "../api/policies";

function freezeWindow(overrides: Partial<FreezeWindowView> = {}): FreezeWindowView {
  return { ruleId: "r", description: "d", severity: "deny", recognized: true, ...overrides };
}

function schedule(overrides: Partial<Schedule> = {}): Schedule {
  return {
    changesetId: "cs1",
    windowStart: 1_700_000_000,
    windowEnd: 1_700_000_100,
    confirmTimeoutSec: 30,
    missedWindowPolicy: "skip",
    status: "pending",
    createdBy: "alice",
    createdAt: 1_700_000_000,
    ...overrides,
  };
}

describe("freezeWindowSummary", () => {
  it("returns undefined for an unrecognized window rather than guessing", () => {
    expect(freezeWindowSummary(freezeWindow({ recognized: false, weekdays: ["fri"] }))).toBeUndefined();
  });

  it("summarizes a recurring weekly window with its zone", () => {
    const summary = freezeWindowSummary(
      freezeWindow({ weekdays: ["fri"], minuteOfDayStart: 840, minuteOfDayEnd: 1080, zone: "America/New_York" }),
    );
    expect(summary).toBe("every Friday, 14:00-18:00 (America/New_York)");
  });

  it("summarizes a one-off epoch range with no zone needed", () => {
    const summary = freezeWindowSummary(freezeWindow({ epochStart: 1_765_756_800, epochEnd: 1_767_312_000 }));
    expect(summary).toContain(" - ");
    expect(summary).not.toContain("(");
  });

  it("returns undefined when recognized but no field actually resolved", () => {
    expect(freezeWindowSummary(freezeWindow({ recognized: true }))).toBeUndefined();
  });
});

describe("sortSchedulesByWindowStart", () => {
  it("orders soonest first without mutating the input", () => {
    const input = [schedule({ changesetId: "later", windowStart: 200 }), schedule({ changesetId: "sooner", windowStart: 100 })];
    const sorted = sortSchedulesByWindowStart(input);
    expect(sorted.map((s) => s.changesetId)).toEqual(["sooner", "later"]);
    expect(input[0]?.changesetId).toBe("later"); // unmutated
  });
});

describe("scheduleInOneOffFreeze", () => {
  const windows = [freezeWindow({ epochStart: 100, epochEnd: 200 })];

  it("flags a schedule whose fire instant falls inside the range (start inclusive)", () => {
    expect(scheduleInOneOffFreeze(schedule({ windowStart: 100 }), windows)).toBe(true);
    expect(scheduleInOneOffFreeze(schedule({ windowStart: 150 }), windows)).toBe(true);
  });

  it("does not flag the range's exclusive end or outside it", () => {
    expect(scheduleInOneOffFreeze(schedule({ windowStart: 200 }), windows)).toBe(false);
    expect(scheduleInOneOffFreeze(schedule({ windowStart: 99 }), windows)).toBe(false);
  });

  it("ignores a recurring (non-epoch) window entirely", () => {
    const recurring = [freezeWindow({ weekdays: ["fri"] })];
    expect(scheduleInOneOffFreeze(schedule({ windowStart: 150 }), recurring)).toBe(false);
  });
});
