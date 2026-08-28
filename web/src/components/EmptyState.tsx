// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";
import { EmptyIllustration, type EmptyStateVariant } from "./emptystate/EmptyIllustration";
import type { PictogramKind } from "../icons/registry";

export type { EmptyStateVariant } from "./emptystate/EmptyIllustration";

export interface EmptyStateProps {
  title: string;
  description?: string;
  /** T-4209: exactly one next action, if this domain has one. Not a slot
   * for multiple buttons — an empty state earns its keep by pointing at a
   * single correct next step, not by offering a menu. Omit entirely where
   * no action is genuinely correct (a "pick one from the list" hint, for
   * instance) rather than inventing one. */
  action?: ReactNode;
  className?: string;
  /** T-905: compact/comfortable spacing (density.ts) — "comfortable" is
   * this component's original `min-h-[16rem] p-10` scale, so the prop is
   * additive. "compact" is for a small inline empty state (e.g. a tile in
   * a dense dashboard grid) rather than a full-page placeholder. Defaults
   * to the ambient `<DensityProvider>` in scope. */
  density?: Density;
  /** T-4209: the T-4205 pictogram that seeds this empty state's
   * illustration — the domain "noun" (bridge, zone, WireGuard peer, ...).
   * Omit to keep the plain text-only rendering: a loading placeholder or a
   * "pick one from the list" master-detail hint is not a domain empty
   * state and doesn't want a picture. */
  icon?: PictogramKind;
  /** T-4209: which of the four situations this is (empty / filtered to
   * nothing / not configured / failed to load) — changes the illustration's
   * badge and tone. Defaults to "empty". No effect without `icon`. */
  variant?: EmptyStateVariant;
}

const DENSITY_CLASSES: Record<Density, string> = {
  comfortable: "min-h-[16rem] gap-2 p-10",
  compact: "min-h-[8rem] gap-1 p-4",
};

/** Generic "nothing here (yet)" panel — used by every placeholder page in
 * this task, and by the keyboard framework's "not yet implemented"
 * affordances elsewhere in the app. */
export function EmptyState({ title, description, action, className, density, icon, variant }: EmptyStateProps) {
  const resolvedDensity = useDensity(density);
  const compact = resolvedDensity === "compact";
  return (
    <div
      data-density={resolvedDensity}
      className={clsx(
        // T-3405: larger radius to match Dialog/Drawer/Toast's softer look.
        "flex h-full flex-col items-center justify-center rounded-xl border border-dashed text-center",
        DENSITY_CLASSES[resolvedDensity],
        "border-slate-300 dark:border-slate-700",
        className,
      )}
    >
      {icon ? (
        <EmptyIllustration icon={icon} variant={variant ?? "empty"} compact={compact} />
      ) : null}
      <h2 className="text-lg font-semibold text-slate-800 dark:text-slate-100">{title}</h2>
      {description ? (
        // T-3406: same fix as PageHeader's description line and for the
        // same reason — this component has no background of its own (only
        // a dashed border), so a full-page empty state renders it directly
        // on AppShell's `bg-slate-100`, where text-slate-500 measures
        // 4.34:1 against the 4.5:1 AA floor. slate-600 clears it; dark
        // mode (slate-400 on bg-slate-900) is untouched.
        <p className="max-w-md text-sm text-slate-600 dark:text-slate-400">{description}</p>
      ) : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}
