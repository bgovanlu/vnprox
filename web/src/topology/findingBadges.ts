// SPDX-License-Identifier: Apache-2.0

// T-3501's shared badge vocabulary: the pure, framework-free helpers both
// renderers (SwitchFaceplate.tsx, EntityNode.tsx/EntityEdge.tsx/canvasDraw.ts)
// and the a11y bridge (a11yBridge.ts) use so a map entity says which KIND of
// finding it carries and HOW BAD, instead of the single bare "drift" wire
// badge that (per T-602's own comment) had come to mean "any open finding
// from any producer" while still literally rendering the word "drift" and
// pulsing uniformly regardless of severity.
//
// Wire shape (docs/api.md, internal/topology.FindingBadge): `badges[]`
// carries one "finding:<source>:<severity>" token per distinct source (worst
// severity for that source) PLUS the legacy bare "drift" token, unconditionally
// present whenever the entity carries at least one open finding of any kind —
// kept wire-present, unchanged trigger condition, for backward compatibility
// with any consumer outside this frontend that reads docs/api.md's originally
// -documented meaning (see internal/api/topology.go's findingBadge doc
// comment). This module is what stops that legacy token from driving any of
// THIS frontend's own rendering decisions going forward — this frontend now
// keys off the new, more precise "finding:" tokens (parseFindingBadge et al.)
// wherever it can, and falls back to the legacy bare token only where no
// producer emits the richer form yet (web/src/wireguard/wgEdgeStatus.ts's
// client-synthesized "drift" edge badge for wg_endpoint_drift — see
// hasOpenFinding/pulseSeverity's fallback branches).
import type { FindingBadge, Severity } from "../api/types";

export const FINDING_BADGE_PREFIX = "finding:";

/** The legacy wire badge (docs/api.md, internal/api/topology.go's
 * `findingBadge` constant) — still present on the wire for back-compat, but
 * this frontend no longer renders it as literal text or keys a status
 * decision off it alone where a `"finding:<source>:<severity>"` token is
 * available. Exported so call sites name it once rather than re-typing the
 * string literal. */
export const LEGACY_DRIFT_BADGE = "drift";

export interface ParsedFindingBadge {
  source: string;
  severity: Severity;
}

const KNOWN_SEVERITIES: ReadonlySet<string> = new Set<Severity>(["error", "warning", "info"]);

/** Parses one `badges[]` token; returns undefined for anything that isn't a
 * well-formed `"finding:<source>:<severity>"` token (including the legacy
 * bare `"drift"` token and every other badge vocabulary word — mgmt,
 * corosync, mgmt-path, qos-shaped, count=N, ...). `source` may itself
 * contain no further ":" (internal/findings.Source values never do). */
