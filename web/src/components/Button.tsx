import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";
import { useReducedMotion } from "../lib/useReducedMotion";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "destructive";
export type ButtonSize = "sm" | "md" | "lg";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
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
  // T-905 (axe color-contrast): white-on-accent-600 measured 3.76:1 in the
  // v2-canvas toolbar's active toggle — below WCAG AA's 4.5:1 for this ~14px
  // text. accent-700 clears it (~5.8:1) while keeping the same blue identity;
  // hover brightens to accent-600 (a hover state is transient and not the
  // gating resting contrast).
  primary:
    "bg-accent-700 text-white hover:bg-accent-600 focus-visible:outline-accent-500 disabled:bg-accent-700/50",
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

/** The one button component every feature uses (docs/development.md:
 * "Components: function components only"). Keep app-specific styling out
 * of call sites — add a variant here instead of one-off className hacks. */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { className, variant = "secondary", size = "md", density, type = "button", ...props },
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
        "inline-flex items-center justify-center rounded-md font-medium",
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
