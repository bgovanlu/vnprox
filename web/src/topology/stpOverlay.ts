// SPDX-License-Identifier: Apache-2.0

// T-3901's STP/RSTP badge vocabulary (internal/topology/project.go's
// badgesOf/stpPortBadges): a bridge node carries "stp-root" when it is the
// elected root of its L2 domain; a bridge's EdgePortOf membership edges
// carry "stp-state=<state>" and "stp-role=<role>". Pure parsing helpers,
// kept out of EntityNode.tsx/EntityEdge.tsx so "is this the loop-breaking
// blocked port" is unit-testable without React — the same split
// findingBadges.ts's parseFindingBadge establishes for the map's other
// structured badge vocabularies.
//
// Both badges are only ever emitted when the owning bridge has STP
// administratively on (internal/host's evidence transcript,
// planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt: every
// bridge trivially reports itself root when STP is off, since there's no
// protocol running to elect a real one) — so this module never needs its
// own "is STP even enabled" gate; the backend already applied it.

export const STP_ROOT_BADGE = "stp-root";
const STP_ROLE_PREFIX = "stp-role=";
const STP_STATE_PREFIX = "stp-state=";

export type StpPortRole = "root" | "designated" | "blocking" | "disabled";

/** True when this bridge node carries the "stp-root" badge — this bridge is
 * the elected root of its L2 domain. */
export function isStpRootNode(badges: readonly string[]): boolean {
  return badges.includes(STP_ROOT_BADGE);
}

/** Parses a `"stp-role=<role>"` edge badge, or undefined if none present. */
export function stpPortRole(badges: readonly string[]): StpPortRole | undefined {
  for (const b of badges) {
    if (b.startsWith(STP_ROLE_PREFIX)) {
      return b.slice(STP_ROLE_PREFIX.length) as StpPortRole;
    }
  }
  return undefined;
}

/** Parses a `"stp-state=<state>"` edge badge, or undefined if none present. */
export function stpPortState(badges: readonly string[]): string | undefined {
  for (const b of badges) {
    if (b.startsWith(STP_STATE_PREFIX)) {
      return b.slice(STP_STATE_PREFIX.length);
    }
  }
  return undefined;
}

/** The "first question in any L2 loop hunt" edge: this port is blocking to
 * prevent a loop. */
export function isStpBlockingEdge(badges: readonly string[]): boolean {
  return stpPortRole(badges) === "blocking";
}

/** Humanizes one `"stp-root"`/`"stp-role=…"`/`"stp-state=…"` badge token
 * into edge/chip label text ("STP root", "STP blocking", "forwarding"), or
 * undefined for any other badge vocabulary word — callers fall back to the
 * raw token (or drop it) the way every other unrecognized badge already
 * does. */
export function stpBadgeLabel(token: string): string | undefined {
  if (token === STP_ROOT_BADGE) return "STP root";
  if (token.startsWith(STP_ROLE_PREFIX)) return `STP ${token.slice(STP_ROLE_PREFIX.length)}`;
  if (token.startsWith(STP_STATE_PREFIX)) return token.slice(STP_STATE_PREFIX.length);
  return undefined;
}
