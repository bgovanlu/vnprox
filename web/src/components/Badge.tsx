// SPDX-License-Identifier: Apache-2.0

// T-4208: the one status pill every page currently hand-rolls its own copy
// of. Grounded directly in three call sites that were each independently
// reinventing this component before it existed:
//
//   - topology/findingBadges.ts's `findingBadgeClass` — the map's severity
//     chips, already on the semantic status scale's soft role
//     (bg-status-*-soft text-status-*) but with no shared component behind
//     it, so every renderer (SwitchFaceplate.tsx, EntityNode.tsx) repeats
//     the className logic by hand.
//   - wireguard/WireGuardPage.tsx's `StateBadge` — up/down/unknown in raw
//     emerald/red/slate, the exact drift T-4204's token scale exists to end.
//   - dashboard/PluginTile.tsx's `SEVERITY_DOT` — info/warn/critical mapped
//     to status-* dots beside a plugin tile's value.
//
// The one nuance those call sites don't have to get right that this
// component does: the SOLID role's foreground colour is NOT a constant
// white. index.css.test.ts's own contrast assertions measure light-mode
// solid against WHITE but dark-mode solid against SLATE_900 — the dark
// palette's `-solid` steps are deliberately lighter/more saturated (so they
// read as "filled" against a dark page), which makes white-on-them fail AA.
// `text-status-on-solid` is therefore not a status/surface token
// getting a `dark:` prefix (the thing docs/design-language.md §2.2 forbids)
// — it is a plain neutral pairing layered on top of a status token that
// already switches itself, the same relationship Badge's soft role has to
// `text-status-*` (which needs no dark: prefix at all).
import type { ReactNode } from "react";
import clsx from "clsx";
import type { StatusTone } from "./statusTone";
import { useDensity, type Density } from "./density";

export type BadgeRole = "solid" | "soft";
export type BadgeSize = "sm" | "md";

export interface BadgeProps {
  status: StatusTone;
  /** "soft" (default) — a wash behind bare-token text, the role every
   * existing severity/state chip in this app already uses. "solid" — a
   * filled pill, for a single badge that needs to stand out on its own
   * (a table cell's only status indicator) rather than sit among several. */
  role?: BadgeRole;
  /** docs/design-language.md §2.2: "stale" is a freshness qualifier layered
   * on a real state, never a filled badge of its own — so this is a
   * boolean modifier on `status`, not a sixth `status` value. Renders as a
   * dashed ring plus reduced opacity over whatever `status`/`role` would
   * otherwise draw, which is the "desaturation or dashed border" the design
   * language specifies. */
  stale?: boolean;
  size?: BadgeSize;
  /** T-4207: joins the density.ts seam (T-905) that SegmentedControl and
   * KeyValue already read. Comfortable is byte-for-byte this component's
   * original `SIZE_CLASSES`, so the prop is additive. Nested per `size`
   * rather than layered, matching Button.tsx's `sizeClasses` precedent. */
  density?: Density;
  className?: string;
  children: ReactNode;
}

const SOFT_CLASSES: Record<StatusTone, string> = {
  ok: "bg-status-ok-soft text-status-ok",
  degraded: "bg-status-degraded-soft text-status-degraded",
  critical: "bg-status-critical-soft text-status-critical",
  info: "bg-status-info-soft text-status-info",
  unknown: "bg-status-unknown-soft text-status-unknown",
};

// See the file-level comment: the dark step is deliberately light text on a
// darker foreground pair, not white-on-color.
const SOLID_CLASSES: Record<StatusTone, string> = {
  ok: "bg-status-ok-solid text-status-on-solid",
  degraded: "bg-status-degraded-solid text-status-on-solid",
  critical: "bg-status-critical-solid text-status-on-solid",
  info: "bg-status-info-solid text-status-on-solid",
  unknown: "bg-status-unknown-solid text-status-on-solid",
};

// Comfortable is byte-for-byte the pre-T-4207 `SIZE_CLASSES`. Compact drops
// padding on both axes; `sm` is already at the smallest legible text step
// (`text-[11px]`) so it has nothing left to shed there, while `md` also
// steps its text down a notch, matching SegmentedControl's md compact step.
const SIZE_CLASSES: Record<BadgeSize, Record<Density, string>> = {
  sm: { comfortable: "px-1.5 py-0.5 text-[11px]", compact: "px-1 py-0 text-[11px]" },
  md: { comfortable: "px-2 py-0.5 text-xs", compact: "px-1.5 py-0 text-[11px]" },
};

/** The one status pill: a small filled label on the semantic status scale
 * (docs/design-language.md §2.2). Never used for anything that isn't a
 * health/severity signal — a categorical (non-health) tag is Chip, not
 * Badge with an invented status. */
export function Badge({ status, role = "soft", stale = false, size = "md", density, className, children }: BadgeProps) {
  const resolvedDensity = useDensity(density);
  return (
    <span
      data-density={resolvedDensity}
      className={clsx(
        "inline-flex w-fit shrink-0 items-center gap-1 rounded-full font-semibold whitespace-nowrap",
        SIZE_CLASSES[size][resolvedDensity],
        role === "solid" ? SOLID_CLASSES[status] : SOFT_CLASSES[status],
        stale && "border border-dashed border-status-stale opacity-75",
        className,
      )}
    >
      {children}
    </span>
  );
}
