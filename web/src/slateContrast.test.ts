// SPDX-License-Identifier: Apache-2.0

// T-3406-followup-02: bare `text-slate-400` / `text-slate-500`, guarded at
// the source.
//
// This defect class has now been found and fixed three times — T-2004 (one
// call site, measured 2.63:1), T-3406 (Phase 34's close-out), and
// T-3406-followup-01 (~130 sites across 19 routes). Each fix was local and
// correct; none of them stopped the next one. The reason is that an axe
// sweep proves what it *renders*, and most of these call sites are on
// routes no spec visits, in transient states ("checking…", "none found")
// a passing test never sees, or behind a `disabled` control.
//
// THE MEASUREMENT — REWRITTEN AT T-4215, because the old one had gone
// stale in a way that is worth recording rather than quietly deleting.
//
// It read, correctly when written:
//
//     foreground     on white     on slate-900
//     slate-400        2.56 ✗         6.96 ✓
//     slate-500        4.76 ✓         3.75 ✗
//     slate-600        7.58 ✓         2.36 ✗
//
// Then T-4203 introduced a surface *ladder* — sunken/page/raised/overlay —
// and the app stopped sitting on `bg-white` and `dark:bg-slate-900`.
// `--color-surface-sunken` is #f4f6f8, darker than white, and slate-500 on
// it measures 4.39, not 4.76. Below AA. At 172 call sites. This guard could
// not see it, because introducing a ladder changed the denominator of every
// contrast measurement in the codebase and the guard kept the old one.
//
// So the numbers are no longer written down here at all. They are READ from
// index.css at run time, against every surface in the ladder, so the next
// surface change breaks this test instead of silently breaking contrast.
//
// AA is 4.5:1 for normal text. `text-fg-muted` (T-4215) clears it on every
// surface in both themes and is what these call sites should use; the raw
// pairing `text-slate-600 dark:text-slate-400` also clears it and is what
// most of them say today.
//
// WHY A SOURCE SCAN AND NOT ESLINT. There is no custom-rule infrastructure
// in web/eslint.config.js, so a rule means taking on a plugin dependency;
// and a lint code cannot carry the table above, which is the part that
// makes the fix obvious instead of arbitrary. index.css.test.ts and
// topology/mapContainerFloor.test.ts set the precedent for asserting a CSS
// fact this suite cannot resolve, for the same reason.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// `__dirname` rather than `import.meta.url` — under jsdom the latter is an
// http: URL, not a file: one (see index.css.test.ts, i18nCoverage.test.ts).
const SRC = resolve(__dirname);

/** Call sites whose slate step is measured against something other than the
 * surface ladder, keyed by the exact `className` literal so an exemption
 * covers one element rather than a whole file.
 *
 * T-4215 replaced the previous file-keyed `ALWAYS_DARK_SURFACES`, which was
 * empty. It was empty for a good reason at the time — no always-dark panel
 * then contained a bare slate step — but the shape was wrong twice over.
 * Exempting a *file* silently exempts every future element added to it; and
 * "always dark" turned out to be only one of the three reasons a call site
 * legitimately sits outside the ladder. The other two only appeared once the
 * detector started measuring pairs instead of counting them.
 *
 * Every entry states which reason applies. An entry whose className drifts
 * stops matching and the site is reported again, which is the intended
 * failure mode: a stale exemption should expire, not persist. */
const OFF_LADDER: Record<string, string> = {
  // 1. Always-dark surface. The element's own className carries an
  //    unprefixed dark background, so the light ladder never applies.
  "flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-amber-500/30 bg-slate-950 px-3 py-1.5 text-sm text-slate-200":
    "DemoBanner: bg-slate-950 in both themes",
  "mt-1 w-64 rounded border border-slate-600 bg-slate-900 px-2 py-1 text-sm text-slate-100":
    "IncidentsPage: bg-slate-900 in both themes",
  "mt-1 rounded border border-slate-600 bg-slate-900 px-2 py-1 text-sm text-slate-100":
    "IncidentsPage: bg-slate-900 in both themes",
  "rounded-sm border border-slate-600 px-1 text-[9px] uppercase leading-tight tracking-wider text-slate-300":
    "SwitchFaceplate name plate (T-3503): bg-slate-800 in both themes",

  // 2. Always-COLOURED surface — the case the old file-keyed list had no
  //    name for. A status or accent fill is the same in both themes, so the
  //    text on it answers to that fill and not to the ladder.
  "ml-auto hidden shrink-0 rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold text-slate-900 sm:inline-block":
    "Sidebar count pill: slate-900 on bg-amber-500, identical in both themes",
  "absolute max-w-[200px] rounded border border-amber-400 bg-amber-50 p-1 text-[10px] text-slate-800 shadow-sm dark:border-amber-500 dark:bg-amber-950 dark:text-amber-100":
    "AnnotationLayer: amber fill in both themes, and its dark partner is amber-100, not a slate step",

  // 3. Not text. `stroke="currentColor"` on a chart grid takes its colour
  //    from a text-* class, but a gridline is a decorative graphic: AA does
  //    not apply, and a grid that met 4.5:1 would overpower its own chart.
  //    slate-200/slate-700 is the correct faint pairing, not an inverted one
  //    — which is what it looks like until you read the element.
  "text-slate-200 dark:text-slate-700": "CartesianGrid stroke in MetricsTab and AnalyticsTab: decorative gridline, not text",
};