export function parseFindingBadge(token: string): ParsedFindingBadge | undefined {
  if (!token.startsWith(FINDING_BADGE_PREFIX)) return undefined;
  const rest = token.slice(FINDING_BADGE_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  if (sep <= 0 || sep === rest.length - 1) return undefined;
  const source = rest.slice(0, sep);
  const severity = rest.slice(sep + 1);
  if (!KNOWN_SEVERITIES.has(severity)) return undefined;
  return { source, severity: severity as Severity };
}

/** Every well-formed `"finding:<source>:<severity>"` token in badges,
 * parsed. Order follows the input array (the backend sorts sources, so this
 * is deterministic already). */
export function parsedFindingBadges(badges: readonly string[]): ParsedFindingBadge[] {
  const out: ParsedFindingBadge[] = [];
  for (const b of badges) {
    const p = parseFindingBadge(b);
    if (p) out.push(p);
  }
  return out;
}

/** True if this entity carries at least one open finding of any kind —
 * either the new per-source tokens, or (fallback, for a producer that has
 * not been given the richer form yet, e.g. wgEdgeStatus.ts's synthesized WG
 * tunnel edges) the legacy bare "drift" token alone. This is the
 * "should this entity get the dashed-outline affordance at all" question —
 * source-agnostic, matching the affordance's pre-T-3501 behavior exactly. */
export function hasOpenFinding(badges: readonly string[]): boolean {
  return badges.includes(LEGACY_DRIFT_BADGE) || parsedFindingBadges(badges).length > 0;
}

const SEVERITY_RANK: Record<Severity, number> = { info: 0, warning: 1, error: 2 };

/** The worst severity among this entity's `"finding:"` tokens, or undefined
 * if there are none (which includes the legacy-badge-only case — that case
 * carries no severity information at all, so callers must not guess one). */
export function worstFindingSeverity(badges: readonly string[]): Severity | undefined {
  let worst: Severity | undefined;
  for (const { severity } of parsedFindingBadges(badges)) {
    if (worst === undefined || SEVERITY_RANK[severity] > SEVERITY_RANK[worst]) worst = severity;
  }
  return worst;
}

/** T-3501's "make the pulse mean something": motion is reserved for the
 * severity that warrants it (error), not painted uniformly across every
 * open finding the way the single bare "drift" badge did. When no severity
 * information is available at all (the legacy-badge-only fallback —
 * wgEdgeStatus.ts today), this preserves the pre-T-3501 "pulse on any open
 * finding" behavior rather than silently going still: a producer that
 * hasn't been upgraded to the richer form yet should not lose its motion
 * cue, only the newly-added severity precision. `prefers-reduced-motion`
 * is applied by the caller (EntityNode.tsx/SwitchFaceplate.tsx already do,
 * via useReducedMotion) — this function only answers "would this entity
 * pulse if motion were allowed". */
export function shouldPulse(badges: readonly string[]): boolean {
  const worst = worstFindingSeverity(badges);
  if (worst !== undefined) return worst === "error";
  return badges.includes(LEGACY_DRIFT_BADGE);
}

/** A colourblind-safe glyph per severity (Phase 34: "a red LED and a green
 * LED are identical to a colourblind operator — shape or glyph must carry
 * it too") — a filled square reads as the most alarming shape, a triangle
 * as caution, a dot as informational, independent of the colour classes
 * `findingBadgeClass` pairs them with. */
export const SEVERITY_GLYPH: Record<Severity, string> = {
  error: "■",
  warning: "▲",
  info: "●",
};

export const SEVERITY_WORD: Record<Severity, string> = {
  error: "error",
  warning: "warning",
  info: "info",
};

/** Chip colour classes for one severity — T-4204's semantic status scale
 * (index.css's `-soft` wash + bare fg pairing), the same vocabulary status
 * painting uses everywhere else (EntityNode.tsx's STATUS_CLASSES,
 * docs/development.md's Visual language section: "Status colours ... are a
 * separate vocabulary and never substitute for the accent"). Colour is
 * additive to SEVERITY_GLYPH, never the only signal.
 *
 * These were `bg-*-200 text-*-900 dark:bg-*-900 dark:text-*-100` — opaque,
 * deliberately (see the AA-contrast history this comment used to carry),
 * which is exactly the `-soft`/bare pairing index.css's status scale
 * computes and asserts AA for already, so the opacity reasoning now lives
 * there instead of being re-derived per call site. `info` maps onto
 * `status-info` rather than the old neutral slate: severity "info" is a
 * real rung on this same ok/degraded/critical/info/unknown/stale scale,
 * not a fourth, differently-sourced neutral. */
export function findingBadgeClass(severity: Severity): string {
  switch (severity) {
    case "error":
      return "bg-status-critical-soft text-status-critical";
    case "warning":
      return "bg-status-degraded-soft text-status-degraded";
    case "info":
    default:
      return "bg-status-info-soft text-status-info";
  }
}

// T-702: distinct treatment for the management-path badge vocabulary
// (docs/features/topology.md §3) — "mgmt"/"corosync" mark the carrier
// itself, "mgmt-path" marks every physical entity behind it. Amber (not the
// plain grey every other badge renders as) so a glance at the map answers
// "which interface carries this node's management/corosync traffic, and
// what's physically behind it" without opening the inspector.
//
// T-3406-followup-02: this table, `isMgmtBadge`, and the amber class string
// below used to be written out independently in SwitchFaceplate.tsx (three
// places) and EntityNode.tsx (one place) — literal copies of what was, at
// the time, also findingBadgeClass's own `"warning"` case. The August 2026
// opacity fix (bddc74eb) touched every copy but one, and the one it missed
// (findingBadges.ts's own canonical `findingBadgeClass`) was the one an
// axe re-run could still see, because the fix swept `*.tsx` and this was
// the `.ts` original. `findingBadgeClass`'s "warning" case has since been
// given one extra step of contrast (`text-amber-900`/`dark:text-amber-100`)
// that this mgmt/corosync/mgmt-path vocabulary never needed — the two have
// genuinely diverged, so this stays its own constant rather than reusing
// `findingBadgeClass("warning")`, but it is now written exactly once.
export const MGMT_BADGE_LABEL: Record<string, string> = {
  mgmt: "management IP",
  corosync: "corosync link",
  "mgmt-path": "on the management path",
};

export function isMgmtBadge(badge: string): boolean {
  return badge in MGMT_BADGE_LABEL;
}

/** The opaque amber pair every mgmt/corosync/mgmt-path badge renders —
 * 5.70:1 in light mode, well clear of AA (see the opacity-fix note above
 * for why it must stay opaque rather than `/70`). Exported as a bare
 * constant (not only via `mgmtBadgeClass`) because some call sites
 * (SwitchFaceplate.tsx's NicPort mgmt-path chip) always render this exact
 * badge and have no "else" branch to switch on. */
export const MGMT_BADGE_CLASS = "bg-amber-200 text-amber-800 dark:bg-amber-900 dark:text-amber-200";

/** Chip colour classes for one wire-vocabulary badge token that is neither a
 * `"finding:"` token nor rendered by a more specific chip of its own: amber
 * for the mgmt/corosync/mgmt-path trio, plain slate otherwise. Used where a
 * badge is rendered generically (SwitchFaceplate.tsx's chassis header) —
 * call sites that already know they are in the mgmt branch can use
 * `MGMT_BADGE_CLASS` directly instead. */
export function mgmtBadgeClass(badge: string): string {
  return isMgmtBadge(badge)
    ? MGMT_BADGE_CLASS
    : "bg-slate-200 text-slate-600 dark:bg-slate-700 dark:text-slate-300";
}

/** The chip's visible text: glyph + source name (e.g. "▲ health",
 * "■ drift") — the source word is what tells the operator *which* checker
 * flagged this, replacing the single word "drift" every source used to
 * render regardless of what actually fired. */
export function findingChipText(parsed: ParsedFindingBadge): string {
  return `${SEVERITY_GLYPH[parsed.severity]} ${parsed.source}`;
}

/** The finding(s) of one source among an entity's full `findings[]` list
 * (topology/types.go's `Node.Findings`/`Edge.Findings`) — used to build the
 * chip's hover title / aria phrase from the finding's own `detail` text
 * (T-3501 AC4: "the operator should not have to leave the map"). */
export function findingsForSource(findings: readonly FindingBadge[] | undefined, source: string): FindingBadge[] {
  if (!findings) return [];
  return findings.filter((f) => f.source === source);
}

/** The hover-tooltip / aria-description text for one source's chip: every
 * matching finding's own detail, semicolon-joined (almost always exactly
 * one finding per source on a given entity in practice, but never assumed).
 * Falls back to a generic "<severity> <source> finding" sentence when no
 * `findings[]` detail is available at all (an entity painted only via the
 * legacy fallback, or a `findings[]`-less older backend). */
export function findingDetailText(parsed: ParsedFindingBadge, findings: readonly FindingBadge[] | undefined): string {
  const matches = findingsForSource(findings, parsed.source);
  const details = matches.map((f) => f.detail).filter((d) => d !== "");
  if (details.length > 0) return details.join("; ");
  return `${SEVERITY_WORD[parsed.severity]} ${parsed.source} finding`;
}
