// SPDX-License-Identifier: Apache-2.0

/** T-3401 / T-4201 / T-4203 / T-4204 / T-4206: the design-language tokens
 * are a CSS-only mechanism — Tailwind resolves `accent-*`, `status-*` and
 * `surface-*` utilities from the custom properties in `@theme`, and
 * `html.dark` / `html.demo` re-point those same properties to retheme the
 * entire app without touching a component.
 *
 * That mechanism cannot be asserted through the DOM in this suite: jsdom
 * does not run Tailwind, so `getComputedStyle` on a rendered component
 * returns the raw class list, not a resolved colour. The property
 * declarations themselves are the artifact under test, so this reads them
 * out of the stylesheet source. A test that rendered a component and
 * checked for the string "accent" in its className would pass whether or
 * not the demo override existed — which is the exact failure this guards.
 *
 * T-4201 CHANGED WHAT THIS FILE CAN CHECK, FOR THE BETTER. While the
 * accent was `var(--color-indigo-*)`, no test here could know what colour
 * the app actually rendered — only which Tailwind scale name it pointed
 * at. The tokens are now literal hex, so every contrast claim written in
 * index.css's comments is recomputable, and this suite recomputes them.
 * The measurements in that file are therefore not documentation that can
 * quietly go stale; they are assertions. This is the guard that was
 * missing when the accent's contrast was re-derived by hand at T-905,
 * T-3401, T-3405 and T-3406 — four times, each time correctly, each time
 * leaving nothing behind that would catch the fifth. */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// `__dirname` rather than `import.meta.url`: under the jsdom environment
// this suite runs in, `import.meta.url` is an http: URL, not a file: one
// (see i18nCoverage.test.ts, which reads source files the same way).
const css = readFileSync(resolve(__dirname, "index.css"), "utf8");

/** The body of the `<selector> { ... }` rule in the stylesheet.
 * Adequate for this file's flat token blocks; it would need real brace
 * balancing if `@theme`, `:root`, `html.dark` or `html.demo` ever gained
 * a nested rule. Deliberately NOT used on `@media` blocks, which do.
 *
 * The selector must be matched as a RULE, at the start of a line and
 * followed by its brace — not merely found as a substring. The original
 * form of this helper used a bare `indexOf`, which silently returned the
 * WRONG BLOCK the moment a selector name was mentioned in a comment
 * earlier in the file: `indexOf("html.dark")` landed inside the `@theme`
 * comment that explains the dark override, and the following `{` it
 * seized on belonged to the next rule entirely. The tests then asserted
 * against a block that happened to parse, and reported the tokens as
 * missing rather than as misread. Anchoring costs nothing and removes a
 * whole class of confusing failure. */
function blockBody(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const rule = new RegExp(`^${escaped}\\s*\\{`, "m");
  const found = rule.exec(css);
  expect(found, `${selector} rule not found in index.css`).not.toBeNull();
  const open = css.indexOf("{", (found as RegExpExecArray).index);
  const close = css.indexOf("}", open);
  expect(close, `${selector} block is not closed`).toBeGreaterThan(open);
  return css.slice(open + 1, close);
}

/** Every `--<prefix>-<name>: <literal>` declared in a block, as a map.
 * Only literal values are captured — a `var(...)` indirection returns
 * nothing, which is intentional: a token this suite cannot resolve is a
 * token whose contrast it cannot check, and that should fail loudly
 * rather than silently pass. */
function tokensIn(block: string, prefix: string): Map<string, string> {
  const out = new Map<string, string>();
  const re = new RegExp(`--${prefix}-([a-z0-9-]+)\\s*:\\s*([^;]+);`, "g");
  for (const m of block.matchAll(re)) {
    const [, name, value] = m;
    if (name !== undefined && value !== undefined) out.set(name, value.trim());
  }
  return out;
}

const HEX = /^#[0-9a-f]{6}$/;

