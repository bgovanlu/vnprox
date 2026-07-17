// T-909's responsive-triage seam: the one place "is this a narrow (phone/
// small-tablet) viewport" is decided, so every consumer (the route guard,
// NavRail's reachable-page filter, ToolsPage's restricted Findings-only
// render, the changeset drawer's drafting-time edit controls) reads the
// same signal instead of each hand-rolling its own breakpoint. Mirrors
// lib/useReducedMotion.ts's shape deliberately (JS-level `matchMedia`, not
// Tailwind's CSS-only `md:` variant): route-level decisions here are about
// which *component tree* renders, not just how one already-rendered tree is
// styled, so it has to be readable in JS.
//
// 768px (Tailwind's `md` breakpoint) is the cutoff: phones and portrait
// small tablets fall below it, matching the task card's "tablet/phone
// layout for on-call triage" framing. Below it, the app's writable surface
// is restricted to Dashboard, Findings (a read-only view of /tools), and
// the changeset confirm/rollback/apply-an-existing-draft ceremony — see
// docs/features's responsive-triage section (this task's own report) for
// the full reachable-surface rationale.
import { useEffect, useState } from "react";

export const NARROW_VIEWPORT_QUERY = "(max-width: 767px)";

/** One-shot, non-reactive read (SSR/test-safe: `matchMedia` may not exist,
 * e.g. under jsdom without a mock — treated as "not narrow", the safer
 * default for every existing test that doesn't stub matchMedia at all,
 * matching prefersReducedMotion()'s identical fallback). */
export function isNarrowViewport(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
  try {
    return window.matchMedia(NARROW_VIEWPORT_QUERY).matches;
  } catch {
    return false;
  }
}

/** Reactive hook: re-renders as the viewport crosses the breakpoint (window
 * resize, device rotation, or a real narrow device outright). */
export function useNarrowViewport(): boolean {
  const [narrow, setNarrow] = useState(isNarrowViewport);

  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    let mql: MediaQueryList;
    try {
      mql = window.matchMedia(NARROW_VIEWPORT_QUERY);
    } catch {
      return;
    }
    // Sync immediately on mount too — the initial useState read could have
    // raced a test's matchMedia stub being installed after first render.
    setNarrow(mql.matches);
    const handler = () => {
      setNarrow(mql.matches);
    };
    mql.addEventListener("change", handler);
    return () => {
      mql.removeEventListener("change", handler);
    };
  }, []);

  return narrow;
}
