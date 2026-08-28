// SPDX-License-Identifier: Apache-2.0

import type { ComponentPropsWithoutRef } from "react";
import * as RadixDialog from "@radix-ui/react-dialog";
import clsx from "clsx";
import { useDensity, type Density } from "./density";
import "./motion.css";

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
  return (
    <RadixDialog.Portal>
      {/* T-4206: `.motion-scrim` (motion.css) plays a real fade in/out
       * keyframe driven by the `--motion-*`/`--ease-*` tokens — no
       * `useReducedMotion()` check needed here: index.css's
       * `prefers-reduced-motion` block already zeroes those duration
       * tokens (and, as a blanket fallback, every animation-duration in
       * the app), so this collapses to instant for free. */}
      <RadixDialog.Overlay className="motion-scrim fixed inset-0 z-40 bg-black/50" />
      <RadixDialog.Content
        data-density={resolvedDensity}
        className={clsx(
          // T-3405: softer dialog — larger radius, a subtle shadow instead
          // of a heavy one, hairline border (docs/development.md "Visual
          // language" — "borders before shadows").
          // T-4203: a dialog is a floating layer above the page/raised
          // chrome beneath it, so it sits at the top of the surface ladder
          // (`surface-overlay`) in both themes with no `dark:` prefix.
          // T-4206: `.motion-dialog-surface` (motion.css) owns the
          // fixed/centered positioning's entrance+exit animation — see
          // that file's doc comment for why the centering transform lives
          // in the keyframe rather than a separate static utility.
          "motion-dialog-surface fixed left-1/2 top-1/2 z-50 w-full rounded-xl border shadow-lg",
          DENSITY_PADDING[resolvedDensity],
          widthClassName,
          "border-slate-200 bg-surface-overlay dark:border-slate-800",
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
      className={clsx("mt-1 text-sm text-fg-subtle", className)}
      {...props}
    />
  );
}
