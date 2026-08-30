// SPDX-License-Identifier: Apache-2.0

// T-4301 criterion 3: the two places the OPERATING SYSTEM renders the brand.
//
// `index.html`'s `<meta name="theme-color">` and `manifest.webmanifest`'s
// `theme_color`/`background_color` are what a phone's browser chrome and an
// installed PWA's splash screen paint with. Neither is CSS and neither can
// read a custom property, so both must carry literal values — which makes them
// the one part of the design language that *cannot* be re-pointed by editing
// `index.css`, and therefore the part most likely to drift.
//
// It had. Both carried `#2563eb`, the pre-T-4201 Tailwind blue, so on a phone
// or an installed PWA the chrome was still a colour the product stopped using
// everywhere else. `background_color` had drifted too, and more quietly:
// `#0f172a` is Tailwind slate-900, one hex digit from the `#0f172b` the dark
// surface ladder actually settled on at T-4203 — close enough that no one
// would ever catch it by eye, which is the whole argument for this file.
//
// So the values stay literal and this test makes them derived in the only way
// available: it reads `index.css` and asserts they match. Editing a token
// without editing these two now fails here rather than shipping a splash
// screen in last season's brand.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const ROOT = resolve(__dirname, "..");
const CSS = readFileSync(resolve(__dirname, "index.css"), "utf8");
const HTML = readFileSync(resolve(ROOT, "index.html"), "utf8");
const MANIFEST = JSON.parse(readFileSync(resolve(ROOT, "public", "manifest.webmanifest"), "utf8")) as {
  theme_color: string;
  background_color: string;
};

/** Reads a token out of a specific stylesheet block, so `html.dark`'s
 * re-pointed value is distinguishable from the base one. */
function token(selector: string, name: string): string {
  const opener = new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m");
  const found = opener.exec(CSS);
  if (found === null) throw new Error(`${selector} not found in index.css`);
  const open = CSS.indexOf("{", found.index);
  const block = CSS.slice(open + 1, CSS.indexOf("}", open));
  const match = new RegExp(`${name}\\s*:\\s*(#[0-9a-f]{6})\\s*;`).exec(block);
  if (match?.[1] === undefined) throw new Error(`${name} not found under ${selector}`);
  return match[1];
}

describe("OS-rendered brand chrome tracks the design tokens (T-4301)", () => {
  const accent = token("@theme", "--color-accent-600");
  const darkPage = token("html.dark", "--color-surface-page");

  it("index.html's theme-color is the brand accent", () => {
    const meta = /<meta name="theme-color" content="(#[0-9a-f]{6})"/i.exec(HTML);
    expect(meta?.[1], "no <meta name=\"theme-color\"> found in index.html").toBeDefined();
    expect(meta?.[1]?.toLowerCase()).toBe(accent);
  });

  it("the manifest's theme_color is the brand accent", () => {
    expect(MANIFEST.theme_color.toLowerCase()).toBe(accent);
  });

  // The splash background. Dark deliberately — an installed app opening to a
  // white flash before its own dark chrome paints is the failure this avoids —
  // so it tracks the DARK page surface, not the base one.
  it("the manifest's background_color is the dark page surface", () => {
    expect(MANIFEST.background_color.toLowerCase()).toBe(darkPage);
  });

  // Named explicitly, the way overlayHues.test.ts names it: the retired brand
  // blue reappearing here means something different from a generic wrong
  // value. It means the PWA has drifted back to a colour the product
  // abandoned, in the one surface no stylesheet edit can reach.
  it("neither file carries the pre-T-4201 brand blue", () => {
    expect(HTML).not.toMatch(/#2563eb/i);
    expect(JSON.stringify(MANIFEST)).not.toMatch(/#2563eb/i);
  });
});
