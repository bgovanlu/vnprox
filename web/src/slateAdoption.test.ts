// SPDX-License-Identifier: Apache-2.0

// T-4215 criterion 5: a gate that fails when a converted pairing creeps back.
//
// `slateContrast.test.ts` is the sibling of this file and measures something
// different: whether slate text is *legible*. It has been at zero offences
// since the card landed, and it would stay at zero while every call site
// drifted back off the tokens — because a legible slate pair is still a slate
// pair. Adoption and contrast are two properties, and only one of them had a
// gate.
//
// What this asserts is narrow on purpose. Not "no slate anywhere": 82 pairs
// remain that no token represents exactly (`text-slate-800 dark:text-slate-100`
// is the biggest group at 42 — slate-800 sits between `--color-fg` and
// `--color-fg-body` and matches neither), and converting those means picking a
// visible change, which is a design decision and not something a test should
// force. The assertion is: **if a token already spells this exact pairing,
// use the token.** Those conversions are byte-identical, so there is never a
// reason to write the long form, and 1202 call sites were converted on exactly
// that basis.
//
// The token -> slate mapping is DERIVED from index.css rather than written
// here. That matters more than usual for this card: T-4215 re-pointed
// `--color-border` and `--color-border-strong` a step lighter, and a
// hand-written table would have gone on policing the old pairing — which is
// the failure mode the card is named after.
import { readFileSync, readdirSync } from "node:fs";
import { relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = resolve(__dirname);
const CSS = readFileSync(resolve(SRC, "index.css"), "utf8");

/** Tailwind's slate ramp, the only palette these call sites draw from. */
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

function slateStepFor(hex: string | undefined): string | undefined {
  if (hex === undefined) return undefined;
  return Object.entries(SLATE).find(([, v]) => v === hex.toLowerCase())?.[0];
}

/** Every (utility, lightStep, darkStep) a token spells exactly. Built by
 * asking each token what slate steps its two values ARE — so a token whose
 * value is not a slate step (`--color-fg-subtle` light is #5f6e85, solved for
 * contrast rather than picked off the ramp) simply produces no entry, and its
 * call sites are never demanded. */
function convertiblePairs(): { utility: string; light: string; dark: string }[] {
  const out: { utility: string; light: string; dark: string }[] = [];
  for (const [token, lightHex] of LIGHT) {
    const prop = token.startsWith("--color-fg") ? "text" : token.startsWith("--color-border") ? "border" : undefined;
    if (prop === undefined) continue;
    const light = slateStepFor(lightHex);
    const dark = slateStepFor(DARK.get(token));
    if (light === undefined || dark === undefined) continue;
    out.push({ utility: `${prop}-${token.slice("--color-".length)}`, light: `${prop}-slate-${light}`, dark: `dark:${prop}-slate-${dark}` });
  }
  return out;
}

function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, e.name);
    if (e.isDirectory()) sourceFiles(full, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(full);
  }
  return out;
}

const LITERAL = /(["'`])([^"'`\n]*?)\1/g;

describe("slate adoption (T-4215 criterion 5)", () => {
  const convertible = convertiblePairs();

  // Guard the guard. If the derivation silently produced nothing — a renamed
  // token, a changed value, a broken regex — every assertion below would pass
  // vacuously, which is precisely the failure this card exists to punish.
  it("derives a non-empty set of convertible pairings from index.css", () => {
    expect(convertible.length).toBeGreaterThanOrEqual(4);
    expect(convertible.map((c) => c.utility)).toContain("text-fg-muted");
    expect(convertible.map((c) => c.utility)).toContain("border-border");
  });

  it("has no class list spelling out a pairing a token already covers", () => {
    const offences: string[] = [];
    for (const file of sourceFiles(SRC)) {
      const source = readFileSync(file, "utf8");
      for (const m of source.matchAll(LITERAL)) {
        const tokens = (m[2] ?? "").split(" ");
        for (const c of convertible) {
          if (tokens.includes(c.light) && tokens.includes(c.dark)) {
            offences.push(`${relative(SRC, file)}: "${c.light} ${c.dark}" -> ${c.utility}`);
          }
        }
      }
    }
    expect(
      offences,
      "these pairings are byte-identical to a token that already exists, so writing them out " +
        "means a future change to the token will not reach them. Replace the two classes with " +
        "the one utility named on the right.",
    ).toEqual([]);
  });
});
