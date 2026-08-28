// SPDX-License-Identifier: Apache-2.0

// T-4208. Grounded in five independent boxed-message components that
// already converge on "a rounded box, a border, a tone-matched wash, tone-
// matched text" — two of them (the ones this component's default styling
// matches exactly) are already on the semantic status scale, three are
// still raw amber and are exactly the drift T-4204 exists to end:
//
//   - topology/StalenessBanner.tsx / topology/LldpSetupBanner.tsx — already
//     `border-status-degraded bg-status-degraded-soft text-status-degraded`,
//     the pairing this component generalizes.
//   - firewall/Banner.tsx's `FirewallBanners`, changesets/LockNoticeBanner.tsx,
//     settings/platformCommon.tsx's `RefusalNotice` — still
//     `border-amber-300 bg-amber-50 text-amber-800`, raw-colour siblings of
//     the same shape.
//
// Deliberately does NOT reproduce demo/DemoBanner.tsx's or
// layout/OfflineShellBanner.tsx's fixed-position, full-width "pill badge +
// message" bar: those two are page CHROME (`fixed inset-x-0 top-0 z-40`,
// theme-independent in DemoBanner's case on purpose — see its own comment)
// rather than a boxed status message, so positioning stays the caller's
// concern. Their shared "pill-badge-plus-message" layout is still
// available here via the `badge` prop.
import type { ReactNode } from "react";
import clsx from "clsx";
import type { StatusTone } from "./statusTone";
import { useDensity, type Density } from "./density";

const TONE_CLASSES: Record<StatusTone, string> = {
  ok: "border-status-ok bg-status-ok-soft text-status-ok",
  degraded: "border-status-degraded bg-status-degraded-soft text-status-degraded",
  critical: "border-status-critical bg-status-critical-soft text-status-critical",
  info: "border-status-info bg-status-info-soft text-status-info",
  unknown: "border-status-unknown bg-status-unknown-soft text-status-unknown",
};

// A banner announcing a problem (degraded/critical) is assertive by
// default; everything else is a polite status update — matching the
// role="alert" vs role="status" split CountdownBanner.tsx already makes by
// hand between its awaiting-confirm and its applying/outcome states.
const DEFAULT_ROLE: Record<StatusTone, "status" | "alert"> = {
  ok: "status",
  degraded: "alert",
  critical: "alert",
  info: "status",
  unknown: "status",
};

export interface BannerProps {
  tone: StatusTone;
  children: ReactNode;
  title?: ReactNode;
  /** A short leading label pill — DemoBanner.tsx/OfflineShellBanner.tsx's
   * "pill-badge-plus-message" layout, without their fixed positioning. */
  badge?: string;
  actions?: ReactNode;
  role?: "status" | "alert";
  onDismiss?: () => void;
  dismissLabel?: string;
  /** T-4207: joins the density.ts seam (T-905). Comfortable is byte-for-byte
   * this component's original `px-3 py-2 text-sm gap-2`, so the prop is
   * additive. Compact tightens padding and gap and steps the body text down
   * a notch, the same shape SegmentedControl's compact step uses. */
  density?: Density;
  className?: string;
}

// Comfortable is byte-for-byte the pre-T-4207 hardcoded
// `gap-2 px-3 py-2 text-sm`.
const DENSITY_CLASSES: Record<Density, string> = {
  comfortable: "gap-2 px-3 py-2 text-sm",
  compact: "gap-1.5 px-2 py-1.5 text-xs",
};

/** A boxed, tone-matched status message — the shared shape behind
 * StalenessBanner/LldpSetupBanner (already on-token) and FirewallBanners/
 * LockNoticeBanner/RefusalNotice (still raw amber). Not page chrome: no
 * fixed positioning, so it composes wherever the caller places it. */
export function Banner({ tone, children, title, badge, actions, role, onDismiss, dismissLabel = "Dismiss", density, className }: BannerProps) {
  const resolvedDensity = useDensity(density);
  return (
    <div
      role={role ?? DEFAULT_ROLE[tone]}
      data-density={resolvedDensity}
      className={clsx("flex flex-wrap items-start rounded-md border", DENSITY_CLASSES[resolvedDensity], TONE_CLASSES[tone], className)}
    >
      {badge ? (
        <span
          className={clsx(
            "shrink-0 rounded-full px-2 py-0.5 text-xs font-semibold tracking-wide uppercase",
            tone === "ok" && "bg-status-ok-solid text-status-on-solid",
            tone === "degraded" && "bg-status-degraded-solid text-status-on-solid",
            tone === "critical" && "bg-status-critical-solid text-status-on-solid",
            tone === "info" && "bg-status-info-solid text-status-on-solid",
            tone === "unknown" && "bg-status-unknown-solid text-status-on-solid",
          )}
        >
          {badge}
        </span>
      ) : null}
      <div className="min-w-0 flex-1">
        {title ? <p className="font-medium">{title}</p> : null}
        <div>{children}</div>
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      {onDismiss ? (
        <button
          type="button"
          aria-label={dismissLabel}
          onClick={onDismiss}
          className="shrink-0 rounded px-1 text-xs font-medium underline hover:no-underline"
        >
          {dismissLabel}
        </button>
      ) : null}
    </div>
  );
}
