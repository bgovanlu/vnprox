// SPDX-License-Identifier: Apache-2.0

// Pure filter-form helpers for the Audit page, in their own module (not
// AuditPage.tsx) so the page file only exports components (react-refresh)
// and the logic is directly unit-testable.
import type { AuditFilter } from "../api/audit";

/** Parses a datetime-local input value into unix seconds, or undefined for
 * an empty/invalid value. */
export function parseDateInput(value: string): number | undefined {
  if (!value) {
    return undefined;
  }
  const ms = new Date(value).getTime();
  return Number.isNaN(ms) ? undefined : Math.floor(ms / 1000);
}

/** The filter form's raw state (strings straight from inputs). */
export interface FilterForm {
  user: string;
  result: string;
  target: string;
  from: string;
  to: string;
}

export const emptyForm: FilterForm = { user: "", result: "", target: "", from: "", to: "" };

/** Converts the raw form into the API filter shape. */
export function toAuditFilter(form: FilterForm): AuditFilter {
  return {
    user: form.user.trim() || undefined,
    result: form.result.trim() || undefined,
    target: form.target.trim() || undefined,
    from: parseDateInput(form.from),
    to: parseDateInput(form.to),
  };
}
