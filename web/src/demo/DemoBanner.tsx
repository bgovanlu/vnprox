// SPDX-License-Identifier: Apache-2.0

// T-2801: the persistent demo banner.
//
// "Demo mode is unmistakable" is the card's word, and unmistakable rules
// out three things this component deliberately does not do: it is not
// dismissible, it does not auto-hide, and it does not appear only on the
// first screen. A banner an operator can close is a banner that is absent
// exactly when someone else walks up to the screen.
//
// Rendered in normal document flow rather than as a fixed overlay, for the
// same reason OnboardingWalkthrough is (see AppShell.tsx's comment): every
// fixed corner of this app is already occupied by something clickable, and
// a banner that intercepts pointer events on the map's own controls is
// worse than one that takes 32 pixels of height.
//
// T-3403: restyled as the reference design's dark, full-width "You're
// testing" test-mode bar — a fixed dark-navy surface with an amber accent
// pill, rather than a translucent amber wash. Deliberately theme-INDEPENDENT
// (no `dark:` pairing): the bar looks the same whether the app chrome
// itself is in light or dark mode, same as Stripe's own sandbox banner does
// not re-tint with the dashboard around it, which sidesteps having to
// re-derive two separate contrast pairs for one message. Text is a plain
// light-on-near-black pairing (slate-200 on slate-950, ~16:1) so it reads
// regardless of ambient theme; only the amber pill's own colours (unchanged
// from before this restyle) needed re-verifying in isolation.
import { useIsDemo } from "./useDemoMode";

/** The banner's accessible name, used by the e2e sweep. Exported so the
 * spec and the component cannot disagree about it. */
export const DEMO_BANNER_LABEL = "Demo mode";

export function DemoBanner() {
  const isDemo = useIsDemo();
  if (!isDemo) return null;

  return (
    <div
      role="status"
      aria-label={DEMO_BANNER_LABEL}
      data-testid="demo-banner"
      className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-amber-500/30 bg-slate-950 px-3 py-1.5 text-sm text-slate-200"
    >
      <span className="rounded-full bg-amber-500 px-2 py-0.5 text-xs font-semibold tracking-wide text-amber-950 uppercase">
        {DEMO_BANNER_LABEL}
      </span>
      <span>
        This is a synthetic cluster built into vnprox. No Proxmox VE node is connected, nothing here is real, and
        every change you make is reported rather than applied.
      </span>
    </div>
  );
}
