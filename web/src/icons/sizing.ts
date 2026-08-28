// SPDX-License-Identifier: Apache-2.0

// Split out of Icon.tsx (which otherwise mixes a component export with
// these plain helpers, tripping react-refresh/only-export-components) —
// the detailed-vs-simplified sizing rule every glyph's interior branches on.

/** Below this pixel size a pictogram swaps its full-detail interior for a
 * simplified one — interior lines that read as texture at 32-96px collapse
 * into mud at 16px. 20, not 16, so the swap already covers lucide-react's
 * own default render size (20px), not just vnprox's 16px "inline icon"
 * spec size — see each glyph module's comment for what it drops. */
export const INLINE_THRESHOLD = 20;

/** True once `size` crosses INLINE_THRESHOLD — the one predicate every
 * glyph's detailed/simplified branch shares, so "what counts as small"
 * can never drift kind-by-kind. */
export function isDetailed(size: number | undefined): boolean {
  return (size ?? 24) > INLINE_THRESHOLD;
}
