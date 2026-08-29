// SPDX-License-Identifier: Apache-2.0

// T-3908's "what changed" heat layer: resolves a point-in-time topology diff
// (the same T-2704 route diffOverlay.ts already reads — no new backend
// route) to per-map-node recency marks, colored by how long ago the entity
// last changed.
//
// A pure mapping function, kept out of canvasDraw.ts so it is unit-testable
// without a real CanvasRenderingContext2D — the same split diffOverlay.ts/
// mtuOverlay.ts already establish for every other overlay.
//
// ATTRIBUTION IS REUSED, NOT REINVENTED. `TopologyDiffAttribution` already
// distinguishes "a changeset explains this" from "nothing does" (an
// out-of-band, drift change) — internal/drift and the reconcile machinery's
// own attributed/unattributed vocabulary, surfaced here exactly as T-2704
// already surfaces it for diffOverlay.ts. This module does not invent a
// second notion of attribution.
//
// WHY DRIFT ISN'T ON THE TIME SCALE. An attributed change carries the exact
// instant its changeset's apply-lifecycle audit row fired
// (`TopologyDiffAttribution.at`), so it buckets precisely. An unattributed
// (drift) change carries no such instant — the diff route only knows the
// entity differs somewhere between its `from` and `to` points, never when.
// Rather than fabricate a bucket by guessing a point in that range (the
// exact kind of invented precision this codebase's diff/snapshot code
// consistently refuses to do — see topodiff.go's ErrNoSnapshotForPoint doc
// comment), drift gets its own bucket, orthogonal to elapsed time. Per this
// task's card, that is also the MORE interesting signal during triage, not
// a lesser one, so it is never demoted into "older" just because its age is
// unknown.
//
// WHY "NEVER CHANGED" HAS NO MARK AT ALL. An entity with no row in the diff
// response didn't differ between `from` and `to` — this mirrors
// diffOverlay's own convention exactly (no ring = no difference). The
// caller (TopologyPage.tsx) paints every *rendered* entity that has no
// recency mark as the visually distinct "no data" state — plain, unringed —
// so "changed long ago" (a muted but present ring) and "never changed in
// the recorded window" (no ring) cannot be confused for one another, per
// this task's card.

import type { TopologyDiffResponse, TopologyEntityDiff } from "../api/topologyDiff";
import { allTopologyDiffRows } from "../api/topologyDiff";

/** Five states a changed entity can be painted in. `drift` is not a time
 * bucket — see this file's doc comment. The remaining four are ordered
 * hottest first, purely for readability; nothing iterates this order. */
export type RecencyBucket = "drift" | "justNow" | "today" | "thisWeek" | "older";

/** Bucket boundaries, in seconds of elapsed time since the change. Named
 * constants (not inlined) so recencyOverlay.test.ts can assert on the exact
 * edges rather than guessing them from behavior. */
export const JUST_NOW_SECONDS = 15 * 60;
export const TODAY_SECONDS = 24 * 3600;
export const WEEK_SECONDS = 7 * 24 * 3600;

/** Buckets an exact elapsed age (seconds, already `nowAt - at`) into one of
 * the four time-based buckets. A negative age (clock skew, or `at` landing
 * after `nowAt`) is clamped to zero rather than producing a nonsensical
 * bucket — "just now" is the honest answer to "in the future, as far as we
 * can tell". */
export function recencyBucketForAge(ageSeconds: number): "justNow" | "today" | "thisWeek" | "older" {
  const age = Math.max(0, ageSeconds);
  if (age < JUST_NOW_SECONDS) return "justNow";
  if (age < TODAY_SECONDS) return "today";
  if (age < WEEK_SECONDS) return "thisWeek";
  return "older";
}

/** Buckets an absolute change instant against the current time. Thin
 * wrapper over recencyBucketForAge — kept separate because callers
 * naturally have two absolute instants (the change's `at`, and "now"), not
 * a pre-subtracted age. */
export function recencyBucketForInstant(nowAt: number, at: number): "justNow" | "today" | "thisWeek" | "older" {
  return recencyBucketForAge(nowAt - at);
}

/** One map node's recency mark. `nodeId` is the entity's own `inventory.Ref`
 * string, already the map's node-id convention (docs/features/topology.md
 * §3). `at`/`changesetId`/`changesetTitle`/`actor` are present only for a
 * timed (non-`drift`) bucket — the exact instant and, where a vnprox
 * changeset explains the change, which one. */
export interface RecencyMark {
  nodeId: string;
  bucket: RecencyBucket;
  attributed: boolean;
  at?: number;
  changesetId?: string;
  changesetTitle?: string;
  actor?: string;
  /** Short human label for the legend/tooltip/aria text. */
  label: string;
}

/** A changed entity not currently on the map (removed, or hidden by the
 * active layer/VLAN filters) — counted, per diffOverlay.ts's own
 * `offMap`/`isOnMap` convention, rather than silently dropped. */
export interface RecencyOverlay {
  marks: RecencyMark[];
  offMap: RecencyMark[];
  driftCount: number;
  changedCount: number;
}

const BUCKET_PHRASE: Record<RecencyBucket, string> = {
  drift: "outside vnprox — exact time unknown",
  justNow: "in the last 15 minutes",
  today: "in the last 24 hours",
  thisWeek: "in the last 7 days",
  older: "more than 7 days ago",
};

/** Full-sentence phrase for a bucket, reused by the legend, the map
 * status line, and each mark's own label. */
export function recencyBucketPhrase(bucket: RecencyBucket): string {
  return BUCKET_PHRASE[bucket];
}

