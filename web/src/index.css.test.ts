// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/** T-3401: the accent alias layer is a CSS-only mechanism — Tailwind
 * resolves `accent-*` utilities from the custom properties in `@theme`, and
 * `html.demo` re-points those same properties to re-tint the entire app for
 * demo mode (T-2801) without touching a component.
 *
 * That mechanism cannot be asserted through the DOM in this suite: jsdom
 * does not run Tailwind, so `getComputedStyle` on a rendered component
 * returns the raw class list, not a resolved colour. The property
 * declarations themselves are the artifact under test, so this reads them
 * out of the stylesheet source. A test that rendered a component and
 * checked for the string "accent" in its className would pass whether or
 * not the demo override existed — which is the exact failure this guards. */
// `__dirname` rather than `import.meta.url`: under the jsdom environment
// this suite runs in, `import.meta.url` is an http: URL, not a file: one
// (see i18nCoverage.test.ts, which reads source files the same way).
const css = readFileSync(resolve(__dirname, "index.css"), "utf8");

/** Every `--color-accent-<step>` declared inside the given block. */
function accentStepsIn(block: string): Set<string> {
  const steps = new Set<string>();
  for (const match of block.matchAll(/--color-accent-(\d+)\s*:/g)) {
    const step = match[1];
    if (step !== undefined) steps.add(step);
  }
  return steps;
}

/** The body of the first `<selector> { ... }` rule in the stylesheet.
 * Adequate for this file's flat, non-nested top-level blocks; it would need
 * real brace balancing if these blocks ever gained nested rules. */
function blockBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `${selector} block not found in index.css`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf("{", start);
  const close = css.indexOf("}", open);
  expect(close, `${selector} block is not closed`).toBeGreaterThan(open);
  return css.slice(open + 1, close);
}

describe("index.css accent tokens", () => {
  it("defines the base accent as a full 11-step scale", () => {
    const steps = accentStepsIn(blockBody("@theme"));
    expect([...steps].sort((a, b) => Number(a) - Number(b))).toEqual([
      "50",
      "100",
      "200",
      "300",
      "400",
      "500",
      "600",
      "700",
      "800",
      "900",
      "950",
    ]);
  });

  it("re-points the demo override at every step the base scale defines", () => {
    const base = accentStepsIn(blockBody("@theme"));
    const demo = accentStepsIn(blockBody("html.demo"));

    // Set equality in both directions: a step in @theme but not in
    // html.demo leaks the base accent into demo mode at that one shade; a
    // step in html.demo but not in @theme is a dangling override that no
    // utility can ever resolve.
    expect([...demo].sort()).toEqual([...base].sort());
  });

  it("keeps the demo override on a visually distinct hue from the base accent", () => {
    // Not a colour-science assertion — just that the two blocks are not
    // aliasing the same Tailwind scale, which would make demo mode
    // indistinguishable from a real cluster (the whole point of T-2801).
    const scalesIn = (block: string): Set<string> => {
      const scales = new Set<string>();
      for (const match of block.matchAll(/var\(--color-([a-z]+)-\d+\)/g)) {
        const scale = match[1];
        if (scale !== undefined) scales.add(scale);
      }
      return scales;
    };
    const baseScales = scalesIn(blockBody("@theme"));
    const demoScales = scalesIn(blockBody("html.demo"));

    expect(baseScales.size).toBe(1);
    expect(demoScales.size).toBe(1);
    expect([...demoScales][0]).not.toBe([...baseScales][0]);
  });
});
