import type { ReactNode } from "react";
import * as RadixTooltip from "@radix-ui/react-tooltip";
import clsx from "clsx";

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
}

/** The one tooltip component every disabled-affordance explanation uses
 * (docs/user-guide.md §5's "tooltip naming the missing privilege").
 * Self-contained: each instance brings its own RadixTooltip.Provider, so
 * call sites (and their tests) never need an app-level provider mounted —
 * the Provider's only cross-instance feature (shared open-delay grouping)
 * isn't something these one-off explanatory tooltips need. */
export function Tooltip({ children, content, side = "top" }: TooltipProps) {
  if (content === undefined) {
    return <>{children}</>;
  }
  return (
    <RadixTooltip.Provider delayDuration={300}>
      <RadixTooltip.Root>
        <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
        <RadixTooltip.Portal>
          <RadixTooltip.Content
            side={side}
            sideOffset={6}
            className={clsx(
              "z-50 max-w-xs rounded-md border px-2.5 py-1.5 text-xs shadow-lg",
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
