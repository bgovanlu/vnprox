// SPDX-License-Identifier: Apache-2.0

import type { ComponentPropsWithoutRef } from "react";
import * as RadixDialog from "@radix-ui/react-dialog";
import clsx from "clsx";
import { useDensity, type Density } from "./density";
import "./motion.css";

// Radix has no dedicated "drawer/sheet" primitive; a drawer is a Dialog
// anchored to an edge of the viewport instead of centered, so it's built
// on the same @radix-ui/react-dialog primitive as components/Dialog.tsx
// (same focus-trap/portal/escape-to-close behavior for free). Used for
// the change drawer described in docs/user-guide.md §3 ("Edits collect
// in the change drawer (bottom right)").
//
// react-refresh's only-export-components rule can't see that these
// re-exported references are themselves components (see the identical
// note in components/Dialog.tsx); disabled per-line, accepted tradeoff.
// eslint-disable-next-line react-refresh/only-export-components
export const Drawer = RadixDialog.Root;
// eslint-disable-next-line react-refresh/only-export-components
export const DrawerTrigger = RadixDialog.Trigger;
// eslint-disable-next-line react-refresh/only-export-components
export const DrawerClose = RadixDialog.Close;

export type DrawerSide = "right" | "left" | "bottom";

// T-3405: softer drawer — the inner edge (the one not flush with the
// viewport edge) picks up the same larger radius Dialog uses, so a
// right/left drawer reads as a rounded panel rather than a hard-cornered
// sheet; the outer edges stay square since they're flush with the viewport.
// T-4206: each side's `motion-drawer-*` class (motion.css) slides in from
// the edge it's flush with and back out on close.
const sideClasses: Record<DrawerSide, string> = {
  right: "motion-drawer-right inset-y-0 right-0 h-full w-full max-w-md rounded-l-xl border-l",
  left: "motion-drawer-left inset-y-0 left-0 h-full w-full max-w-md rounded-r-xl border-r",
  bottom: "motion-drawer-bottom inset-x-0 bottom-0 max-h-[80vh] w-full rounded-t-xl border-t",
};

export interface DrawerContentProps extends ComponentPropsWithoutRef<typeof RadixDialog.Content> {
  side?: DrawerSide;
  /** T-905: compact/comfortable padding (density.ts) — "comfortable" is
   * this component's original `p-6`, so the prop is additive. Defaults to
   * the ambient `<DensityProvider>` in scope. */
  density?: Density;
}

const DENSITY_PADDING: Record<Density, string> = { comfortable: "p-6", compact: "p-4" };

export function DrawerContent({ className, side = "right", density, children, ...props }: DrawerContentProps) {
  const resolvedDensity = useDensity(density);
  return (
    <RadixDialog.Portal>
      {/* T-4206: see Dialog.tsx's identical comment — `.motion-scrim`
       * (motion.css) needs no `useReducedMotion()` check; index.css's
       * `prefers-reduced-motion` block already collapses it. */}
      <RadixDialog.Overlay className="motion-scrim fixed inset-0 z-40 bg-black/50" />
      <RadixDialog.Content
        data-density={resolvedDensity}
        className={clsx(
          // T-3405: subtle shadow + hairline border, matching Dialog.
          // T-4203: a drawer floats above the page the same way a dialog
          // does, so it takes the same top-of-ladder `surface-overlay`.
          "fixed z-50 flex flex-col overflow-y-auto shadow-lg",
          DENSITY_PADDING[resolvedDensity],
          "border-border bg-surface-overlay",
          "focus:outline-none",
          sideClasses[side],
          className,
        )}
        {...props}
      >
        {children}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  );
}

// eslint-disable-next-line react-refresh/only-export-components
export const DrawerTitle = RadixDialog.Title;
// eslint-disable-next-line react-refresh/only-export-components
export const DrawerDescription = RadixDialog.Description;
