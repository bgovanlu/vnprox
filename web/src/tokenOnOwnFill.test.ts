// SPDX-License-Identifier: Apache-2.0

// The blind spot this phase keeps rediscovering, finally given a gate.
//
// `index.css.test.ts` solves every foreground token against the four SURFACE
// levels, and that is the right denominator for text on a page. It is the
// wrong one for a chip, because a chip brings its own background — and the
// token knows nothing about it.
//
// Four times now:
//
//   T-4215  the changeset badge chips measured 4.08 on their own
//           `bg-slate-200/70` fill, and the card wrote down that "a guard that
//           measures against surfaces still cannot see a call site that brings
//           its own" as its honest boundary.
//   T-4212  a chip on /tools composited `bg-black/5` over a `-soft` wash,
//           inventing a sixth surface, at 4.11.
//   T-4306  the canvas badge, where `--color-border` — a hairline token held
//           under 2:1 against every surface on purpose — was being used as a
//           FILL, so a surface-solved foreground on it measured 4.04.
//   T-4215  again, and this time the sweep caused it: converting
//           `dark:text-slate-300` to `--color-fg-muted` is safe on every
//           surface and lands at 4.04 on a self-supplied `bg-slate-700`.
//
// Each was found by a different accident — a manual read, an axe run against
// whichever fixture happened to render the element, a failing sibling test.
// The last one is the reason this file exists: axe reports only what the page
// rendered, so a chip on a route with no fixture data is invisible to it. This
// scans the source instead, so coverage does not depend on what a test
// happened to mount.
//
// Scope is deliberately the case that is decidable statically: a design-token
// foreground in the same class list as a literal slate fill. Composited fills
// (`bg-black/5` over a wash) are not resolvable from source and remain axe's
// job.
import { readFileSync, readdirSync } from "node:fs";
import { relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = resolve(__dirname);
const CSS = readFileSync(resolve(SRC, "index.css"), "utf8");

const SLATE: Record<string, string> = {
  "50": "#f8fafc", "100": "#f1f5f9", "200": "#e2e8f0", "300": "#cbd5e1", "400": "#94a3b8",
  "500": "#64748b", "600": "#475569", "700": "#334155", "800": "#1e293b", "900": "#0f172a",
  "950": "#020617",
};

function blockTokens(selector: string): Map<string, string> {
  const opener = new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m");
  const found = opener.exec(CSS);
  if (found === null) throw new Error(`${selector} not found in index.css`);
  const open = CSS.indexOf("{", found.index);
  const out = new Map<string, string>();
  for (const m of CSS.slice(open + 1, CSS.indexOf("}", open)).matchAll(/(--color-[a-z0-9-]+)\s*:\s*(#[0-9a-f]{6})\s*;/g)) {
    const [, name, value] = m;
    if (name !== undefined && value !== undefined) out.set(name, value);
  }
  return out;
}

const LIGHT = blockTokens("@theme");
const DARK = blockTokens("html.dark");
const FG_UTILITIES = ["text-fg", "text-fg-body", "text-fg-muted", "text-fg-subtle"] as const;

function relLum(hex: string): number {
  const parts = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const lin = parts.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * (lin[0] ?? 0) + 0.7152 * (lin[1] ?? 0) + 0.0722 * (lin[2] ?? 0);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [relLum(a), relLum(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, e.name);
    if (e.isDirectory()) sourceFiles(full, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(full);
  }
  return out;
}

interface Offence {
  readonly where: string;
  readonly theme: string;
  readonly detail: string;
}

function findOffences(): Offence[] {
  const found: Offence[] = [];
  for (const file of sourceFiles(SRC)) {
    for (const m of readFileSync(file, "utf8").matchAll(/(["'`])([^"'`\n]*?)\1/g)) {
      const tokens = (m[2] ?? "").split(" ");
      const utility = FG_UTILITIES.find((u) => tokens.includes(u));
      if (utility === undefined) continue;
      const lightFill = tokens.find((t) => /^bg-slate-\d{2,3}$/.test(t))?.slice("bg-slate-".length);
      const darkFill = tokens.find((t) => /^dark:bg-slate-\d{2,3}$/.test(t))?.slice("dark:bg-slate-".length);
      if (lightFill === undefined && darkFill === undefined) continue;

      // A class list with only a light fill still renders that fill in dark
      // mode, so the dark foreground is measured against it too.
      for (const [theme, tokensFor, step] of [
        ["light", LIGHT, lightFill],
        ["dark", DARK, darkFill ?? lightFill],
      ] as const) {
        if (step === undefined) continue;
        const fg = tokensFor.get(`--color-${utility.slice("text-".length)}`) ?? LIGHT.get(`--color-${utility.slice("text-".length)}`);
        const bg = SLATE[step];
        if (fg === undefined || bg === undefined) continue;
        const ratio = contrast(fg, bg);
        if (ratio < 4.5) {
          found.push({
            where: relative(SRC, file),
            theme,
            detail: `${utility} on bg-slate-${step} = ${ratio.toFixed(2)}`,
          });
        }
      }
    }
  }
  return found;
}

describe("design-token text on a self-supplied fill", () => {
  it("clears AA in both themes", () => {
    expect(
      findOffences().map((o) => `${o.where} (${o.theme}): ${o.detail}`),
      "a foreground token solved against the SURFACE ladder is being drawn on a fill the call " +
        "site brought itself, where the ladder says nothing. Either give the element a stronger " +
        "foreground (text-fg-body is the usual answer for a chip) or make its fill a surface token.",
    ).toEqual([]);
  });

  // Guard the guard: the detector must still detect. Without this, a broken
  // regex or a renamed token turns the assertion above into a green light for
  // everything — which is the exact failure mode the four incidents in this
  // file's header share.
  it("detects the shape it claims to detect", () => {
    const fgMuted = DARK.get("--color-fg-muted");
    const slate700 = SLATE["700"];
    expect(fgMuted, "--color-fg-muted missing from html.dark").toBeDefined();
    expect(fgMuted === undefined || slate700 === undefined ? 0 : contrast(fgMuted, slate700)).toBeLessThan(4.5);
  });
});