function labelFor(row: TopologyEntityDiff, bucket: RecencyBucket): string {
  const what = row.name ?? row.ref;
  if (bucket === "drift") {
    return `${what} changed ${BUCKET_PHRASE.drift}`;
  }
  const who = row.attribution.actor ?? "vnprox";
  return `${what} changed by ${who}, ${BUCKET_PHRASE[bucket]}`;
}

/** Resolves a topology diff to recency marks.
 *
 * `nowAt` defaults to the real wall clock (unix seconds) — overridable so
 * tests never depend on `Date.now()`. `isOnMap` reports whether an entity
 * ref is currently rendered, exactly mirroring diffOverlay.ts's
 * `computeDiffOverlay`.
 *
 * Deterministic: rows arrive already ref-ordered from the server and this
 * function never iterates a map into its output. */
export function computeRecencyOverlay(
  diff: TopologyDiffResponse | undefined,
  isOnMap: (ref: string) => boolean,
  nowAt: number = Math.floor(Date.now() / 1000),
): RecencyOverlay {
  const out: RecencyOverlay = { marks: [], offMap: [], driftCount: 0, changedCount: 0 };
  if (!diff) return out;

  for (const row of allTopologyDiffRows(diff)) {
    const timed = row.attribution.attributed && row.attribution.at !== undefined;
    const bucket: RecencyBucket = timed ? recencyBucketForInstant(nowAt, row.attribution.at ?? 0) : "drift";
    const mark: RecencyMark = {
      nodeId: row.ref,
      bucket,
      attributed: row.attribution.attributed,
      at: timed ? row.attribution.at : undefined,
      changesetId: row.attribution.changesetId,
      changesetTitle: row.attribution.changesetTitle,
      actor: row.attribution.actor,
      label: labelFor(row, bucket),
    };
    out.changedCount += 1;
    if (bucket === "drift") out.driftCount += 1;
    if (isOnMap(row.ref)) {
      out.marks.push(mark);
    } else {
      out.offMap.push(mark);
    }
  }
  return out;
}

/** The corner-badge color for a mark — a heat scale (hot red -> cool slate)
 * for the four timed buckets, plus a fifth, deliberately off-scale color for
 * drift so it never reads as merely "a bit stale". Never the only signal:
 * every mark also carries a distinct glyph (recencyMarkGlyph) and a full
 * text label, per this task's WCAG requirement. */
export function recencyMarkColor(bucket: RecencyBucket): string {
  switch (bucket) {
    case "drift":
      return "#4f46e5"; // indigo-600 — off the heat gradient entirely
    case "justNow":
      return "#dc2626"; // red-600 — hottest
    case "today":
      return "#f97316"; // orange-500
    case "thisWeek":
      return "#d97706"; // amber-600
    case "older":
      return "#64748b"; // slate-500 — coolest, but still a visible ring
  }
}

/** The colour the badge's own glyph is drawn in.
 *
 * T-4303 (while measuring this overlay): the badge is a filled disc with a
 * glyph on it, and `canvasDraw.ts` drew that glyph in flat white for every
 * bucket. Against the two warm fills white does not clear AA:
 *
 *     bucket     fill      white glyph   dark glyph
 *     drift      #4f46e5      6.29          2.84
 *     justNow    #dc2626      4.83          3.69
 *     today      #f97316      2.80  FAIL    6.36
 *     thisWeek   #d97706      3.19  FAIL    5.60
 *     older      #64748b      4.76          3.75
 *
 * Two of the five glyphs were unreadable on their own badge — and the glyph
 * is not decoration. This module's own comment introduces it as the
 * non-colour channel provided "per this task's WCAG requirement", so the
 * accessibility mitigation was the part that failed.
 *
 * This is the same asymmetry `--color-status-on-solid` exists for (T-4208):
 * a fill light enough to need dark text and a fill dark enough to need light
 * text cannot share one on-colour. The buckets are canvas literals rather
 * than status tokens, so the pairing is written out per bucket here with the
 * measurement above rather than resolved from a token — but the rule is the
 * same one, and picking per fill is not optional.
 *
 * The colour scheme itself is deliberately NOT converted to severity tones
 * the way trafficMode and latencyMode were. Recency is not severity: a thing
 * changed a minute ago is not "critical", and painting it with the critical
 * token would say something false. Its chroma already falls monotonically
 * from justNow to older (0.215, 0.187, 0.157, 0.041), which is a real
 * ordering, and every mark carries a glyph and a text label besides. */
export function recencyGlyphColor(bucket: RecencyBucket): string {
  switch (bucket) {
    case "today":
    case "thisWeek":
      return "#0f172b";
    default:
      return "#ffffff";
  }
}

/** The one-character glyph drawn in a mark's corner badge — the non-color
 * channel diffMarkGlyph already establishes the precedent for. Each bucket
 * gets a distinct letter rather than a shape, since a screen magnifier or a
 * printed export renders text more reliably than a bespoke glyph shape. */
export function recencyMarkGlyph(bucket: RecencyBucket): string {
  switch (bucket) {
    case "drift":
      return "?";
    case "justNow":
      return "m";
    case "today":
      return "h";
    case "thisWeek":
      return "d";
    case "older":
      return "w";
  }
}

/** A short sentence for the overlay's own status line, mirroring
 * summarizeDiffOverlay's exact shape. */
export function summarizeRecencyOverlay(overlay: RecencyOverlay): string {
  const total = overlay.changedCount;
  if (total === 0) return "No changes in the lookback window.";
  const parts = [`${String(total)} ${total === 1 ? "entity" : "entities"} changed`];
  if (overlay.driftCount > 0) {
    parts.push(`${String(overlay.driftCount)} outside vnprox`);
  }
  if (overlay.offMap.length > 0) {
    parts.push(`${String(overlay.offMap.length)} not on the current map`);
  }
  return `${parts.join(" · ")}.`;
}
