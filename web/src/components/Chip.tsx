// SPDX-License-Identifier: Apache-2.0

// T-4208, promoted from settings/platformCommon.tsx's `ScopeChip`/
// `ScopeChips` — "a neutral pill for a scope/capability/extension-point
// name" was already exactly this component, just not shared. Generalized
// with two more tones seen at other hand-rolled call sites doing the same
// job with a different palette:
//
//   - ipam/AddressList.tsx's FILTERS row — a single-select filter chip,
//     accent-tinted when active (`tone="accent"`).
//   - ScopeChip's own `tone="removed"` (a struck-through name in a diff-like
//     view) stays, unchanged in meaning.
//
// Chip is for a NAME or TAG — a scope, a capability, an object reference —
// never a health signal; that vocabulary is Badge's.
import type { ReactNode } from "react";
import clsx from "clsx";
import { useDensity, type Density } from "./density";

export type ChipTone = "neutral" | "accent" | "removed";

export interface ChipProps {
  children: ReactNode;
  tone?: ChipTone;
  /** ScopeChip's original rendering was always `font-mono` (scope/capability
   * names read as identifiers). Default true for the same reason; a plain
   * free-text tag can opt out. */
  mono?: boolean;
  /** Present = removable. Rendered as a trailing "x" button; `removeLabel`
   * is its accessible name (defaults to "Remove" — pass something more
   * specific, e.g. `Remove ${name}`, when several chips render side by
   * side so a screen reader user can tell them apart). */
  onRemove?: () => void;
  removeLabel?: string;
  /** T-4207: joins the density.ts seam (T-905). Comfortable is byte-for-byte
   * this component's original padding, so the prop is additive. Only
   * padding moves — `mono` already owns the text-size decision
   * (`text-[11px]` vs `text-xs`), so density doesn't fight it for the same
   * property. */
  density?: Density;
  className?: string;
}

const TONE_CLASSES: Record<ChipTone, string> = {
  neutral: "bg-slate-100 text-fg-muted dark:bg-slate-800",
  removed: "bg-slate-100 text-fg-muted line-through dark:bg-slate-800",
  accent: "border border-accent-500 bg-accent-soft text-accent-fg",
};

// Comfortable is byte-for-byte the pre-T-4207 hardcoded `px-2 py-0.5`.
// Compact tightens padding only; text size stays `mono`'s call (see the
// `density` prop's doc comment).
const PADDING_CLASSES: Record<Density, string> = { comfortable: "px-2 py-0.5", compact: "px-1.5 py-0" };

/** A small pill for a name or tag — a scope, a capability, a filter, an
 * object reference. Not a status indicator; see Badge for that. */
export function Chip({ children, tone = "neutral", mono = true, onRemove, removeLabel = "Remove", density, className }: ChipProps) {
  const resolvedDensity = useDensity(density);
  return (
    <span
      data-density={resolvedDensity}
      className={clsx(
        "inline-flex w-fit shrink-0 items-center gap-1 rounded-full text-xs font-medium whitespace-nowrap",
        PADDING_CLASSES[resolvedDensity],
        mono && "font-mono text-[11px] font-normal",
        TONE_CLASSES[tone],
        className,
      )}
    >
      {children}
      {onRemove ? (
        <button
          type="button"
          aria-label={removeLabel}
          onClick={onRemove}
          className={clsx(
            "-mr-1 inline-flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full",
            "hover:bg-black/10 dark:hover:bg-white/10",
            "transition-colors duration-[var(--motion-fast)] ease-standard",
          )}
        >
          <svg aria-hidden viewBox="0 0 24 24" className="h-2.5 w-2.5" fill="none" stroke="currentColor" strokeWidth={3} strokeLinecap="round">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      ) : null}
    </span>
  );
}
