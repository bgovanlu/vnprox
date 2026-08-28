// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from "react";
import * as RadixTooltip from "@radix-ui/react-tooltip";
import clsx from "clsx";
import { useDensity, type Density } from "./density";

export interface TooltipProps {
  /** The element the tooltip is anchored to — typically a disabled button
   * wrapped in a plain <span> (a disabled native <button> doesn't fire
   * pointer events Radix's tooltip trigger needs, so callers gating an
   * affordance on a missing capability should wrap the disabled control in
   * a <span> and put the tooltip on that span, not the button itself). */
  children: ReactNode;
  /** Tooltip text. When undefined, renders `children` with no tooltip at
   * all (the common "not disabled, nothing to explain" case) rather than
   * an empty bubble. */
  content: string | undefined;
  side?: "top" | "right" | "bottom" | "left";
  /** T-905: compact/comfortable padding (density.ts) — "comfortable" is
   * this component's original `px-2.5 py-1.5`, so the prop is additive.
   * Defaults to the ambient `<DensityProvider>` in scope. */
  density?: Density;
}

const DENSITY_PADDING: Record<Density, string> = { comfortable: "px-2.5 py-1.5", compact: "px-2 py-1" };

/** The one tooltip component every disabled-affordance explanation uses
 * (docs/user-guide.md §5's "tooltip naming the missing privilege").
 * Self-contained: each instance brings its own RadixTooltip.Provider, so
 * call sites (and their tests) never need an app-level provider mounted —
 * the Provider's only cross-instance feature (shared open-delay grouping)
 * isn't something these one-off explanatory tooltips need. */
export function Tooltip({ children, content, side = "top", density }: TooltipProps) {
  const resolvedDensity = useDensity(density);
  if (content === undefined) {
    return <>{children}</>;
  }
  return (
    <RadixTooltip.Provider delayDuration={300}>
      <RadixTooltip.Root>
        <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
        <RadixTooltip.Portal>
          <RadixTooltip.Content
            data-density={resolvedDensity}
            side={side}
            sideOffset={6}
            className={clsx(
              // T-3405: larger radius, subtler shadow — the tooltip stays
              // an inverted (always-dark) chip in both themes, unchanged.
              "z-50 max-w-xs rounded-lg border text-xs shadow-md",
              DENSITY_PADDING[resolvedDensity],
              "border-slate-700 bg-slate-900 text-slate-100 dark:border-slate-600 dark:bg-slate-800",
            )}
          >
            {content}
            <RadixTooltip.Arrow className="fill-slate-900 dark:fill-slate-800" />
          </RadixTooltip.Content>
        </RadixTooltip.Portal>
      </RadixTooltip.Root>
    </RadixTooltip.Provider>
  );
}
