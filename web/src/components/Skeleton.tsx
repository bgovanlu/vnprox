// SPDX-License-Identifier: Apache-2.0

// T-4208. Unlike this file's siblings, there is no hand-rolled Skeleton to
// promote — every loading state in this app today is literal "Loading…"
// text (83 call sites at the time of writing). It is built anyway because
// the roadmap card names it explicitly and T-4907 ("Skeleton loading",
// Phase 49) depends on it existing; what grounds its SHAPE instead of a
// call site is `dashboard/DashboardTile.tsx`'s own `isLoading` branch (the
// one shared shell every dashboard tile already renders through) — a
// future T-4907 adopter replaces that branch's "Loading…" text with this.
//
// Reduced motion: deliberately NO `useReducedMotion()` check here. The
// shimmer is plain Tailwind `animate-pulse` (the same utility
// CountdownBanner.tsx/EntityNode.tsx/SwitchFaceplate.tsx already use for
// their own pulse treatments), and index.css's global reduced-motion gate
// (T-4206) already catches it: `*, *::before, *::after { animation-duration:
// 0.01ms !important; animation-iteration-count: 1 !important }` under
// `prefers-reduced-motion: reduce` applies to ANY animation-duration,
// Tailwind-built-in or custom, not only ones driven through the
// `--motion-*` tokens. A frozen, single-iteration pulse IS this
// component's static reduced-motion rendering — there is no second,
// differently-shaped representation to hand-author, which is exactly the
// case docs/design-language.md §5 says the hook is for skipping.
import clsx from "clsx";

export type SkeletonVariant = "text" | "circle" | "rect";

export interface SkeletonProps {
  variant?: SkeletonVariant;
  /** CSS width/height (e.g. "60%", "3rem", 120). Sensible defaults apply
   * per variant when omitted. */
  width?: string | number;
  height?: string | number;
  /** `variant="text"` only: stack N lines, the last one shortened — the
   * common "a paragraph is loading" shape. */
  lines?: number;
  className?: string;
}

const VARIANT_SHAPE: Record<SkeletonVariant, string> = {
  text: "rounded-sm h-3.5 w-full",
  circle: "rounded-full h-8 w-8",
  rect: "rounded-md h-20 w-full",
};

function Block({ variant, width, height, className }: { variant: SkeletonVariant; width?: string | number; height?: string | number; className?: string }) {
  return (
    <span
      aria-hidden
      className={clsx("block animate-pulse bg-slate-200 dark:bg-slate-800", VARIANT_SHAPE[variant], className)}
      style={{ width, height }}
    />
  );
}

/** A loading placeholder shaped like the content it stands in for. Purely
 * decorative (`aria-hidden`) — the loading STATE is announced by the
 * surrounding component (a `role="status"`/`aria-busy` container), never by
 * the placeholder shapes themselves. */
export function Skeleton({ variant = "text", width, height, lines = 1, className }: SkeletonProps) {
  if (variant === "text" && lines > 1) {
    return (
      <span className={clsx("flex flex-col gap-1.5", className)}>
        {Array.from({ length: lines }, (_, i) => (
          // A fixed-count, order-stable placeholder list — index is a
          // legitimate stable key here, nothing about these rows reorders.
          <Block key={i} variant="text" width={i === lines - 1 ? (width ?? "70%") : width} height={height} />
        ))}
      </span>
    );
  }
  return <Block variant={variant} width={width} height={height} className={className} />;
}
