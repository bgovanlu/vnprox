// SPDX-License-Identifier: Apache-2.0

// T-4006: pure rendering helpers for the change-calendar view (`GET
// /calendar`) — turning a FreezeWindowView's recognized shape into readable
// text, and sorting pending schedules chronologically. No network, no
// enforcement decision: the daemon's own validate stage is the sole
// authority on whether a freeze actually blocks anything (see
// freeze_calendar.go's own "best-effort READER" doc comment).
import type { FreezeWindowView, Schedule } from "../api/policies";

const WEEKDAY_LABEL: Record<string, string> = {
  sun: "Sunday",
  mon: "Monday",
  tue: "Tuesday",
  wed: "Wednesday",
  thu: "Thursday",
  fri: "Friday",
  sat: "Saturday",
};

function minuteOfDayLabel(m: number): string {
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return `${String(h).padStart(2, "0")}:${String(mm).padStart(2, "0")}`;
}

/** A one-line, human-readable rendering of what a recognized freeze window
 * covers, e.g. "every Friday, 14:00-18:00 (America/New_York)" or "Dec 15
 * 00:00:00 UTC - Jan 2 00:00:00 UTC". Returns undefined for a window this
 * renderer did not recognize (`recognized: false`) — the caller must fall
 * back to the rule's own description rather than inventing a box for it. */
export function freezeWindowSummary(w: FreezeWindowView): string | undefined {
  if (!w.recognized) return undefined;

  const parts: string[] = [];
  if (w.weekdays !== undefined && w.weekdays.length > 0) {
    parts.push(`every ${w.weekdays.map((d) => WEEKDAY_LABEL[d] ?? d).join(", ")}`);
  }
  if (w.daysOfMonth !== undefined && w.daysOfMonth.length > 0) {
    parts.push(`day${w.daysOfMonth.length === 1 ? "" : "s"} ${w.daysOfMonth.join(", ")} of the month`);
  }
  if (w.months !== undefined && w.months.length > 0) {
    parts.push(`month${w.months.length === 1 ? "" : "s"} ${w.months.join(", ")}`);
  }
  if (w.minuteOfDayStart !== undefined && w.minuteOfDayEnd !== undefined) {
    parts.push(`${minuteOfDayLabel(w.minuteOfDayStart)}-${minuteOfDayLabel(w.minuteOfDayEnd)}`);
  }
  if (w.epochStart !== undefined && w.epochEnd !== undefined) {
    parts.push(`${new Date(w.epochStart * 1000).toLocaleString()} - ${new Date(w.epochEnd * 1000).toLocaleString()}`);
  }
  if (parts.length === 0) return undefined;

  const zoneSuffix = w.zone !== undefined && w.zone !== "" ? ` (${w.zone})` : "";
  return parts.join(", ") + zoneSuffix;
}

/** Pending schedules, soonest windowStart first — the order an operator
 * scanning "what's coming up" wants. */
export function sortSchedulesByWindowStart(schedules: readonly Schedule[]): Schedule[] {
  return [...schedules].sort((a, b) => a.windowStart - b.windowStart);
}

/** Whether a given pending schedule's fire instant (windowStart) falls
 * inside a recognized one-off freeze window (epochStart/epochEnd) — the UI
 * hint behind "why this apply will be refused before you stage it". Weekly/
 * monthly (local-wall-clock) windows are deliberately NOT evaluated here:
 * doing that correctly needs the same timezone-aware machinery
 * policy_eval.go owns, and re-implementing it client-side would be a second
 * copy of the one thing this whole card insists on having only once. A
 * schedule inside a recurring freeze is still caught, authoritatively, at
 * fire time (AC2) — this helper only flags what it can prove from the
 * numbers already on screen. */
export function scheduleInOneOffFreeze(schedule: Schedule, windows: readonly FreezeWindowView[]): boolean {
  return windows.some(
    (w) =>
      w.epochStart !== undefined &&
      w.epochEnd !== undefined &&
      schedule.windowStart >= w.epochStart &&
      schedule.windowStart < w.epochEnd,
  );
}
