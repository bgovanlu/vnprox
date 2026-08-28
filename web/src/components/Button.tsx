// SPDX-License-Identifier: Apache-2.0

import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";
import { useReducedMotion } from "../lib/useReducedMotion";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "destructive";
export type ButtonSize = "sm" | "md" | "lg";
export type ButtonShape = "default" | "pill";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** T-3405: page-action shape (docs/development.md "Visual language" —
   * "Page actions are pills"). "default" (`rounded-md`) is byte-for-byte
   * this component's original corner radius, so the prop is additive and
   * changes no existing call site's rendered output; only callers that opt
   * into `shape="pill"` (a whole-page action, not an in-form/in-table one)
   * pick up `rounded-pill` (the `--radius-pill` token from index.css,
   * T-3401). Selected via a lookup table, not appended to `className`
   * alongside the base `rounded-md` — see the `density` prop's doc comment
   * below for why two same-specificity radius utilities can't just be
   * layered and left to source order. */
  shape?: ButtonShape;
  /** T-905: compact/comfortable spacing (density.ts) — orthogonal to
   * `size` (which sets the sm/md/lg scale a density then tightens or
   * keeps as-is). Defaults to the ambient `<DensityProvider>` in scope, or
   * "comfortable" absent one — comfortable's classes are byte-for-byte
   * this component's original sizeClasses, so introducing this prop is
   * additive and changes no existing call site's rendered output.
   * Nested per size+density (not layered on top of `sizeClasses` via a
   * second, competing `h-*`/`px-*` utility) because two same-specificity
   * Tailwind utilities for the same property are not guaranteed to
   * override by source order — see DialogContent's `widthClassName` doc
   * comment for the same precedent in this codebase. */
  density?: Density;
}

const variantClasses: Record<ButtonVariant, string> = {
  // This variant's `bg-accent-600 text-white` is the pairing the accent
  // ramp is SOLVED AROUND, so it is the reason the identity can change
  // without this file changing. The history is worth knowing before
  // touching either number:
  //
  //   T-905   accent was blue; blue-600 measured below AA's 4.5:1, which
  //           forced the resting state up to accent-700.
  //   T-3401  re-pointed the alias blue -> indigo, invalidating that
  //           measurement.
  //   T-3405  re-measured indigo (6.44:1 at 600) and moved the resting
  //           state back down to accent-600.
  //   T-4201  replaced the stock-indigo alias with vnprox's own signal
  //           azure. Cyan is intrinsically light at a given OKLCH
  //           lightness, so a naive azure ramp measured 3.67:1 here and
  //           would have forced a fourth move. Instead L(600) and L(700)
  //           were solved against these very targets, landing accent-600
  //           at 4.94:1 vs white — so this line, and the ~10 other solid
  //           `bg-accent-600 text-white` controls, did not move at all.
  //
  // Hover brightens to accent-500 (3.09:1): a hover state is transient
  // and not the gating resting contrast, as it was not at T-3405 either.
  //
  // Every ratio above is now recomputed by index.css.test.ts rather than
  // recorded here by hand. Four separate hand-derivations of this same
  // pairing (T-905, T-3401, T-3405, T-3406) were each correct and each
  // left nothing behind that would catch the next one; do not start a
  // fifth. Change the token, run the test.
  primary:
    "bg-accent-600 text-white hover:bg-accent-500 focus-visible:outline-accent-500 disabled:bg-accent-600/50",
  secondary:
    "bg-slate-200 text-slate-900 hover:bg-slate-300 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700",
  ghost:
    "bg-transparent text-slate-700 hover:bg-slate-200/70 dark:text-slate-200 dark:hover:bg-slate-800/70",
  destructive: "bg-red-600 text-white hover:bg-red-500 focus-visible:outline-red-500",
};

const sizeClasses: Record<ButtonSize, Record<Density, string>> = {
  sm: { comfortable: "h-8 px-2.5 text-sm gap-1.5", compact: "h-7 px-2 text-xs gap-1" },
  md: { comfortable: "h-9 px-3.5 text-sm gap-2", compact: "h-8 px-2.5 text-sm gap-1.5" },
  lg: { comfortable: "h-11 px-5 text-base gap-2", compact: "h-9 px-3.5 text-sm gap-1.5" },
};

// T-3405: `--radius-pill` (index.css, T-3401) generates a real `rounded-pill`
// Tailwind v4 utility (verified with a build probe against this repo's
// tailwindcss package — `--radius-*` tokens in `@theme` are picked up
// automatically), so this uses the named utility rather than falling back
// to `rounded-full`.
const shapeClasses: Record<ButtonShape, string> = {
  default: "rounded-md",
  pill: "rounded-pill",
};

/** The one button component every feature uses (docs/development.md:
 * "Components: function components only"). Keep app-specific styling out
 * of call sites — add a variant here instead of one-off className hacks. */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, variant = "secondary", size = "md", shape = "default", density, type = "button", ...props },
  ref,
) {
  const resolvedDensity = useDensity(density);
  // T-905: reduced motion drops the hover/focus color transition to an
  // instant state change rather than an eased one.
  const reducedMotion = useReducedMotion();
  return (
    <button
      ref={ref}
      type={type}
      data-density={resolvedDensity}
      className={clsx(
        "inline-flex items-center justify-center",
        shapeClasses[shape],
        "font-medium",
        reducedMotion ? "transition-none" : "transition-colors",
        "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2",
        "disabled:cursor-not-allowed disabled:opacity-60",
        variantClasses[variant],
        sizeClasses[size][resolvedDensity],
        className,
      )}
      {...props}
    />
  );
});
