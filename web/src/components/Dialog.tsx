// SPDX-License-Identifier: Apache-2.0

import type { ComponentPropsWithoutRef } from "react";
import * as RadixDialog from "@radix-ui/react-dialog";
import clsx from "clsx";
import { useDensity, type Density } from "./density";
import { useReducedMotion } from "../lib/useReducedMotion";

// react-refresh's only-export-components rule can't see that these are
// components — they're re-exported references to Radix's, not functions
// defined in this file — and flags the whole compound-component-module
// pattern (Dialog/DialogTrigger/DialogClose alongside the styled
// DialogContent below) as a Fast Refresh boundary risk. Accepted
// tradeoff for this pattern; disabled per-line rather than for the file
// so a genuinely non-component export here would still be caught.
// eslint-disable-next-line react-refresh/only-export-components
export const Dialog = RadixDialog.Root;
// eslint-disable-next-line react-refresh/only-export-components
export const DialogTrigger = RadixDialog.Trigger;
// eslint-disable-next-line react-refresh/only-export-components
export const DialogClose = RadixDialog.Close;

export interface DialogContentProps extends ComponentPropsWithoutRef<typeof RadixDialog.Content> {
  /** Overrides the default `max-w-lg` sizing. A distinct prop rather than
   * relying on `className`'s own `max-w-*` to win via source order: Tailwind
   * utility precedence for two same-specificity `max-w-*` classes depends on
   * generated-stylesheet order, not JSX concatenation order, so silently
   * appending a wider `max-w-*` to `className` is not guaranteed to
   * override the base class (T-403's zone wizards needed a wider dialog for
   * the form+live-preview split and found this the safe way to get it,
   * without adding a class-merging dependency (`tailwind-merge`) beyond
   * docs/development.md's locked stack). */
  widthClassName?: string;
  /** T-905: compact/comfortable padding (density.ts) — "comfortable" is
   * this component's original `p-6`, so the prop is additive. Defaults to
   * the ambient `<DensityProvider>` in scope. */
  density?: Density;
}

const DENSITY_PADDING: Record<Density, string> = { comfortable: "p-6", compact: "p-4" };

export function DialogContent({
  className,
  widthClassName = "max-w-lg",
  density,
  children,
  ...props
}: DialogContentProps) {
  const resolvedDensity = useDensity(density);
  // T-905: `prefers-reduced-motion: reduce` drops the open/close
  // fade — the dialog still opens/closes instantly, just without the
  // animation classes.
  const reducedMotion = useReducedMotion();
  return (
    <RadixDialog.Portal>
      <RadixDialog.Overlay
        className={clsx(
          "fixed inset-0 z-40 bg-black/50",
          !reducedMotion &&
            "data-[state=open]:animate-in data-[state=open]:fade-in data-[state=closed]:animate-out data-[state=closed]:fade-out",
        )}
      />
      <RadixDialog.Content
        data-density={resolvedDensity}
        className={clsx(
          // T-3405: softer dialog — larger radius, a subtle shadow instead
          // of a heavy one, hairline border (docs/development.md "Visual
          // language" — "borders before shadows").
          "fixed left-1/2 top-1/2 z-50 w-full -translate-x-1/2 -translate-y-1/2 rounded-xl border shadow-lg",
          DENSITY_PADDING[resolvedDensity],
          widthClassName,
          "border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900",
          "focus:outline-none",
          className,
        )}
        {...props}
      >
        {children}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  );
}

export function DialogTitle({ className, ...props }: ComponentPropsWithoutRef<typeof RadixDialog.Title>) {
  return (
    <RadixDialog.Title
      className={clsx("text-base font-semibold text-slate-900 dark:text-slate-100", className)}
      {...props}
    />
  );
}

export function DialogDescription({
  className,
  ...props
}: ComponentPropsWithoutRef<typeof RadixDialog.Description>) {
  return (
    <RadixDialog.Description
      className={clsx("mt-1 text-sm text-slate-500 dark:text-slate-400", className)}
      {...props}
    />
  );
}
