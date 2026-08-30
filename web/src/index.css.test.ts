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
  if (found === null) throw new Error(`${selector} rule not found in index.css`);
  const open = css.indexOf("{", found.index);
  const close = css.indexOf("}", open);
  expect(close, `${selector} block is not closed`).toBeGreaterThan(open);
  return css.slice(open + 1, close);
}

/** Every `--<prefix>-<name>: <literal>` declared in a block, as a map.
 * Only literal values are captured — a `var(...)` indirection returns
 * nothing, which is intentional: a token this suite cannot resolve is a
 * token whose contrast it cannot check, and that should fail loudly
 * rather than silently pass.
 *
 * T-4215: the suffix is optional, and a bare `--<prefix>` lands under the
 * key `""`. The families that existed before this all had suffixed names
 * (`--color-status-critical`, `--color-surface-page`, `--color-accent-600`)
 * so nothing else changes; `--color-fg` and `--color-border` are the first
 * roles whose *default* is the natural call-site name, and a helper that
 * silently skipped them would have reported them as undefined rather than
 * as unreadable. */
function tokensIn(block: string, prefix: string): Map<string, string> {
  const out = new Map<string, string>();
  const re = new RegExp(`--${prefix}(?:-([a-z0-9-]+))?\\s*:\\s*([^;]+);`, "g");
  for (const m of block.matchAll(re)) {
    const [, name, value] = m;
    if (value !== undefined) out.set(name ?? "", value.trim());
  }
  return out;
}

const HEX = /^#[0-9a-f]{6}$/;

/** Reads a token that is required to exist.
 *
 * Returns a plain `string`, so no call site needs a non-null assertion —
 * and, more usefully, a missing token fails HERE, naming itself, instead
 * of surfacing later as an "expected undefined to be a hex colour" that
 * says nothing about which of the thirty-odd tokens went missing. */
function token(tokens: Map<string, string>, name: string): string {
  const value = tokens.get(name);
  if (value === undefined) throw new Error(`token --...-${name} is not defined`);
  return value;
}

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

/** OKLCH chroma — how COLOURED a colour is, in the same space `hue` uses.
 *
 * T-4211's gate needs this because a hue comparison is only meaningful
 * between two colours that have one. `--color-status-unknown` sits at chroma
 * 0.0147, an order of magnitude below every other status; measuring its hue
 * produced a 31deg "collision" with the base accent that is pure noise, while
 * the same metric said nothing about two colours at chroma 0.16 sitting 24deg
 * apart, which was the real defect. */
function chroma(hex: string): number {
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
  return Math.hypot(a, bb);
}

const AA = 4.5;
/** Every surface a status colour can be drawn on, read from the ladder
 * itself rather than named as a literal.
 *
 * This used to be `WHITE = "#ffffff"` and `SLATE_900 = "#0f172b"`, described
 * as "the two surfaces this app actually renders on". That was true before
 * T-4203 introduced a four-level ladder, and it silently stopped being true
 * afterwards — the light page is `#fafbfd`, not `#ffffff`, and the dark
 * raised surface is `#182133`, not `#0f172b`.
 *
 * The cost was measurable and shipped: `--color-status-stale` scored 4.61 on
 * `#ffffff` and passed this gate, while measuring **4.48 on the page it is
 * actually drawn on** — a real AA failure that T-4212's axe sweep found and
 * this suite could not, because it was still measuring the pre-ladder
 * denominator. That is the third time in this phase a green gate has been
 * pointed at the wrong surface (T-4215's slate table, T-4305's outline
 * token), so the fix here is not a better pair of literals: it is to stop
 * having literals.
 *
 * WHITE/SLATE_900 survive below for the ACCENT ramp's own assertions, which
 * measure a hover blend rather than a surface and are a separate question
 * (T-4214). They are no longer used for anything the status scale asserts. */