/** WCAG 2.x relative luminance from an `#rrggbb` string. */
function luminance(hex: string): number {
  expect(hex, `${hex} is not a 6-digit hex colour`).toMatch(HEX);
  const n = Number.parseInt(hex.slice(1), 16);
  const channels = [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff].map((v) => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * (channels[0] ?? 0) + 0.7152 * (channels[1] ?? 0) + 0.0722 * (channels[2] ?? 0);
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}

/** Composite `fg` over `bg` at `alpha`, for the `bg-accent-600/10`-style
 * washes this app uses for selected rows and tabs. */
function over(fg: string, bg: string, alpha: number): string {
  const parts = (h: string) => [1, 3, 5].map((i) => Number.parseInt(h.slice(i, i + 2), 16));
  const [f, b] = [parts(fg), parts(bg)];
  return (
    "#" +
    f
      .map((v, i) => Math.round(v * alpha + (b[i] ?? 0) * (1 - alpha)).toString(16).padStart(2, "0"))
      .join("")
  );
}

/** Hue in degrees, measured in OKLCH — the space this palette is designed
 * in, and deliberately NOT sRGB-HSL.
 *
 * That distinction is load-bearing rather than pedantic. An earlier
 * version of this guard measured HSL hue, which compresses the warm
 * region badly: it scored an amber/red pair that reads unmistakably on
 * screen as only 36deg apart, and it scored an olive-green "degraded"
 * that reads as moss as comfortably separated. Rendering the candidates
 * and looking at them settled it the other way both times. A perceptual
 * claim needs a perceptual space, or the gate ends up being satisfied by
 * a palette nobody can read. */
function hue(hex: string): number {
  const n = Number.parseInt(hex.slice(1), 16);
  const linear = (v: number): number => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  const r = linear((n >> 16) & 0xff);
  const g = linear((n >> 8) & 0xff);
  const b = linear(n & 0xff);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const a = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const bb = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  return (((Math.atan2(bb, a) * 180) / Math.PI) % 360 + 360) % 360;
}

const AA = 4.5;
/** The two surfaces this app actually renders on: `bg-white`, and
 * `dark:bg-slate-900` (147 call sites against 16 for slate-950 — the
 * count is slateContrast.test.ts's, and this suite reuses its finding
 * rather than picking its own dark surface). */
const WHITE = "#ffffff";
const SLATE_900 = "#0f172b";

const ACCENT_STEPS = ["50", "100", "200", "300", "400", "500", "600", "700", "800", "900", "950"];
const STATUS_STATES = ["ok", "degraded", "critical", "info", "unknown"];
const SURFACE_LEVELS = ["sunken", "page", "raised", "overlay"];

const theme = blockBody("@theme");
const dark = blockBody("html.dark");
const demo = blockBody("html.demo");
const root = blockBody(":root");

describe("accent tokens (T-3401, T-4201)", () => {
  const base = tokensIn(theme, "color-accent");
  const demoAccent = tokensIn(demo, "color-accent");

  it("defines the base accent as a full 11-step scale of literal hex", () => {
    expect([...base.keys()].sort((a, b) => Number(a) - Number(b))).toEqual(ACCENT_STEPS);
    for (const [step, value] of base) expect(value, `accent-${step}`).toMatch(HEX);
  });

  it("re-points the demo override at every step the base scale defines", () => {
    // Set equality in both directions: a step in @theme but not in
    // html.demo leaks the base accent into demo mode at exactly that one
    // shade; a step in html.demo but not in @theme is a dangling override
    // that no utility can ever resolve.
    expect([...demoAccent.keys()].sort()).toEqual([...base.keys()].sort());
    for (const [step, value] of demoAccent) expect(value, `demo accent-${step}`).toMatch(HEX);
  });

  it("keeps the demo override on a visually distinct hue from the base accent", () => {
    // The whole point of T-2801 is that demo mode cannot be mistaken for a
    // real cluster at a glance. Now that both blocks are literal, this can
    // assert the actual colours rather than merely that two Tailwind scale
    // names differ (which said nothing about how they looked).
    const b = base.get("600");
    const d = demoAccent.get("600");
    expect(b).toBeDefined();
    expect(d).toBeDefined();
    const separation = Math.abs(hue(b as string) - hue(d as string));
    expect(Math.min(separation, 360 - separation)).toBeGreaterThan(90);
  });

  it("clears every contrast floor index.css claims for the accent", () => {
    // These are the pairings that decide whether a component call site has
    // to move. index.css's T-4201 comment records each number; this
    // recomputes it, so the comment cannot drift from the tokens.
    const a = (step: string): string => {
      const v = base.get(step);
      expect(v, `accent-${step} missing`).toBeDefined();
      return v as string;
    };
    // Button's primary variant: bg-accent-600 text-white, ~10 solid controls.
    expect(contrast(WHITE, a("600"))).toBeGreaterThanOrEqual(AA);
    // The selected-row/tab pattern: text-accent-700 on bg-accent-600/10.
    expect(contrast(a("700"), WHITE)).toBeGreaterThanOrEqual(AA);
    expect(contrast(a("700"), over(a("600"), WHITE, 0.1))).toBeGreaterThanOrEqual(AA);
    // Its dark-mode counterpart.
    expect(contrast(a("300"), SLATE_900)).toBeGreaterThanOrEqual(AA);
    expect(contrast(a("400"), SLATE_900)).toBeGreaterThanOrEqual(AA);
    expect(contrast(a("300"), over(a("600"), SLATE_900, 0.1))).toBeGreaterThanOrEqual(AA);
  });

  it("clears the same floors in demo mode", () => {
    // T-3406 found that the base accent passing says nothing about demo
    // mode — amber failed at its own step numbers where indigo passed,
    // which is why the 600/700 steps are remapped. Asserted, not assumed.
    const d = (step: string): string => demoAccent.get(step) as string;
    expect(contrast(WHITE, d("600"))).toBeGreaterThanOrEqual(AA);
    expect(contrast(d("700"), WHITE)).toBeGreaterThanOrEqual(AA);
    expect(contrast(d("700"), over(d("600"), WHITE, 0.1))).toBeGreaterThanOrEqual(AA);
  });
});

describe("semantic status scale (T-4204)", () => {
  const light = tokensIn(theme, "color-status");
  const night = tokensIn(dark, "color-status");

  it("defines fg, solid and soft for every health state, in both themes", () => {
    for (const state of STATUS_STATES) {
      for (const role of [state, `${state}-solid`, `${state}-soft`]) {
        expect(light.get(role), `light --color-status-${role}`).toMatch(HEX);
        expect(night.get(role), `dark --color-status-${role}`).toMatch(HEX);
      }
    }
  });

  it("redefines in html.dark exactly the tokens @theme declares", () => {
    // Same failure mode as the demo accent: a status token defined for
    // light mode but missing from html.dark renders the light-mode colour
    // on a dark surface, and only for that one state — invisible in review
    // because the other five look right.
    expect([...night.keys()].sort()).toEqual([...light.keys()].sort());
  });

  it("gives `stale` no solid or soft variant", () => {
    // Deliberate, and load-bearing: stale is a freshness qualifier layered
    // on top of a health state, not a state of its own. A filled stale
    // badge would compete with the real status badge beside it, and giving
    // it a wash produced the only sub-AA pairing in the set. See index.css.
    expect(light.get("stale")).toMatch(HEX);
    expect(light.has("stale-solid")).toBe(false);
    expect(light.has("stale-soft")).toBe(false);
    expect(night.has("stale-solid")).toBe(false);
    expect(night.has("stale-soft")).toBe(false);
  });

  it("clears AA for every state, in both themes, on all three roles", () => {
    for (const state of STATUS_STATES) {
      const lf = light.get(state) as string;
      const ls = light.get(`${state}-solid`) as string;
      const lw = light.get(`${state}-soft`) as string;
      expect(contrast(lf, WHITE), `light ${state} fg on white`).toBeGreaterThanOrEqual(AA);
      expect(contrast(lf, lw), `light ${state} fg on its soft wash`).toBeGreaterThanOrEqual(AA);
      expect(contrast(WHITE, ls), `light ${state} white on solid`).toBeGreaterThanOrEqual(AA);

      const df = night.get(state) as string;
      const dw = night.get(`${state}-soft`) as string;
      const ds = night.get(`${state}-solid`) as string;
      expect(contrast(df, SLATE_900), `dark ${state} fg on slate-900`).toBeGreaterThanOrEqual(AA);
      expect(contrast(df, dw), `dark ${state} fg on its soft wash`).toBeGreaterThanOrEqual(AA);
      expect(contrast(SLATE_900, ds), `dark ${state} surface on solid`).toBeGreaterThanOrEqual(AA);
    }
    expect(contrast(light.get("stale") as string, WHITE)).toBeGreaterThanOrEqual(AA);
    expect(contrast(night.get("stale") as string, SLATE_900)).toBeGreaterThanOrEqual(AA);
  });

  it("keeps the health states mutually distinguishable by hue, in both themes", () => {
    // Principle 2 of the visual roadmap — colour encodes status — only
    // holds if the encodings are separable. ok/degraded/critical must not
    // converge, and none may drift into the brand accent, or a selected
    // row would read as a health signal.
    //
    // BOTH themes, because the collision that nearly shipped was
    // dark-only: amber and red as light warm tones on a dark ground came
    // out 32.7deg apart while their light-mode counterparts were fine.
    // Checking one theme would have missed it entirely.
    const accent = tokensIn(theme, "color-accent");
    for (const [themeName, tokens, accentStep] of [
      ["light", light, "600"],
      ["dark", night, "300"],
    ] as const) {
      const hues = new Map<string, number>();
      for (const state of ["ok", "degraded", "critical"]) hues.set(state, hue(tokens.get(state) as string));
      hues.set("accent", hue(accent.get(accentStep) as string));
      const names = [...hues.keys()];
      for (let i = 0; i < names.length; i++) {
        for (let j = i + 1; j < names.length; j++) {
          const [x, y] = [names[i] as string, names[j] as string];
          const raw = Math.abs((hues.get(x) as number) - (hues.get(y) as number));
          expect(Math.min(raw, 360 - raw), `${themeName}: ${x} vs ${y} hue separation`).toBeGreaterThan(40);
        }
      }
    }
  });

  it("keeps `info` ON the brand hue rather than separated from it", () => {
    // The one deliberate exception to the rule above, asserted rather
    // than merely left out of it: an informational status IS the accent,
    // and if a later change pushed `info` off the brand hue the product
    // would carry two different "this is a neutral notice" colours with
    // nothing to say which is correct.
    const accent600 = tokensIn(theme, "color-accent").get("600") as string;
    const raw = Math.abs(hue(light.get("info") as string) - hue(accent600));
    expect(Math.min(raw, 360 - raw), "info vs accent hue").toBeLessThan(10);
  });
});

describe("surface ladder (T-4203)", () => {
  const light = tokensIn(theme, "color-surface");
  const night = tokensIn(dark, "color-surface");

  it("defines all four levels in both themes", () => {
    for (const level of SURFACE_LEVELS) {
      expect(light.get(level), `light --color-surface-${level}`).toMatch(HEX);
      expect(night.get(level), `dark --color-surface-${level}`).toMatch(HEX);
    }
    expect([...night.keys()].sort()).toEqual([...light.keys()].sort());
  });

  it("builds dark elevation by lightening, monotonically", () => {
    // The claim in index.css is that dark mode cannot use shadow for
    // depth, so each level up must actually be a lighter surface. If two
    // levels ever collapse to the same luminance the ladder conveys
    // nothing, and a dialog stops reading as being above the page.
    const ladder = SURFACE_LEVELS.map((l) => luminance(night.get(l) as string));
    for (let i = 1; i < ladder.length; i++) {
      expect(ladder[i], `dark surface-${SURFACE_LEVELS[i]} vs ${SURFACE_LEVELS[i - 1]}`).toBeGreaterThan(
        ladder[i - 1] as number,
      );
    }
  });

  it("keeps body text legible on every dark level", () => {
    // slate-200, the app's dark-mode body text.
    for (const level of SURFACE_LEVELS) {
      expect(contrast("#e2e8f0", night.get(level) as string), `text on dark ${level}`).toBeGreaterThanOrEqual(7);
    }
  });
});

describe("motion tokens (T-4206)", () => {
  it("defines three durations and three curves", () => {
    const durations = tokensIn(root, "motion");
    expect([...durations.keys()].sort()).toEqual(["base", "fast", "slow"]);
    for (const [name, value] of durations) expect(value, `--motion-${name}`).toMatch(/^\d+m?s$/);

    const eases = tokensIn(theme, "ease");
    expect([...eases.keys()].sort()).toEqual(["entrance", "exit", "standard"]);
  });

  it("uses asymmetric entrance and exit curves", () => {
    // A single symmetric ease used for both directions is the tell of
    // motion that was added rather than designed — things should arrive
    // decelerating and leave accelerating. See index.css.
    const eases = tokensIn(theme, "ease");
    expect(eases.get("entrance")).not.toBe(eases.get("exit"));
  });

  it("zeroes the duration tokens under prefers-reduced-motion", () => {
    // This is the entire reason the durations are tokens: a component that
    // animates through one becomes instant here without knowing this rule
    // exists. If the media query stopped redefining them, every such
    // component would silently keep animating.
    const at = css.indexOf("@media (prefers-reduced-motion: reduce)");
    expect(at, "reduced-motion block not found").toBeGreaterThanOrEqual(0);
    const block = css.slice(at);
    for (const name of ["fast", "base", "slow"]) {
      expect(block, `--motion-${name} under reduced motion`).toMatch(
        new RegExp(`--motion-${name}\\s*:\\s*0\\.01ms`),
      );
    }
  });
});

describe("typefaces (T-4202)", () => {
  it("declares self-hosted faces and never a remote stylesheet", () => {
    // The daemon is routinely reached over a management network with no
    // internet route; a CDN font link fails silently there and the page
    // renders in the fallback with nobody the wiser. Every src must be a
    // same-origin path.
    const sources = [...css.matchAll(/src:\s*url\("([^"]+)"\)/g)].map((m) => m[1]);
    expect(sources.length).toBeGreaterThan(0);
    for (const src of sources) expect(src, "font src must be same-origin").toMatch(/^\/fonts\//);
    expect(css).not.toMatch(/@import\s+url\(/);
    expect(css).not.toMatch(/fonts\.googleapis\.com|fonts\.gstatic\.com/);
  });

  it("keeps a system fallback chain behind each custom face", () => {
    // A corrupt or blocked woff2 must degrade to something reasonable
    // rather than to the browser's default serif.
    const fonts = tokensIn(theme, "font");
    const sans = fonts.get("sans");
    const mono = fonts.get("mono");
    expect(sans).toContain("IBM Plex Sans");
    expect(sans).toMatch(/sans-serif\s*$/);
    expect(mono).toContain("IBM Plex Mono");
    expect(mono).toMatch(/monospace\s*$/);
  });

  it("declares every weight the fallback-free faces need, in both subsets", () => {
    // A weight used by a `font-semibold` utility but never declared here
    // is synthesised by the browser (faux bold), which looks subtly wrong
    // and is invisible unless you know to look for it.
    for (const family of ["IBM Plex Sans", "IBM Plex Mono"]) {
      const weights = [...css.matchAll(new RegExp(`font-family: "${family}";[\\s\\S]{0,120}?font-weight: (\\d+);`, "g"))]
        .map((m) => m[1])
        .filter((w): w is string => w !== undefined);
      // Each declared weight appears once per unicode-range subset.
      const counts = new Map<string, number>();
      for (const w of weights) counts.set(w, (counts.get(w) ?? 0) + 1);
      expect(counts.size, `${family} declares no weights`).toBeGreaterThan(0);
      for (const [weight, n] of counts) expect(n, `${family} ${weight} subset count`).toBe(2);
    }
  });
});
