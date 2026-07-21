// Small display helpers shared by MicrosegPlanner + DryRunReport. Kept
// separate from the components so the "never round coverage to 100%/0"
// contract (T-1602's honesty fields, T-1603 AC1) is one tested function,
// not duplicated inline formatting.

/** Formats a byte count into a compact human string (matches FlowExplorer's
 * own byte formatting so the dry-run flow table reads identically to the
 * flow explorer T-1003 built). */
export function formatBytes(n: number): string {
  if (n < 1024) return `${String(n)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex] ?? "TB"}`;
}

/** Formats a unix-seconds timestamp as a local time string, the same as
 * FlowExplorer's flow rows. */
export function formatFlowTime(at: number): string {
  return new Date(at * 1000).toLocaleTimeString();
}

/** Renders a coverage fraction as a percentage string WITHOUT rounding a
 * genuinely-partial coverage up to "100%" or a genuinely-nonzero uncovered
 * tail down to nothing — the honesty contract T-1602 owns and T-1603 AC1
 * asserts. A value like 99.53 renders "99.53%", never "100%"; 99.996 renders
 * "99.996%" (kept precise near the ceiling), and a real 100.0 renders
 * "100%". Uses up to three fractional digits, trimming trailing zeros, and
 * deliberately floors the displayed value just below 100 when the true value
 * is under 100 so a 99.9996% coverage never reads as a flat "100%". */
export function formatCoveragePct(pct: number): string {
  if (pct >= 100) return "100%";
  // Round to 3 decimals for display, but never let rounding lift a
  // sub-100 value onto "100%": clamp the rounded result just below 100.
  let rounded = Math.round(pct * 1000) / 1000;
  if (rounded >= 100) rounded = 99.999;
  // Trim trailing zeros ("99.500" -> "99.5", "99.000" -> "99").
  const str = rounded.toFixed(3).replace(/\.?0+$/, "");
  return `${str}%`;
}
