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
      className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-amber-500/60 bg-amber-500/15 px-3 py-1.5 text-sm text-amber-900 dark:text-amber-100"
    >
      <span className="rounded bg-amber-500 px-1.5 py-0.5 text-xs font-semibold tracking-wide text-amber-950 uppercase">
        {DEMO_BANNER_LABEL}
      </span>
      <span>
        This is a synthetic cluster built into vnprox. No Proxmox VE node is connected, nothing here is real, and
        every change you make is reported rather than applied.
      </span>
    </div>
  );
}
