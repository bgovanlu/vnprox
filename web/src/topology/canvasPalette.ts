// SPDX-License-Identifier: Apache-2.0

// T-4301: the canvas's colours, resolved from the design language instead of
// transcribed from it.
//
// A <canvas> cannot consume Tailwind utilities — it needs values, not class
// names — so for as long as this file did not exist the only route from
// index.css to the map was somebody copying hex across by hand. T-4204 did
// exactly that for the six finding-badge fields, under a comment promising
// they were "kept in sync by hand". Measured afterwards: three of the twelve
// values matched the token they named, and the dark critical badge was 78 RGB
// units from the colour it claimed to be. The tokens had not changed since,
// so the copy was wrong the day it was written. A comment promising manual
// synchronisation is not a synchronisation mechanism.
//
// So: read the custom properties off the document at run time. This gets the
// four mode combinations right for free — html.dark, html.demo, and the
// html.demo.dark case that has no branch anywhere in this codebase — because
// the cascade has already resolved them by the time getComputedStyle runs.
// Drawing code never asks which theme it is in again.
import type { SceneTheme } from "./canvasDraw";

/** Substituted for any token that will not resolve, and deliberately
 * hideous. The alternative is a hardcoded fallback palette, which is the
 * hand-copy this module exists to delete — a fallback that drifts is worse
 * than no fallback, because it renders plausibly while being wrong. Magenta
 * renders implausibly. Under jsdom, where custom properties never resolve and
 * there is no 2D context to draw into, nothing is painted with it. */
const UNRESOLVED = "#ff00ff";

/** Reads one custom property. Injectable so tests can drive the mapping
 * without a layout engine: jsdom's getComputedStyle returns "" for every
 * `--*`, which is the same reason index.css.test.ts parses the stylesheet as
 * text rather than resolving it. */
export type TokenReader = (customProperty: string) => string;

export function documentTokenReader(root: Element): TokenReader {
  const style = getComputedStyle(root);
  return (name) => style.getPropertyValue(name).trim();
}

let warned = false;

/** Maps a `SceneTheme` field to the design-language role it should take.
 *
 * Every entry here is a decision, and the ones that moved a value are the
 * point of the card:
 *
 *   kindText     --color-fg-subtle   was #94a3b8 light / #64748b dark, which
 *                measured 2.56 and 3.07 against the node fill it is drawn on.
 *                It is the entity kind at 9px and the port speed at 8px, so
 *                AA applies in full and it was failing in BOTH themes.
 *   nodeBorderOk --color-outline     was 1.48 light / 1.93 dark against a 3:1
 *                floor for non-text content (WCAG 1.4.11).
 *   edgeDefault  --color-outline     was 2.45 / 2.36. These are the links of
 *                a network topology tool; if any graphic in this product is
 *                "required to understand the content", it is an edge.
 *
 * The rest resolve to what they already were, or within a step of it, so the
 * map does not change character — the same principle T-4201 used when it
 * solved the accent ramp against its existing call sites rather than moving
 * them. */
const ROLE: Record<keyof SceneTheme, string> = {
  background: "--color-surface-page",
  nodeFill: "--color-surface-raised",
  nodeText: "--color-fg-body",
  kindText: "--color-fg-subtle",
  nodeBorderOk: "--color-outline",
  statusDown: "--color-status-critical",
  statusDegraded: "--color-status-degraded",
  statusUnknown: "--color-status-unknown",
  badgeBg: "--color-border",
  badgeText: "--color-fg-muted",
  edgeDefault: "--color-outline",
  findingErrorFill: "--color-status-critical-soft",
  findingErrorText: "--color-status-critical",
  findingWarningFill: "--color-status-degraded-soft",
  findingWarningText: "--color-status-degraded",
  findingInfoFill: "--color-status-info-soft",
  findingInfoText: "--color-status-info",

  // T-4301 leaves these two literal, on purpose, and they are the only pair
  // in this table that is not a role lookup.
  //
  // `mgmt` is not a status and not the brand — it marks a management or
  // corosync path. The design language has semantic status colours and one
  // brand accent and nothing for "these are different kinds of thing".
  // Pointing these at `status-degraded` because both happen to be amber would
  // state something false — that a management path is a warning.
  //
  // T-4302 answered the categorical-scale question this comment used to
  // defer, and answered it "no such scale can exist here" — eleven hues at
  // the status scale's own 40deg separation needs 440deg of a 360deg circle.
  // Kind moved to SHAPE instead (canvasGlyphs.ts), which is why the
  // `KIND_ACCENT` this comment once pointed at is gone. That does not rescue
  // this pair: mgmt marks an EDGE, and an edge has no room for a glyph, so
  // the one categorical distinction the map still draws in colour is the one
  // with nowhere else to go. It stays visibly exceptional in this table
  // rather than being quietly folded into a status role it does not mean.
  mgmtBadgeBg: "",
  mgmtBadgeText: "",
};

const MGMT_BADGE = {
  light: { bg: "#fde68a", text: "#92400e" },
  dark: { bg: "#78350f", text: "#fde68a" },
} as const;

/** Resolves the whole scene palette in ONE pass.
 *
 * Called on mount and on theme change, never per frame. `getComputedStyle`
 * forces a style recalculation, and `themeColors()` used to be invoked three
 * times inside the draw callbacks — cheap when it returned an object literal,
 * a per-frame layout thrash if it resolved properties. T-4107's
 * 50-node/5000-guest envelope is the budget this must not spend. */
export function resolveSceneTheme(read: TokenReader, isDark: boolean): SceneTheme {
  const missing: string[] = [];
  const value = (field: keyof SceneTheme): string => {
    const property = ROLE[field];
    const resolved = read(property);
    if (resolved === "") {
      missing.push(property);
      return UNRESOLVED;
    }
    return resolved;
  };

  const mgmt = isDark ? MGMT_BADGE.dark : MGMT_BADGE.light;
  const theme: SceneTheme = {
    background: value("background"),
    nodeFill: value("nodeFill"),
    nodeText: value("nodeText"),
    kindText: value("kindText"),
    nodeBorderOk: value("nodeBorderOk"),
    statusDown: value("statusDown"),
    statusDegraded: value("statusDegraded"),
    statusUnknown: value("statusUnknown"),
    badgeBg: value("badgeBg"),
    badgeText: value("badgeText"),
    mgmtBadgeBg: mgmt.bg,
    mgmtBadgeText: mgmt.text,
    edgeDefault: value("edgeDefault"),
    findingErrorFill: value("findingErrorFill"),
    findingErrorText: value("findingErrorText"),
    findingWarningFill: value("findingWarningFill"),
    findingWarningText: value("findingWarningText"),
    findingInfoFill: value("findingInfoFill"),
    findingInfoText: value("findingInfoText"),
  };

  // Named once, not per-token and not per-frame: a stylesheet either loaded
  // or it did not, and seventeen identical warnings would bury the one fact
  // worth reading.
  if (missing.length > 0 && !warned) {
    warned = true;
    // Distinct tokens, not lookups: two fields resolve `--color-outline`, so
    // counting occurrences would report 17 for 14 names and read as a bug in
    // the message rather than in the stylesheet.
    const names = [...new Set(missing)];
    // console.warn, not slog: this is the browser, the map is about to render
    // magenta, and a silent fallback is exactly the failure mode this module
    // exists to prevent.
    console.warn(
      `canvasPalette: ${String(names.length)} design tokens did not resolve ` +
        `(${names.join(", ")}). The topology map will draw in ${UNRESOLVED} ` +
        `rather than guess.`,
    );
  }

  return theme;
}
