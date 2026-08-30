// SPDX-License-Identifier: Apache-2.0

// T-4301. Two things need proving here, and neither is "the mapping table has
// the entries I typed into it".
//
// 1. Every SceneTheme field actually resolves — a field missing from the ROLE
//    table, or pointed at a token that does not exist, must not fall through
//    to something that renders plausibly.
// 2. The values the canvas ends up drawing with clear their contrast floors,
//    measured against the surfaces the canvas actually draws them on. This is
//    the check that did not exist before: every contrast gate in this repo
//    scans Tailwind class names, and a <canvas> has none, which is how the
//    node outline came to be 1.48:1 and the default edge stroke 2.4:1 with a
//    green suite.
//
// The token VALUES are parsed out of index.css rather than resolved, for the
// same reason index.css.test.ts does it: jsdom returns "" for every custom
// property. So this file drives the real resolver with a reader backed by the
// real stylesheet — which is a stronger test than stubbing values, because a
// token renamed in index.css and not here fails.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { resolveSceneTheme, type TokenReader } from "./canvasPalette";
import type { SceneTheme } from "./canvasDraw";

const CSS = readFileSync(resolve(__dirname, "..", "index.css"), "utf8");

/** The declarations inside one top-level block, by custom-property name. */
function blockTokens(selector: string): Map<string, string> {
  const opener = new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m");
  const found = opener.exec(CSS);
  if (found === null) throw new Error(`${selector} not found in index.css`);
  const open = CSS.indexOf("{", found.index);
  const close = CSS.indexOf("}", open);
  const body = CSS.slice(open + 1, close);
  const out = new Map<string, string>();
  for (const m of body.matchAll(/(--[a-z0-9-]+)\s*:\s*(#[0-9a-f]{6})\s*;/g)) {
    const [, name, value] = m;
    if (name !== undefined && value !== undefined) out.set(name, value);
  }
  return out;
}

/** A reader over the cascade as the browser would resolve it: `html.dark`
 * overrides `@theme`, which is exactly what getComputedStyle would hand back
 * on a dark document. */
function readerFor(isDark: boolean): TokenReader {
  const base = blockTokens("@theme");
  const merged = new Map(base);
  if (isDark) for (const [k, v] of blockTokens("html.dark")) merged.set(k, v);
  return (name) => merged.get(name) ?? "";
}

function relLum(hex: string): number {
  const chan = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const lin = chan.map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * (lin[0] ?? 0) + 0.7152 * (lin[1] ?? 0) + 0.0722 * (lin[2] ?? 0);
}
function contrast(a: string, b: string): number {
  const [hi, lo] = [relLum(a), relLum(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}

const THEMES = [
  { name: "light", dark: false },
  { name: "dark", dark: true },
] as const;

describe("canvasPalette (T-4301)", () => {
  it("resolves every SceneTheme field to a real colour in both themes", () => {
    for (const { name, dark } of THEMES) {
      const theme = resolveSceneTheme(readerFor(dark), dark);
      const fields = Object.keys(theme) as (keyof SceneTheme)[];
      for (const field of fields) {
        const value = theme[field];
        expect(value, `${name}: ${field}`).toMatch(/^#[0-9a-f]{6}$/i);
        // The sentinel means a token was missing. It is deliberately loud on
        // screen and must be equally loud here.
        expect(value.toLowerCase(), `${name}: ${field} fell back to the sentinel`).not.toBe("#ff00ff");
      }
    }
  });

  it("draws text that clears AA on the fill it is drawn on", () => {
    // `kindText` is the reason this card exists: it was #94a3b8 on a white
    // node fill (2.56) and #64748b on #1e293b (3.07), failing in BOTH themes,
    // at 9px for the entity kind and 8px for the port speed marking — nowhere
    // near the large-text exemption.
    const pairs: [keyof SceneTheme, keyof SceneTheme][] = [
      ["nodeText", "nodeFill"],
      ["kindText", "nodeFill"],
      ["badgeText", "badgeBg"],
      ["mgmtBadgeText", "mgmtBadgeBg"],
      ["findingErrorText", "findingErrorFill"],
      ["findingWarningText", "findingWarningFill"],
      ["findingInfoText", "findingInfoFill"],
    ];
    for (const { name, dark } of THEMES) {
      const theme = resolveSceneTheme(readerFor(dark), dark);
      for (const [fg, bg] of pairs) {
        expect(contrast(theme[fg], theme[bg]), `${name}: ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  it("draws structural graphics that clear the 3:1 non-text floor", () => {
    // WCAG 1.4.11, for content required to understand the page. An edge on a
    // network topology map is the clearest example of that in this product,
    // and it measured 2.45 light / 2.36 dark before this card.
    //
    // `nodeBorderOk` is checked against BOTH its neighbours because it sits
    // between them: passing against the node fill while disappearing into the
    // page is not passing.
    for (const { name, dark } of THEMES) {
      const t = resolveSceneTheme(readerFor(dark), dark);
      expect(contrast(t.nodeBorderOk, t.nodeFill), `${name}: nodeBorderOk on nodeFill`).toBeGreaterThanOrEqual(3);
      expect(contrast(t.nodeBorderOk, t.background), `${name}: nodeBorderOk on background`).toBeGreaterThanOrEqual(3);
      expect(contrast(t.edgeDefault, t.background), `${name}: edgeDefault on background`).toBeGreaterThanOrEqual(3);

      // T-4302's status roles, measured against BOTH surfaces for the reason
      // T-4305 had to file against `--color-outline`: these are drawn as a
      // node border (on `nodeFill`) *and* as an edge stroke (on `background`),
      // and a token solved against one surface is blind to the other. That
      // mistake has now been made twice in this phase; measuring both is what
      // stops it being made a third time.
      const status = ["statusDown", "statusDegraded", "statusUnknown"] as const;
      for (const role of status) {
        expect(contrast(t[role], t.nodeFill), `${name}: ${role} on nodeFill`).toBeGreaterThanOrEqual(3);
        expect(contrast(t[role], t.background), `${name}: ${role} on background`).toBeGreaterThanOrEqual(3);
      }

      // The minimap's node dots — its only content, at 2x2 px. They were
      // `#94a3b8` on `#f1f5f9` (2.34) and `#475569` on `#0f172a` (2.36):
      // failing in both themes and by almost the same margin, which is the
      // signature of a light value and a dark value picked by eye rather than
      // solved. Measured here and not by the DOM contrast gates for the
      // reason this whole file exists — a <canvas> has no class names to scan.
      expect(contrast(t.minimapDot, t.minimapBg), `${name}: minimapDot on minimapBg`).toBeGreaterThanOrEqual(3);
    }
  });

  it("keeps the node fill distinguishable from the page behind it", () => {
    // Not a WCAG floor — a node whose fill matches the canvas background has
    // only its border left to say where it is, which is the failure the
    // surface ladder exists to prevent.
    for (const { name, dark } of THEMES) {
      const t = resolveSceneTheme(readerFor(dark), dark);
      expect(contrast(t.nodeFill, t.background), `${name}: nodeFill vs background`).toBeGreaterThan(1.02);
    }
  });

  it("substitutes a loud sentinel rather than a plausible guess when a token is missing", () => {
    // The whole argument of this card is that a wrong-but-plausible colour is
    // worse than an obviously broken one, because the first kind survives
    // review. A hardcoded fallback palette would be the hand-copy that was
    // measured at three-of-twelve accurate; this is the alternative.
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    try {
      const theme = resolveSceneTheme(() => "", false);
      expect(theme.nodeText.toLowerCase()).toBe("#ff00ff");
      expect(theme.edgeDefault.toLowerCase()).toBe("#ff00ff");
      // mgmtBadge* are literals, not lookups, so they survive — see the ROLE
      // table's comment for why that pair is deliberately exceptional.
      expect(theme.mgmtBadgeBg.toLowerCase()).not.toBe("#ff00ff");
    } finally {
      warn.mockRestore();
    }
  });

  it("reads each token exactly once, so resolution cannot creep into the draw loop", () => {
    // T-4107's 50-node/5000-guest envelope. `getComputedStyle` forces a style
    // recalculation, and the literal this replaced was rebuilt three times per
    // frame — harmless for an object literal, a per-frame thrash for property
    // resolution. The component memoises on theme; this asserts the resolver
    // itself does not multiply the cost.
    const reads: string[] = [];
    const counting: TokenReader = (name) => {
      reads.push(name);
      return readerFor(false)(name);
    };
    resolveSceneTheme(counting, false);
    // 20 lookups over 16 distinct tokens. Four fields share a token with
    // another field, and every sharing is the point rather than an accident:
    // nodeBorderOk/edgeDefault are both `--color-outline`; T-4302's
    // statusDown/statusDegraded land on the same `--color-status-critical`/
    // `-degraded` the finding badges already read; and minimapDot is
    // `--color-fg-subtle`, the same role the on-node kind word takes, since
    // both are secondary information rendered small.
    //
    // The status pair is worth reading twice — a node border saying "down"
    // and a badge saying "error" now provably resolve to one value, which two
    // tables of literals could not promise and, when measured, did not
    // deliver.
    //
    // T-4306 added the twentieth: `statusOk` is `--color-status-ok`, the GREEN
    // one, and is deliberately NOT shared with `nodeBorderOk`. A healthy node
    // is drawn neutral because health is the absence of a signal; the path
    // simulator's `allow` verdict is an answer to a question the operator
    // asked, so it says yes in green. Two roles, two tokens, on purpose.
    expect(reads).toHaveLength(20);
    expect(new Set(reads).size).toBe(16);
  });
});