function tsxFilesUnder(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (entry === "__fixtures__" || entry === "node_modules") continue;
      tsxFilesUnder(full, out);
    } else if (entry.endsWith(".tsx") && !entry.endsWith(".test.tsx")) {
      out.push(full);
    }
  }
  return out;
}

/** Every `className="..."` literal in a source file. Template literals and
 * `clsx()` calls are not matched: this guard deliberately covers the plain,
 * overwhelmingly common form rather than trying to be a CSS-aware parser
 * and being wrong in ways nobody can predict. */
function classNameLiterals(source: string): string[] {
  return [...source.matchAll(/className="([^"]*)"/g)].map((m) => m[1] ?? "");
}

/** Tailwind's slate scale, and the surface ladder read out of index.css, so
 * this guard measures against what the app actually renders on rather than
 * against a table someone typed once. */
const SLATE: Record<string, string> = {
  "50": "#f8fafc", "100": "#f1f5f9", "200": "#e2e8f0", "300": "#cbd5e1", "400": "#94a3b8",
  "500": "#64748b", "600": "#475569", "700": "#334155", "800": "#1e293b", "900": "#0f172a",
  "950": "#020617",
};

function surfacesFrom(css: string, selector: string): string[] {
  const start = new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m").exec(css);
  if (start === null) throw new Error(`${selector} not found in index.css`);
  const body = css.slice(start.index, css.indexOf("}", start.index));
  return [...body.matchAll(/--color-surface-[a-z]+\s*:\s*(#[0-9a-f]{6})\s*;/g)].map((m) => m[1] ?? "");
}

const CSS = readFileSync(resolve(__dirname, "index.css"), "utf8");
/** Light surfaces come from `@theme`, dark ones from the `html.dark` override. */
const LIGHT_SURFACES = surfacesFrom(CSS, "@theme");
const DARK_SURFACES = surfacesFrom(CSS, "html.dark");

function relLum(hex: string): number {
  const chan = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const lin = chan.map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * (lin[0] ?? 0) + 0.7152 * (lin[1] ?? 0) + 0.0722 * (lin[2] ?? 0);
}
function ratio(a: string, b: string): number {
  const [hi, lo] = [relLum(a), relLum(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}
/** Worst case across the whole ladder — the surface a call site sits on is
 * not knowable from its className, so the guard assumes the hardest one. */
function worstOn(hex: string, surfaces: string[]): number {
  return Math.min(...surfaces.map((s) => ratio(hex, s)));
}

const AA = 4.5;

/** True when this class list renders slate text that fails AA in either
 * theme. `hover:text-slate-400` and friends are ignored — a hover state is
 * not body text, and AA does not apply to it the same way.
 *
 * T-4215 widened this. It used to ask only whether SOME `dark:text-` token
 * was present, and treat that as rescue. It is not: `text-slate-400
 * dark:text-slate-500` has a dark partner and fails in BOTH themes (2.37-2.56
 * light, 2.98-4.02 dark), and the old unit tests below asserted that pairing
 * was fine. Presence of a partner is not legibility of a partner, so both
 * halves are now measured. */
function hasUnpairedSlate(classes: string): boolean {
  const tokens = classes.split(/\s+/).filter(Boolean);
  const lightStep = tokens.find((t) => /^text-slate-\d{2,3}$/.test(t))?.slice("text-slate-".length);
  const darkStep = tokens.find((t) => /^dark:text-slate-\d{2,3}$/.test(t))?.slice("dark:text-slate-".length);
  // Nothing to judge: no plain slate text step at all.
  if (lightStep === undefined && darkStep === undefined) return false;

  // A light step with no dark partner renders in BOTH themes, so it has to
  // clear AA on both ladders. With a partner, each half answers for its own.
  const lightHex = lightStep === undefined ? undefined : SLATE[lightStep];
  const darkHex = darkStep === undefined ? undefined : SLATE[darkStep];

  if (lightHex !== undefined) {
    if (worstOn(lightHex, LIGHT_SURFACES) < AA) return true;
    if (darkHex === undefined && worstOn(lightHex, DARK_SURFACES) < AA) return true;
  }
  if (darkHex !== undefined && worstOn(darkHex, DARK_SURFACES) < AA) return true;
  return false;
}

export interface Offence {
  readonly file: string;
  readonly classes: string;
}

function findOffences(): Offence[] {
  const found: Offence[] = [];
  for (const file of tsxFilesUnder(SRC)) {
    const rel = relative(SRC, file);
    for (const classes of classNameLiterals(readFileSync(file, "utf8"))) {
      if (classes in OFF_LADDER) continue;
      if (hasUnpairedSlate(classes)) found.push({ file: rel, classes });
    }
  }
  return found;
}

describe("slate text contrast", () => {
  it("has no bare text-slate-400/500 outside an always-dark surface", () => {
    const offences = findOffences();
    const detail = offences.map((o) => `  ${o.file}\n    className="${o.classes}"`).join("\n");
    expect(
      offences,
      offences.length === 0
        ? ""
        : `${String(offences.length)} call site(s) use a bare slate text step with no dark: pairing.\n\n` +
          "Each light step is measured against every surface in the light ladder and\n" +
          "each dark: partner against every surface in the dark one, read live from\n" +
          "index.css. AA for normal text is 4.5:1.\n\n" +
          "Use the T-4215 role tokens — `text-fg`, `text-fg-body`, `text-fg-muted`,\n" +
          "`text-fg-subtle` — which resolve per theme and need no `dark:` partner.\n" +
          "If this element does not sit on the surface ladder at all (an always-dark\n" +
          "or always-coloured fill, or an SVG stroke rather than text), add its exact\n" +
          "className to OFF_LADDER with which of those three reasons applies.\n\n" +
          detail,
    ).toEqual([]);
  });

  // A guard only ever seen green is a guard nobody has tested. This drives
  // the detector over inputs directly, so a refactor that quietly broke the
  // matching — and left the suite passing because the tree happens to be
  // clean — fails here instead.
  it("detects the shapes it claims to detect", () => {
    expect(hasUnpairedSlate("text-sm text-slate-400")).toBe(true);
    expect(hasUnpairedSlate("text-xs text-slate-500 italic")).toBe(true);
    // T-4215: this line used to assert `false`. A dark partner was treated
    // as rescue without anyone measuring it — and this particular pairing
    // fails in BOTH themes (slate-400 is 2.37-2.56 on the light ladder,
    // slate-500 is 2.98-4.02 on the dark one). The old guard blessed it.
    expect(hasUnpairedSlate("text-slate-400 dark:text-slate-500")).toBe(true);
    // Genuinely rescued: both halves clear AA on their own ladder.
    expect(hasUnpairedSlate("text-slate-600 dark:text-slate-400")).toBe(false);
    // No unpaired slate step works in both themes, and that is the whole
    // argument for `--color-fg-*`. A grey dark enough to read on the light
    // ladder is too dark to read on the dark one, and vice versa; the scale
    // has no member that satisfies both. Every one of these is an offence:
    for (const step of ["300", "400", "500", "600", "700", "800", "900"]) {
      expect(hasUnpairedSlate(`text-slate-${step}`), `bare text-slate-${step}`).toBe(true);
    }
    // Not a bare step: a hover or variant prefix is a different question.
    expect(hasUnpairedSlate("hover:text-slate-400")).toBe(false);
    expect(hasUnpairedSlate("group-hover:text-slate-500")).toBe(false);
    // Substring traps: slate-4000 is not slate-400, and border is not text.
    expect(hasUnpairedSlate("border-slate-400")).toBe(false);
    expect(hasUnpairedSlate("text-slate-4000")).toBe(false);
  });

  it("reads the tree it thinks it is reading", () => {
    // Guards against the scan silently walking an empty directory — which
    // would make the assertion above pass for the worst possible reason.
    expect(tsxFilesUnder(SRC).length).toBeGreaterThan(100);
  });
});
