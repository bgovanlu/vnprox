// SPDX-License-Identifier: Apache-2.0

// The fallback rendered for any kind not present in the PICTOGRAMS
// registry (registry.ts). Should never actually appear in production — the
// registry is meant to cover every kind the app models — but a lookup
// helper needs *something* to return rather than throwing, since a
// malformed/future backend `kind` string reaching the frontend is a data
// problem, not a reason to crash the canvas or a table row around it.
import { IconShell, type PictogramProps } from "./Icon";

/** Deliberately plain: a dashed, unmarked boundary — "something is here,
 * but this set doesn't know what it is" — never a question mark or an
 * error glyph, which would read as an alarm rather than a gap in the icon
 * set. Dashed for the same "observed/uncertain" reason ZoneIcon and
 * LldpNeighborIcon use it. */
export function UnknownPictogram(props: PictogramProps) {
  return (
    <IconShell {...props}>
      <rect x={4} y={4} width={16} height={16} rx={3} strokeDasharray="2 2" />
      <circle cx={12} cy={12} r={1.2} fill="currentColor" stroke="none" />
    </IconShell>
  );
}
