// SPDX-License-Identifier: Apache-2.0

// T-4306: the path simulator's verdict palette, in one place.
//
// It was in four: `canvasDraw.ts`'s `SIM_STROKE` and `EntityEdge.tsx`'s
// `SIM_STROKE` as hex, and `EntityNode.tsx`'s `SIM_RING_CLASS` and
// `SIM_MARKER_CLASS` as Tailwind utilities. All four agreed — they were the
// same four Tailwind-500 defaults — which is the part worth noticing: they
// agreed by coincidence of everyone reaching for the same swatch, not by any
// mechanism. `STATUS_STROKE` had three copies that did NOT agree (T-4302), and
// nothing about this table made it less likely to drift, only luckier so far.
//
// **The three status-adjacent verdicts now resolve the status scale, because
// that is what they were already approximating.** Measured in OKLCH, the old
// literals sat 0.2-17deg from the status hue they mirror:
//
//     allow        emerald-500  162.5   vs  ok        145.1   (17.4)
//     deny         red-500       25.3   vs  critical   21.9   ( 3.5)
//     unreachable  amber-500     70.1   vs  degraded   70.3   ( 0.2)
//
// That closeness was deliberate — EntityEdge.tsx's own comment mapped the
// verdicts onto severity on purpose, and a `deny` SHOULD look like an error.
// Restating it as a private near-miss made the map carry a fourth copy of the
// status scale and spend three hues it did not need, in a palette T-4306
// measured as having none left to spend. Resolving the tokens says the same
// thing exactly instead of approximately.
//
// `indeterminate` is the exception and keeps a private hue: "the simulator
// could not decide" is not a health state, and painting it with any status
// token would state something false — the same reason T-4303 exempted
// `recency` from the severity scale. It stays a literal, but now in ONE place
// rather than four.
import type { SimVerdict } from "../api/types";

/** A design-token name, or the one literal below. Callers resolve it the way
 * their renderer can: the DOM through `var()`, the canvas through
 * `canvasPalette`. Same division of labour `trafficMode.ts`'s `toneVar`
 * already documents for the traffic overlay. */
export type SimTone = "status-ok" | "status-critical" | "status-degraded" | "sim-indeterminate";

const VERDICT_TONE: Record<SimVerdict, SimTone> = {
  allow: "status-ok",
  deny: "status-critical",
  unreachable: "status-degraded",
  indeterminate: "sim-indeterminate",
};

export function simVerdictTone(verdict: SimVerdict): SimTone {
  return VERDICT_TONE[verdict];
}

/** The one verdict with no token behind it (see the header). Exported so the
 * canvas palette and the DOM renderers share the value rather than each
 * keeping their own copy of the exception — which is how the four copies this
 * module replaced came to exist. */
export const SIM_INDETERMINATE_COLOR = "#8b5cf6"; // violet-500, OKLCH hue 292.7

/** The CSS value for a tone, for renderers that can use `var()`. */
export function simToneVar(tone: SimTone): string {
  return tone === "sim-indeterminate" ? SIM_INDETERMINATE_COLOR : `var(--color-${tone})`;
}

/** The Tailwind utility suffix for a tone, for `ring-*` / `bg-*` call sites.
 *
 * Returns the SUFFIX rather than a whole class name on purpose: Tailwind v4
 * resolves utilities by scanning source text, so an interpolated
 * `ring-${tone}` is never emitted. Every call site therefore writes its
 * classes out in full and uses this only to pick between them — see
 * `NoticeStack.tsx`, which had exactly this bug and the test that now forbids
 * it. */
export const SIM_TONE_SUFFIX: Record<SimTone, string> = {
  "status-ok": "status-ok",
  "status-critical": "status-critical",
  "status-degraded": "status-degraded",
  "sim-indeterminate": "violet-500",
};
