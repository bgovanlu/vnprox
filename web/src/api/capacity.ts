// SPDX-License-Identifier: Apache-2.0

// Capacity history export (docs/api.md's `GET /capacity/export?ref=&kind=
// link|ipam_pool[&format=csv|json]`, T-1606; internal/api/capacity.go).
//
// This is the *export*, not a forecast. Capacity forecasts are not headless:
// internal/capacity's Analyze feeds `SourceCapacity` findings into GET
// /findings, which the findings stream already renders — so nothing here
// draws a second forecast surface.
//
// The export is bounded server-side to `[capacity] aggregate_retention_days`
// (store.DefaultCapacityRetentionDays = 400 when unset), computed from the
// daemon's own clock, so rows older than that are absent even between prune
// ticks. That bound is NOT exposed on GET /config, so the UI can name the
// setting but must not claim a number — see RETENTION_BOUND_NOTE.
import { apiFetch } from "./client";
import type { CapacityExport, CapacityKind } from "./types";

/** The daemon's own default when `[capacity] aggregate_retention_days` is
 * unset (store.DefaultCapacityRetentionDays). Only ever rendered as "the
 * default", never as "your configured value" — GET /config does not expose
 * the configured one, so the UI genuinely does not know it. */
export const DEFAULT_CAPACITY_RETENTION_DAYS = 400;

/** The sentence every export affordance shows next to itself. Phrased so a
 * daemon with a non-default retention is not misdescribed. */
export const RETENTION_BOUND_NOTE =
  `Bounded server-side to this daemon's [capacity] aggregate_retention_days ` +
  `(default ${String(DEFAULT_CAPACITY_RETENTION_DAYS)} days). Older buckets are not exported.`;

/** Both required query parameters, in the one place they are built.
 * `format` defaults to `json` server-side; it is always sent explicitly so
 * the URL a download link carries is the URL that was tested. */
export function capacityExportQuery(ref: string, kind: CapacityKind, format: "csv" | "json"): string {
  return new URLSearchParams({ ref, kind, format }).toString();
}

/** The href for the CSV download. A plain `<a download>` rather than an
 * apiFetch round trip: the response is `text/csv` with a
 * `Content-Disposition: attachment`, and a same-origin navigation already
 * carries the session cookie — the same shape ToolsPage's documentation
 * export uses. */
export function capacityExportCsvHref(ref: string, kind: CapacityKind): string {
  return `/api/v1/capacity/export?${capacityExportQuery(ref, kind, "csv")}`;
}

/** GET /capacity/export&format=json — the same bounded history the CSV
 * download carries, for previewing what an export would contain. */
export function fetchCapacityExport(ref: string, kind: CapacityKind): Promise<CapacityExport> {
  return apiFetch<CapacityExport>(`/capacity/export?${capacityExportQuery(ref, kind, "json")}`);
}