function surfaceLadder(isDark: boolean): string[] {
  const surfaces = tokensIn(isDark ? dark : theme, "color-surface");
  return ["page", "raised", "sunken", "overlay"].map((level) => token(surfaces, level));
}

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
  /** T-4214 added `--color-accent-fg` and `--color-accent-soft` alongside the
   * eleven numbered steps, so "every `--color-accent-*` token" is no longer
   * the same set as "the ramp". The ramp is what these two assertions are
   * about — a STEP must stay a literal hex, because a step that is an alias
   * cannot be compared by any test that does not itself resolve Tailwind
   * (see the html.demo block's own comment). The ROLES are asserted
   * separately, and `accent-soft` is deliberately not a hex at all. */
  const rampOnly = (m: Map<string, string>): Map<string, string> =>
    new Map([...m].filter(([k]) => /^\d+$/.test(k)));
  const base = rampOnly(tokensIn(theme, "color-accent"));
  const demoAccent = rampOnly(tokensIn(demo, "color-accent"));

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
    const separation = Math.abs(hue(token(base, "600")) - hue(token(demoAccent, "600")));
    expect(Math.min(separation, 360 - separation)).toBeGreaterThan(90);
  });

  it("clears every contrast floor index.css claims for the accent", () => {
    // These are the pairings that decide whether a component call site has
    // to move. index.css's T-4201 comment records each number; this
    // recomputes it, so the comment cannot drift from the tokens.
    const a = (step: string): string => token(base, step);
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
    const d = (step: string): string => token(demoAccent, step);
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

  it("gives every solid fill a text colour that clears AA on it, per theme", () => {
    // The reason --color-status-on-solid exists. The light `-solid` steps
    // are dark enough for white text; the dark ones are lighter and more
    // saturated, so white on them measures 2.36-3.66:1 — all below AA —
    // while a near-black clears it. Without this token every filled badge
    // would carry a hand-written `text-white dark:text-slate-900`, which
    // is precisely the conditional this token system exists to delete.
    for (const state of STATUS_STATES) {
      expect(contrast(token(light, "on-solid"), token(light, `${state}-solid`)), `light ${state}`)
        .toBeGreaterThanOrEqual(AA);
      expect(contrast(token(night, "on-solid"), token(night, `${state}-solid`)), `dark ${state}`)
        .toBeGreaterThanOrEqual(AA);
    }
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
      const lf = token(light, state);
      const ls = token(light, `${state}-solid`);
      const lw = token(light, `${state}-soft`);
      const onSolidLight = token(light, "on-solid");
      for (const surface of surfaceLadder(false)) {
        expect(contrast(lf, surface), `light ${state} fg on ${surface}`).toBeGreaterThanOrEqual(AA);
      }
      expect(contrast(lf, lw), `light ${state} fg on its soft wash`).toBeGreaterThanOrEqual(AA);
      expect(contrast(onSolidLight, ls), `light ${state} on-solid on solid`).toBeGreaterThanOrEqual(AA);

      const df = token(night, state);
      const dw = token(night, `${state}-soft`);
      const ds = token(night, `${state}-solid`);
      const onSolidDark = token(night, "on-solid");
      for (const surface of surfaceLadder(true)) {
        expect(contrast(df, surface), `dark ${state} fg on ${surface}`).toBeGreaterThanOrEqual(AA);
      }
      expect(contrast(df, dw), `dark ${state} fg on its soft wash`).toBeGreaterThanOrEqual(AA);
      expect(contrast(onSolidDark, ds), `dark ${state} on-solid on solid`).toBeGreaterThanOrEqual(AA);
    }
    // `stale` is a text colour at exactly one call site (SwitchView's 10px
    // chip) and a BORDER everywhere else (Badge's stale modifier), so it is
    // held to AA on the surfaces it is drawn on as text — which is what the
    // single `on white` assertion here used to stand in for, and got wrong.
    for (const isDark of [false, true]) {
      const stale = token(isDark ? night : light, "stale");
      for (const surface of surfaceLadder(isDark)) {
        expect(contrast(stale, surface), `${isDark ? "dark" : "light"} stale on ${surface}`).toBeGreaterThanOrEqual(
          AA,
        );
      }
    }
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
      for (const state of ["ok", "degraded", "critical"]) hues.set(state, hue(token(tokens, state)));
      hues.set("accent", hue(token(accent, accentStep)));
      const names = [...hues.keys()];
      for (let i = 0; i < names.length; i++) {
        for (let j = i + 1; j < names.length; j++) {
          const x = names[i] ?? "";
          const y = names[j] ?? "";
          const raw = Math.abs((hues.get(x) ?? 0) - (hues.get(y) ?? 0));
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
    const accent600 = token(tokensIn(theme, "color-accent"), "600");
    const raw = Math.abs(hue(token(light, "info")) - hue(accent600));
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
    const ladder = SURFACE_LEVELS.map((l) => luminance(token(night, l)));
    for (let i = 1; i < ladder.length; i++) {
      const level = SURFACE_LEVELS[i] ?? "";
      const below = SURFACE_LEVELS[i - 1] ?? "";
      expect(ladder[i], `dark surface-${level} vs ${below}`).toBeGreaterThan(ladder[i - 1] ?? 0);
    }
  });

  it("keeps body text legible on every dark level", () => {
    // slate-200, the app's dark-mode body text.
    for (const level of SURFACE_LEVELS) {
      expect(contrast("#e2e8f0", token(night, level)), `text on dark ${level}`).toBeGreaterThanOrEqual(7);
    }
  });
});

describe("foreground and border roles (T-4215)", () => {
  const lightFg = tokensIn(theme, "color-fg");
  const nightFg = tokensIn(dark, "color-fg");
  const lightBorder = tokensIn(theme, "color-border");
  const nightBorder = tokensIn(dark, "color-border");

  /** AA is 4.5:1 for normal text; the two roles that carry actual prose are
   * held to AAA-adjacent 7:1, which is what they already measured. */
  const FG_TARGET: Record<string, number> = {
    "": 7, // --color-fg
    body: 7,
    muted: 4.5,
    subtle: 4.5,
  };

  it("defines the same role set in both themes", () => {
    // Same equality the status scale gets, and for the same reason: a role
    // defined in @theme but missing under html.dark silently leaks a
    // light-mode text colour onto a dark surface, and the five that ARE
    // present look right in review.
    expect([...nightFg.keys()].sort()).toEqual([...lightFg.keys()].sort());
    expect([...nightBorder.keys()].sort()).toEqual([...lightBorder.keys()].sort());
    for (const role of Object.keys(FG_TARGET)) {
      expect(lightFg.get(role), `light --color-fg${role ? `-${role}` : ""}`).toMatch(HEX);
      expect(nightFg.get(role), `dark --color-fg${role ? `-${role}` : ""}`).toMatch(HEX);
    }
  });

  it("clears its contrast target on every surface, in both themes", () => {
    // Measured against the SURFACE TOKENS, not against literal white.
    // slateContrast.test.ts measured against `bg-white`/`dark:bg-slate-900`
    // and was correct when written; T-4203 then introduced a ladder whose
    // sunken level is darker than white, and the guard could not see that
    // its denominator had moved. Reading the surfaces from the same
    // stylesheet means the next surface change breaks this test rather
    // than quietly breaking the contrast.
    for (const [themeName, fg, surfaces] of [
      ["light", lightFg, tokensIn(theme, "color-surface")],
      ["dark", nightFg, tokensIn(dark, "color-surface")],
    ] as const) {
      for (const [role, target] of Object.entries(FG_TARGET)) {
        const name = `--color-fg${role ? `-${role}` : ""}`;
        for (const level of SURFACE_LEVELS) {
          const ratio = contrast(token(fg, role), token(surfaces, level));
          expect(ratio, `${themeName}: ${name} on surface-${level}`).toBeGreaterThanOrEqual(target);
        }
      }
    }
  });

  it("does not let fg-subtle regress to slate-500", () => {
    // The specific live failure this card was filed for: slate-500 measures
    // 4.39:1 on surface-sunken, under AA, at 172 call sites. It passes on
    // white (4.76), which is why it survived. Asserted by value as well as
    // by ratio, because the ratio test above would also be satisfied by
    // someone darkening the SURFACE instead — which would fix the number
    // and break the ladder.
    expect(token(lightFg, "subtle").toLowerCase()).not.toBe("#64748b");
    expect(contrast("#64748b", token(tokensIn(theme, "color-surface"), "sunken"))).toBeLessThan(4.5);
  });

  it("keeps fg-muted and fg-subtle distinct where there is room to be", () => {
    // Light mode has room and uses it. Dark mode does not: slate-400 is the
    // dimmest grey that clears AA on all four dark surfaces (5.53), and the
    // next step down measures 2.98-4.02. So the two roles collapsing in
    // dark is forced, and asserting it here stops someone "fixing" the
    // duplication by picking an unreadable slate-500.
    expect(token(lightFg, "muted")).not.toBe(token(lightFg, "subtle"));
    expect(token(nightFg, "muted")).toBe(token(nightFg, "subtle"));
    const darkSurfaces = tokensIn(dark, "color-surface");
    for (const level of SURFACE_LEVELS) {
      expect(
        contrast("#64748b", token(darkSurfaces, level)),
        `slate-500 on dark surface-${level} — the step below fg-muted`,
      ).toBeLessThan(4.5);
    }
  });

  it("keeps borders visible against the surfaces they separate", () => {
    // Borders are boundaries, not text, so AA does not apply — but a border
    // that measures 1.0 against its surface is not a border. The floor is
    // set just under the weakest real pairing so it catches a border going
    // invisible without dictating how strong it should be.
    for (const [themeName, borders, surfaces] of [
      ["light", lightBorder, tokensIn(theme, "color-surface")],
      ["dark", nightBorder, tokensIn(dark, "color-surface")],
    ] as const) {
      for (const role of ["", "strong"]) {
        const name = `--color-border${role ? `-${role}` : ""}`;
        for (const level of ["page", "raised"] as const) {
          const ratio = contrast(token(borders, role), token(surfaces, level));
          expect(ratio, `${themeName}: ${name} on surface-${level}`).toBeGreaterThan(1.08);
        }
      }
    }
  });

  it("holds `outline` to the 3:1 non-text floor on every surface, unlike `border`", () => {
    // T-4301. These two roles look interchangeable and are not. `border`
    // separates a row from the next row and is deliberately faint; `outline`
    // delineates an object a user has to perceive — a node on the topology
    // map, the edge between two of them — and WCAG 1.4.11 asks 3:1 of it.
    //
    // Asserted against ALL FOUR surfaces. The first solve for this token
    // used only the two the canvas happens to draw on and produced a value
    // measuring 2.89 on `sunken` and 2.65 on `overlay` — which is T-4215's
    // whole finding (a contrast number that is correct against a denominator
    // someone picked rather than against the one the app uses) repeated one
    // commit after fixing it.
    for (const [themeName, tokens, outlines, surfaces] of [
      ["light", lightBorder, tokensIn(theme, "color-outline"), tokensIn(theme, "color-surface")],
      ["dark", nightBorder, tokensIn(dark, "color-outline"), tokensIn(dark, "color-surface")],
    ] as const) {
      for (const level of SURFACE_LEVELS) {
        const outline = contrast(token(outlines, ""), token(surfaces, level));
        expect(outline, `${themeName}: --color-outline on surface-${level}`).toBeGreaterThanOrEqual(3);
      }
      // And the decorative one must stay decorative: an outline-strength
      // rule between every table row is the failure in the other direction.
      const worstBorder = Math.min(
        ...SURFACE_LEVELS.map((l) => contrast(token(tokens, ""), token(surfaces, l))),
      );
      expect(worstBorder, `${themeName}: --color-border should stay a hairline`).toBeLessThan(2);
    }
  });

  it("leaves neutrals alone in demo mode", () => {
    // Demo mode re-points the accent so the app reads as "not your
    // cluster". Retinting body text would make it read as broken instead.
    // Asserted rather than left implicit so a future demo-mode change has
    // to argue with a test instead of silently widening its blast radius.
    expect([...tokensIn(demo, "color-fg").keys()]).toEqual([]);
    expect([...tokensIn(demo, "color-border").keys()]).toEqual([]);
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

describe("accent roles (T-4214)", () => {
  // A ramp STEP is a value and cannot re-point; a ROLE can. The status scale
  // got roles at T-4204 and the accent did not, so every accent call site
  // spelled out two steps and a `dark:` conditional — the conditional a token
  // system exists to delete.
  //
  // These assertions are the reason the roles can be trusted in the one
  // combination nothing else covers. `html.dark` and `html.demo` have equal
  // specificity and demo comes second in the file, so demo wins on source
  // order for any property both define. While the blocks were disjoint that
  // was harmless. An accent role lands in both, so `html.demo.dark` has to
  // exist — and a key-set check of `html.dark` against `@theme`, which is the
  // shape every other token in this file gets, would pass while demo+dark
  // rendered demo's LIGHT foreground on a near-black page.
  const ROLES = ["fg", "soft", "border"];

  const COMBOS = [
    { name: "base / light", selectors: [] as string[], dark: false },
    { name: "base / dark", selectors: ["html.dark"], dark: true },
    { name: "demo / light", selectors: ["html.demo"], dark: false },
    { name: "demo / dark", selectors: ["html.dark", "html.demo", "html.demo.dark"], dark: true },
  ];

  /** Resolves a custom property the way the cascade would for one combination:
   * `@theme` first, then each selector in source order, last write winning. */
  function resolve(combo: (typeof COMBOS)[number], name: string): string {
    let value = (new RegExp(`${name}\\s*:\\s*([^;]+);`).exec(blockBody("@theme")))?.[1];
    for (const selector of combo.selectors) {
      const hit = (new RegExp(`${name}\\s*:\\s*([^;]+);`).exec(blockBody(selector)))?.[1];
      if (hit !== undefined) value = hit;
    }
    if (value === undefined) throw new Error(`${name} unresolved for ${combo.name}`);
    return value.trim();
  }

  /** `--color-accent-soft` is `color-mix(in srgb, <hex> N%, transparent)` — it
   * carries its own alpha ON PURPOSE. Flattening it to a hex would be correct
   * only where the wash sits on `surface-page`; measured elsewhere it diverges
   * by 7.5 RGB units on light-sunken and 28.3 on the dark overlay. So the
   * contrast checks below composite it over each surface, as the browser
   * will, rather than measuring the token in isolation. */
  function parseSoft(value: string): { hex: string; alpha: number } {
    const m = /color-mix\(in srgb,\s*(#[0-9a-f]{6})\s+(\d+)%/i.exec(value);
    if (m?.[1] === undefined || m[2] === undefined) {
      throw new Error(`accent-soft must carry its alpha via color-mix, got: ${value}`);
    }
    return { hex: m[1], alpha: Number(m[2]) / 100 };
  }

  function surfacesFor(dark: boolean): string[] {
    return surfaceLadder(dark);
  }

  it("defines the same accent-role key set in all four theme blocks", () => {
    // Not `html.dark` vs `@theme`. The block that would be missing is neither.
    for (const selector of ["@theme", "html.dark", "html.demo", "html.demo.dark"]) {
      const body = blockBody(selector);
      for (const role of ROLES) {
        expect(body, `${selector} is missing --color-accent-${role}`).toContain(`--color-accent-${role}:`);
      }
    }
  });

  it("clears AA on every surface, in all four combinations", () => {
    for (const combo of COMBOS) {
      const fg = resolve(combo, "--color-accent-fg");
      for (const surface of surfacesFor(combo.dark)) {
        expect(contrast(fg, surface), `${combo.name}: accent-fg on ${surface}`).toBeGreaterThanOrEqual(AA);
      }
    }
  });

  it("clears AA on its own wash, composited over each surface", () => {
    // The wash is translucent, so "the colour behind the text" is a different
    // value on every surface. Measuring the token in isolation would answer a
    // question no pixel ever asks.
    for (const combo of COMBOS) {
      const fg = resolve(combo, "--color-accent-fg");
      const soft = parseSoft(resolve(combo, "--color-accent-soft"));
      for (const surface of surfacesFor(combo.dark)) {
        const behind = over(soft.hex, surface, soft.alpha);
        expect(contrast(fg, behind), `${combo.name}: accent-fg on soft over ${surface}`).toBeGreaterThanOrEqual(AA);
      }
    }
  });

  it("keeps accent-border visible without pretending it is an outline", () => {
    // Decorative, like `--color-border`: the `accent-soft` wash already
    // delimits the callout these bound, so WCAG 1.4.11's 3:1 does not apply
    // and claiming it would force a colour the sweep did not ask for. Held to
    // the same "a border that measures 1.0 is not a border" floor the neutral
    // borders get, and no higher.
    for (const combo of COMBOS) {
      const border = resolve(combo, "--color-accent-border");
      const soft = parseSoft(resolve(combo, "--color-accent-soft"));
      for (const surface of surfacesFor(combo.dark)) {
        expect(contrast(border, surface), `${combo.name}: accent-border on ${surface}`).toBeGreaterThan(1.2);
        expect(
          contrast(border, over(soft.hex, surface, soft.alpha)),
          `${combo.name}: accent-border on its own wash over ${surface}`,
        ).toBeGreaterThan(1.2);
      }
    }
  });

  it("moves the wash TOWARD the accent, not merely to a passing ratio", () => {
    // A ratio alone does not catch a wash going the wrong way: the first pass
    // at this derivation produced a near-black "wash" for dark mode that
    // satisfied its contrast constraint perfectly. Direction is the property,
    // so direction is what is asserted.
    for (const combo of COMBOS) {
      const soft = parseSoft(resolve(combo, "--color-accent-soft"));
      const page = surfacesFor(combo.dark)[0] ?? "#ffffff";
      const washed = over(soft.hex, page, soft.alpha);
      if (combo.dark) {
        expect(luminance(washed), `${combo.name}: wash must be lighter than the page`).toBeGreaterThan(luminance(page));
      } else {
        expect(luminance(washed), `${combo.name}: wash must be darker than the page`).toBeLessThan(luminance(page));
      }
    }
  });
});

describe("accent vs status separation (T-4211)", () => {
  // Nothing asserted this before, in either mode, which is how a 23.5deg
  // collision was introduced, reviewed and merged with a green suite. The
  // 40deg floor was held only BETWEEN status pairs; the accent was exempt
  // from it, and `html.demo` was never looked at by any assertion in this
  // file.
  //
  // **Chroma-aware, because hue alone lies at both ends.** A naive comparison
  // flags base accent-600 against `unknown` at 31deg — noise, because
  // `unknown` has chroma 0.0147, an order of magnitude below every other
  // status, and a near-neutral does not have a hue worth comparing. The same
  // naive metric said nothing about two colours at chroma 0.16 sitting 24deg
  // apart, which is the real defect. So the floor applies only where BOTH
  // colours are actually coloured.
  const CHROMA_FLOOR = 0.04;
  const HUE_FLOOR = 40;

  const MODES = [
    { name: "base / light", selectors: [] as string[], dark: false },
    { name: "base / dark", selectors: ["html.dark"], dark: true },
    { name: "demo / light", selectors: ["html.demo"], dark: false },
    { name: "demo / dark", selectors: ["html.dark", "html.demo", "html.demo.dark"], dark: true },
  ];

  function resolveToken(mode: (typeof MODES)[number], name: string): string {
    let value = /(#[0-9a-f]{6})/.exec((new RegExp(`${name}\\s*:\\s*([^;]+);`).exec(blockBody("@theme")))?.[1] ?? "")?.[1];
    for (const selector of mode.selectors) {
      const hit = /(#[0-9a-f]{6})/.exec((new RegExp(`${name}\\s*:\\s*([^;]+);`).exec(blockBody(selector)))?.[1] ?? "")?.[1];
      if (hit !== undefined) value = hit;
    }
    if (value === undefined) throw new Error(`${name} unresolved for ${mode.name}`);
    return value;
  }

  function separation(a: number, b: number): number {
    const d = Math.abs(a - b) % 360;
    return Math.min(d, 360 - d);
  }

  it("keeps the accent 40deg from every SATURATED status hue, in all four modes", () => {
    for (const mode of MODES) {
      const accent = resolveToken(mode, "--color-accent-600");
      const accentChroma = chroma(accent);
      for (const state of STATUS_STATES) {
        // `info` is the documented exception in BASE mode — see the
        // convergence assertion below — so it is checked there, not here.
        if (!mode.name.startsWith("demo") && state === "info") continue;
        const status = resolveToken(mode, `--color-status-${state}`);
        if (chroma(status) < CHROMA_FLOOR || accentChroma < CHROMA_FLOOR) continue;
        expect(
          separation(hue(accent), hue(status)),
          `${mode.name}: accent-600 vs status-${state} — a selected row and a ${state} row must not be the same colour`,
        ).toBeGreaterThanOrEqual(HUE_FLOOR);
      }
    }
  });

  it("exempts `unknown` by measuring chroma, not by naming it", () => {
    // The exemption has to be a property, not a list: a future status that
    // happens to be near-neutral should be exempt for the same reason, and a
    // future `unknown` that gains chroma should stop being.
    for (const mode of MODES) {
      expect(chroma(resolveToken(mode, "--color-status-unknown")), `${mode.name}: unknown`).toBeLessThan(CHROMA_FLOOR);
    }
  });

  it("keeps `info` ON the accent hue in BASE mode only, and says so", () => {
    // T-4204 made `info` converge with the accent deliberately: "an
    // informational status IS the accent". That intent cannot survive demo
    // mode, which re-points the accent and leaves `info` where it is — the
    // old assertion measured 0.3deg while demo mode measured 178.9deg, and
    // passed, because it only ever resolved `@theme`.
    //
    // So the intent is base-mode-only and is now stated as such, rather than
    // asserted globally and checked locally.
    for (const mode of MODES) {
      const sep = separation(hue(resolveToken(mode, "--color-accent-600")), hue(resolveToken(mode, "--color-status-info")));
      if (mode.name.startsWith("demo")) {
        expect(sep, `${mode.name}: info is NOT the accent here, and must be clear of it`).toBeGreaterThanOrEqual(HUE_FLOOR);
      } else {
        expect(sep, `${mode.name}: info IS the accent`).toBeLessThan(10);
      }
    }
  });
});
