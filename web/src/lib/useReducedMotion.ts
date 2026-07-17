// T-905's reduced-motion seam: a single place every animated affordance in
// the app (map pan/zoom easing, the drift/mgmt "pulse" treatment, dialog/
// toast transitions) reads to decide whether motion is allowed, so
// `prefers-reduced-motion: reduce` disables it uniformly rather than each
// call site hand-rolling its own `matchMedia` check. Deliberately JS-level
// (not just Tailwind's CSS-only `motion-reduce:` variant): components that
// need to skip an *imperative* animation (React Flow's `fitView({
// duration })`, a canvas rAF/interval-driven pulse) can't do that with CSS
// alone, and a single JS source of truth keeps the CSS-driven and
// JS-driven motion in sync with the same signal.
import { useEffect, useState } from "react";

const QUERY = "(prefers-reduced-motion: reduce)";

/** One-shot, non-reactive read (SSR/test-safe: `matchMedia` may not exist,
 * e.g. under jsdom without a mock — treated as "motion allowed", the safer
 * default absent evidence the user asked for less of it). */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
  try {
    return window.matchMedia(QUERY).matches;
  } catch {
    return false;
  }
}

/** Reactive hook: re-renders if the OS-level setting changes while the app
 * is open (rare, but cheap to support via the standard `change` event). */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(prefersReducedMotion);

  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    let mql: MediaQueryList;
    try {
      mql = window.matchMedia(QUERY);
    } catch {
      return;
    }
    const handler = () => {
      setReduced(mql.matches);
    };
    mql.addEventListener("change", handler);
    return () => {
      mql.removeEventListener("change", handler);
    };
  }, []);

  return reduced;
}

/** Named motion budget derived from the reduced-motion signal — the one
 * place "what does 'reduced motion' turn off" is spelled out, so every
 * consumer (map fitView easing, the drift/mgmt pulse, dialog/toast
 * transitions) reads the same policy instead of re-deciding it:
 *   - `fitDurationMs`: React Flow's `fitView({ duration })` / any
 *     programmatic pan-zoom easing — 0 collapses it to an instant jump.
 *   - `pulseEnabled`: status-pulse treatments (the amber mgmt/corosync
 *     carrier, the drift dashed outline, the awaiting-confirm countdown
 *     banner) — false falls back to their static equivalent (the badge/
 *     dashed-border/banner still renders, just without the animation).
 *   - `transitionsEnabled`: plain CSS opacity/color transitions on
 *     interactive chrome (buttons, faceplates) — false so hover/selection
 *     state changes are instant rather than eased.
 */
export interface MotionBudget {
  fitDurationMs: number;
  pulseEnabled: boolean;
  transitionsEnabled: boolean;
}

export function motionConfig(reducedMotion: boolean): MotionBudget {
  return {
    fitDurationMs: reducedMotion ? 0 : 500,
    pulseEnabled: !reducedMotion,
    transitionsEnabled: !reducedMotion,
  };
}
