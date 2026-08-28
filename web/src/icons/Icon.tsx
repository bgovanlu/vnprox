// SPDX-License-Identifier: Apache-2.0

// The shared SVG shell every pictogram in this set renders through — the
// single place viewBox/stroke/currentColor conventions live, so no
// individual glyph can drift off the family's optical weight (T-4205,
// planning/roadmap-visual.md's Phase 42 "Design language": the pictogram set
// must read as one family with lucide-react's stroke weight — lucide stays
// in use for generic UI actions — driven entirely by `currentColor`, never a
// hardcoded hex, since the accent identity is decided by a parallel card and
// this set must not pre-empt it).
import type { ReactNode, SVGProps } from "react";

export interface PictogramProps extends Omit<SVGProps<SVGSVGElement>, "viewBox" | "stroke" | "fill" | "children"> {
  /** Rendered width/height in px. Also selects the simplified-vs-detailed
   * interior (see sizing.ts's INLINE_THRESHOLD/isDetailed). Defaults to 24
   * — lucide-react's own
   * default — so dropping a pictogram into inline UI text needs no extra
   * prop. The three sizes this set is designed for: ~16px inline icon,
   * ~32-48px canvas node glyph, ~96px+ illustration seed — the last two
   * both render the same "detailed" interior, since the simplification is
   * about legibility at small sizes, not about scaling detail up further. */
  size?: number;
  /** Accessible name. Omit for the overwhelming majority of call sites — a
   * pictogram almost always sits beside a text label (node title, table
   * cell) that already names the entity, so by default the glyph renders
   * `aria-hidden` and contributes nothing to the accessibility tree. Pass
   * this only where the glyph is the *sole* identifier of the entity kind
   * (e.g. a bare legend swatch with no adjacent text). */
  title?: string;
}

interface IconShellProps extends PictogramProps {
  children: ReactNode;
}

/** The literal `<svg>` every glyph renders into: a fixed 24x24 grid, 2px
 * round-capped/joined stroke (lucide-react's own defaults, so the two sets
 * sit together without a visible weight mismatch), `currentColor` for both
 * stroke and any fill a glyph uses — never a literal color — so the
 * caller's CSS color alone determines the rendered color in both themes and
 * under any accent override. Not exported outside this module's siblings;
 * every glyph component wraps its own shape data in this shell rather than
 * being handed raw `<svg>` control, which is what keeps the whole set's
 * grid/stroke/color contract impossible to violate by accident. */
export function IconShell({ size = 24, title, children, ...rest }: IconShellProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      role={title !== undefined ? "img" : undefined}
      aria-hidden={title === undefined ? true : undefined}
      {...rest}
    >
      {title !== undefined ? <title>{title}</title> : null}
      {children}
    </svg>
  );
}
