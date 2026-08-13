// T-2801: whether this daemon is a demo, and the app-wide consequences.
//
// Read once, from GET /health (the API's only unauthenticated route), at
// app mount. Not TanStack Query: this is a property of the *daemon*, fixed
// for the process's whole lifetime — refetching it on window focus, or
// keeping it in a cache with a stale time, would be answering a question
// that cannot change with machinery designed for questions that do.
import { create } from "zustand";

import { fetchHealth } from "../api/health";

interface DemoState {
  /** undefined until /health has answered. Distinguished from false on
   * purpose: "not a demo" and "we do not know yet" must not render the
   * same, or the banner would flicker in on every load of a demo instance
   * after the page had already been read as real. */
  demo: boolean | undefined;
  setDemo: (demo: boolean) => void;
}

export const useDemoStore = create<DemoState>()((set) => ({
  demo: undefined,
  setDemo: (demo) => {
    set({ demo });
  },
}));

/** True iff this daemon is running in demo mode. */
export function useIsDemo(): boolean {
  return useDemoStore((s) => s.demo === true);
}

/** Fetches /health once and records the demo flag, then stamps
 * `<html class="demo">` so the demo accent colour applies (see
 * src/index.css). Called exactly once, from App.
 *
 * A failure is deliberately swallowed to `false`: if /health cannot be
 * reached, the app has bigger problems than a missing banner, and every
 * other screen will say so far more usefully than a banner claiming to
 * know something it does not. */
export async function detectDemoMode(): Promise<void> {
  let demo: boolean;
  try {
    const health = await fetchHealth();
    demo = health.demo === true;
  } catch {
    demo = false;
  }
  useDemoStore.getState().setDemo(demo);
  document.documentElement.classList.toggle("demo", demo);
}
