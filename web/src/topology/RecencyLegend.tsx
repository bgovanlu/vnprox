// SPDX-License-Identifier: Apache-2.0

// T-3908's recency-overlay legend: the non-color channel this task's WCAG
// requirement asks for. Every bucket's swatch carries the same letter glyph
// the canvas badge draws (recencyMarkGlyph) plus its full text phrase, so a
// user who cannot distinguish the five colors — or who is reading this in a
// screen reader, where the swatch's `aria-hidden` background color carries
// no information at all — still gets the complete signal from the text
// alone. Always rendered as real DOM (not canvas), so it needs no special
// a11y bridging the way the map's own canvas pixels do.
import { recencyBucketPhrase, recencyMarkColor, recencyMarkGlyph, type RecencyBucket } from "./recencyOverlay";

const LEGEND_BUCKETS: readonly RecencyBucket[] = ["justNow", "today", "thisWeek", "older", "drift"];

export function RecencyLegend() {
  return (
    <div
      className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-slate-300 bg-slate-50 px-3 py-2 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 print:hidden"
      aria-label="Recency overlay legend"
    >
      {LEGEND_BUCKETS.map((bucket) => (
        <span key={bucket} className="inline-flex items-center gap-1.5">
          <span
            aria-hidden="true"
            className="flex h-4 w-4 items-center justify-center rounded-full text-[10px] font-bold text-white"
            style={{ backgroundColor: recencyMarkColor(bucket) }}
          >
            {recencyMarkGlyph(bucket)}
          </span>
          <span>{recencyBucketPhrase(bucket)}</span>
        </span>
      ))}
      <span className="inline-flex items-center gap-1.5">
        <span
          aria-hidden="true"
          className="h-4 w-4 rounded-full border border-dashed border-slate-400 dark:border-slate-500"
        />
        <span>no change in the lookback window</span>
      </span>
    </div>
  );
}
