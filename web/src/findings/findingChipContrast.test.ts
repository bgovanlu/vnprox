// SPDX-License-Identifier: Apache-2.0

// Found by T-4212's repointed axe sweep, on a route the old sweep already
// covered — so it was not the drift that hid this, it was that nothing had run
// the sweep. `make ci` (the pre-push gate) deliberately OMITS the e2e job; the
// Makefile says so in as many words. The full matrix is scripts/ci-local.sh.
//
// What axe reported, on /tools in all three modes:
//
//   Element has insufficient color contrast of 4.11 (foreground #9b6200,
//   background #ebe7df, font size 7.5pt (10px), font weight normal).
//
// `#ebe7df` is not a token. It is `--color-status-degraded-soft` (#f7f3eb)
// with `bg-black/5` composited over it — an unnamed sixth surface, created at
// the call site, underneath text coloured by a token that was solved against
// the fifth.
//
// **This is the phase's recurring defect, for the fourth time.** T-4215 found
// it (a guard that measures against a surface is blind to a call site that
// brings its own), T-4305 found it again for `--color-outline` against the
// kind washes, T-4301's remainder found it in the export. Here the call site
// does not merely pick the wrong surface — it *manufactures* one, by
// compositing an opacity over the one the design language named.
//
// Measured across every status x every tint, which is the part axe could not
// do: it can only report what the fixture happened to render, and the fixture
// rendered one of five failing combinations.
//
//   light          soft   +black/5   +black/10
//   ok             5.02     4.51       4.00 FAIL
//   degraded       4.58     4.11 FAIL  3.67 FAIL
//   critical       5.70     5.11       4.55
//   info           5.15     4.62       4.13 FAIL
//   dark           soft   +white/5   +white/10
//   ok             7.33     6.31       5.40
//   degraded       7.05     6.02       5.15
//   critical       4.86     4.20 FAIL  3.58 FAIL
//   info           7.19     6.21       5.33
//
// **Every status text clears AA on its own `-soft` wash. Not one combination
// fails until a tint is added.** So the repair is not a new colour: it is to
// stop manufacturing surfaces. The chips now carry `ring-1 ring-current/30`,
// which marks them without touching what the text sits on, and every
// combination reverts to the passing column.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const CSS = readFileSync(resolve(__dirname, "..", "index.css"), "utf8");
const SOURCE = readFileSync(resolve(__dirname, "FindingsList.tsx"), "utf8");

const AA = 4.5;
const STATUSES = ["ok", "degraded", "critical", "info"] as const;

/** The declarations inside one top-level block, by custom-property name —
 * the same stylesheet-as-text approach index.css.test.ts and
 * canvasPalette.test.ts both use, for the same reason: jsdom resolves no
 * custom property, so a test that asked the DOM would measure nothing. */
function blockTokens(selector: string): Map<string, string> {
  const opener = new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m");
  const found = opener.exec(CSS);
  if (found === null) throw new Error(`${selector} not found in index.css`);
  const open = CSS.indexOf("{", found.index);
  const close = CSS.indexOf("}", open);
  const out = new Map<string, string>();
  for (const m of CSS.slice(open + 1, close).matchAll(/(--[a-z0-9-]+)\s*:\s*(#[0-9a-f]{6})\s*;/g)) {
    const [, name, value] = m;
    if (name !== undefined && value !== undefined) out.set(name, value);
  }
  return out;
}

function tokensFor(dark: boolean): (name: string) => string {
  const merged = new Map(blockTokens("@theme"));
  if (dark) for (const [k, v] of blockTokens("html.dark")) merged.set(k, v);
  return (name) => {
    const hit = merged.get(name);
    if (hit === undefined) throw new Error(`${name} missing from index.css`);
    return hit;
  };
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

/** Composites `tint` over `base` at `alpha` — what a `bg-black/5` utility
 * actually produces, and what axe measured. */
function composite(tint: string, base: string, alpha: number): string {
  const chan = (hex: string, i: number): number => parseInt(hex.slice(i, i + 2), 16);
  const mix = [1, 3, 5].map((i) => Math.round(chan(tint, i) * alpha + chan(base, i) * (1 - alpha)));
  return `#${mix.map((c) => c.toString(16).padStart(2, "0")).join("")}`;
}

const THEMES = [
  { name: "light", dark: false, tint: "#000000" },
  { name: "dark", dark: true, tint: "#ffffff" },
] as const;

describe("finding chips (found by T-4212's axe sweep)", () => {
  it("clears AA on every status wash, which is the surface the design language named", () => {
    for (const { name, dark } of THEMES) {
      const token = tokensFor(dark);
      for (const status of STATUSES) {
        const fg = token(`--color-status-${status}`);
        const bg = token(`--color-status-${status}-soft`);
        expect(contrast(fg, bg), `${name}: status-${status} on its soft wash`).toBeGreaterThanOrEqual(AA);
      }
    }
  });

  it("would fail again if a chip composited a tint over that wash", () => {
    // Guards the guard, and records the measurement rather than the verdict.
    // Five of sixteen combinations fail; axe reported exactly one, because a
    // screenshot-time tool can only see what the fixture rendered. If this
    // ever finds ZERO failures the assertion above has stopped meaning
    // anything, because the tint is precisely what was wrong.
    const failures: string[] = [];
    for (const { name, dark, tint } of THEMES) {
      const token = tokensFor(dark);
      for (const status of STATUSES) {
        const fg = token(`--color-status-${status}`);
        const bg = token(`--color-status-${status}-soft`);
        for (const alpha of [0.05, 0.1]) {
          if (contrast(fg, composite(tint, bg, alpha)) < AA) {
            failures.push(`${name}/${status}/${String(alpha)}`);
          }
        }
      }
    }
    expect(failures.sort()).toEqual([
      "dark/critical/0.05",
      "dark/critical/0.1",
      "light/degraded/0.05",
      "light/degraded/0.1",
      "light/info/0.1",
      "light/ok/0.1",
    ]);
  });

  it("does not composite an opacity over a surface anywhere in FindingsList", () => {
    // The durable half. The two chips that failed are fixed, but the defect is
    // the technique, not the two call sites: any `bg-black/N` or `bg-white/N`
    // inside a status-washed row manufactures a surface no gate has measured.
    // Comments are stripped first so this file's own quotation of the bad
    // pattern, and FindingsList's, cannot satisfy or trip it — the same
    // mistake NoticeStack's interpolation guard made and had to fix.
    const code = SOURCE.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/.*$/gm, "$1");
    const tinted = [...code.matchAll(/bg-(?:black|white)\/\d+/g)].map((m) => m[0]);
    expect(tinted).toEqual([]);
  });
});
