// SPDX-License-Identifier: Apache-2.0

// T-4209: the illustration half of EmptyState.tsx. Composed entirely from
// the T-4205 pictogram set (the domain "noun" — bridge, VLAN, zone, ...) plus
// a small lucide-react badge (the "verb" — searched-and-found-nothing,
// not-set-up-yet, failed-to-load — docs/design-language.md §6's "lucide
// draws verbs, [pictograms] draw nouns" split, applied to a third context).
// No new icon set: every mark here is either a registry pictogram rendered
// large or one of the three lucide glyphs this module names.
import { AlertTriangle, CirclePlus, SearchX, type LucideIcon } from "lucide-react";
import { getPictogram, type PictogramKind } from "../../icons/registry";

/** The four situations T-4209's card asks EmptyState to distinguish. Each
 * gets its own badge and tone — never a `dark:` variant, per
 * docs/design-language.md §2.2: the status tokens already resolve under
 * `html.dark` on their own. */
export type EmptyStateVariant = "empty" | "filtered" | "unconfigured" | "failed";

interface BadgeSpec {
  readonly Glyph: LucideIcon;
  /** Bare (no `dark:`) — status tokens and accent ROLES both re-point per
   * theme, so one unprefixed utility is correct in both.
   *
   * This comment was true of the status half and false of the accent half for
   * as long as it existed: the line below it read
   * `text-accent-600 dark:text-accent-400`, because T-4201 shipped an accent
   * ramp and no aliases, and a ramp STEP is a value that cannot re-point.
   * T-4214 added the roles and this became true. Note the wording: the ramp
   * is still not pre-resolved and never will be — `--color-accent-fg` is. */
  readonly badgeClass: string;
}

const VARIANT_BADGE: Record<EmptyStateVariant, BadgeSpec | null> = {
  // A plain "nothing here yet" carries no badge — the domain glyph alone is
  // the whole story, same as a blank page.
  empty: null,
  // Filtered-to-nothing is a search that found nothing, not a failure.
  filtered: { Glyph: SearchX, badgeClass: "text-status-info" },
  // Not configured yet is an invitation, not a problem — accent, not status.
  unconfigured: { Glyph: CirclePlus, badgeClass: "text-accent-fg" },
  // Failed to load is the one genuinely bad state here.
  failed: { Glyph: AlertTriangle, badgeClass: "text-status-critical" },
};

/** The backdrop circle's tone. Only `failed` departs from the neutral
 * sunken surface every other variant shares — colour encodes the one
 * variant that means "something is wrong" (principle 2: colour is status). */
const VARIANT_CIRCLE_CLASS: Record<EmptyStateVariant, string> = {
  empty: "bg-surface-sunken",
  filtered: "bg-surface-sunken",
  unconfigured: "bg-surface-sunken",
  failed: "bg-status-critical-soft",
};

/** The domain glyph's own colour. `text-slate-600 dark:text-slate-400` is
 * the paired-and-guarded neutral (slateContrast.test.ts); `failed` instead
 * uses the critical status token so the one bad-news variant reads as bad
 * news at a glance, not just via its corner badge. */
const VARIANT_GLYPH_CLASS: Record<EmptyStateVariant, string> = {
  empty: "text-slate-600 dark:text-slate-400",
  filtered: "text-slate-600 dark:text-slate-400",
  unconfigured: "text-slate-600 dark:text-slate-400",
  failed: "text-status-critical",
};

export interface EmptyIllustrationProps {
  icon: PictogramKind;
  variant: EmptyStateVariant;
  /** T-905: shrinks the whole illustration for a compact/inline empty
   * state — see EmptyState.tsx's `density` prop. */
  compact?: boolean;
}

/** A domain pictogram rendered at illustration-seed size (docs/design-
 * language.md §6: pictograms are drawn to work at 96px+) inside a soft
 * circular backdrop, with a small badge in the corner naming which of the
 * four empty-state situations this is. Every colour is a token — never a
 * hardcoded hex — so it retints correctly under dark mode and the demo
 * accent with no `dark:` prefix beyond the two neutral pairings above. */
export function EmptyIllustration({ icon, variant, compact }: EmptyIllustrationProps) {
  const Glyph = getPictogram(icon);
  const badge = VARIANT_BADGE[variant];
  const circleSize = compact ? "h-16 w-16" : "h-24 w-24";
  const glyphSize = compact ? 32 : 48;
  const badgeBoxSize = compact ? "h-5 w-5" : "h-7 w-7";
  const badgeGlyphSize = compact ? 12 : 15;

  return (
    <div className={`relative shrink-0 ${circleSize}`} aria-hidden="true">
      <div
        className={`flex h-full w-full items-center justify-center rounded-full ${VARIANT_CIRCLE_CLASS[variant]} ${VARIANT_GLYPH_CLASS[variant]}`}
      >
        <Glyph size={glyphSize} />
      </div>
      {badge ? (
        <div
          className={`absolute -bottom-1 -right-1 flex ${badgeBoxSize} items-center justify-center rounded-full bg-surface-raised ring-2 ring-surface-page ${badge.badgeClass}`}
        >
          <badge.Glyph size={badgeGlyphSize} strokeWidth={2.5} />
        </div>
      ) : null}
    </div>
  );
}
