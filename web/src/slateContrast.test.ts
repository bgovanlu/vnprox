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
// THE MEASUREMENT. Against the two surfaces this app actually uses —
// `bg-white`, and `dark:bg-slate-900` (147 call sites against 16 for
// slate-950):
//
//     foreground     on white     on slate-900
//     slate-400        2.56 ✗         6.96 ✓
//     slate-500        4.76 ✓         3.75 ✗
//     slate-600        7.58 ✓         2.36 ✗
//
// AA is 4.5:1 for normal text. So the two bare classes fail in *opposite*
// themes — which is exactly why neither is obvious to someone working in
// one theme, and why both survived three sweeps. `text-slate-600
// dark:text-slate-400` clears both (7.58 / 6.96), and is the pairing
// SwitchView.tsx:88 already documents with its own measurement.
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

/** Files whose bare `slate-400` would be *correct*, because the surface
 * under it is dark in BOTH themes — where slate-400 measures 6.96:1 and the
 * pairing this guard asks for would instead give 2.36:1 in light mode.
 *
 * **Empty, and that is a finding rather than an omission.** Only three
 * files in the tree carry an unprefixed always-dark background token
 * (`demo/DemoBanner.tsx`'s bg-slate-950, `incidents/IncidentsPage.tsx`'s
 * bg-slate-900, and `topology/SwitchFaceplate.tsx`'s bg-slate-800 name
 * plate, T-3503), and not one of them contains a bare slate text step.
 * Every other apparent dark surface in this codebase is a `dark:`- or
 * `dark:hover:`-prefixed variant on an ordinary light/dark component —
 * `InspectorPanel.tsx`, the largest single offender at 14 sites, looked
 * dark-surfaced under a careless grep purely because of five
 * `dark:hover:bg-slate-800` rows.
 *
 * The hook is kept because the situation is one always-dark panel away from
 * changing, and because a guard whose only escape hatch is "edit the
 * detector" gets its detector edited. */
const ALWAYS_DARK_SURFACES: Record<string, string> = {};

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

/** True when this class list uses a bare slate step with no dark: pairing
 * to rescue it. `hover:text-slate-400` and friends are ignored — a hover
 * state is not body text, and AA does not apply to it the same way. */
function hasUnpairedSlate(classes: string): boolean {
  const tokens = classes.split(/\s+/).filter(Boolean);
  const bare = tokens.some((t) => t === "text-slate-400" || t === "text-slate-500");
  if (!bare) return false;
  return !tokens.some((t) => t.startsWith("dark:text-"));
}

export interface Offence {
  readonly file: string;
  readonly classes: string;
}

function findOffences(): Offence[] {
  const found: Offence[] = [];
  for (const file of tsxFilesUnder(SRC)) {
    const rel = relative(SRC, file);
    if (rel in ALWAYS_DARK_SURFACES) continue;
    for (const classes of classNameLiterals(readFileSync(file, "utf8"))) {
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
          "  slate-400 measures 2.56:1 on bg-white   (AA needs 4.5:1)\n" +
          "  slate-500 measures 3.75:1 on slate-900  (AA needs 4.5:1)\n\n" +
          "Use `text-slate-600 dark:text-slate-400` (7.58:1 / 6.96:1), the pairing\n" +
          "SwitchView.tsx:88 documents. If the surface under this element is dark in\n" +
          "BOTH themes, add the file to ALWAYS_DARK_SURFACES with its reason instead.\n\n" +
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
    // Paired: rescued in the other theme.
    expect(hasUnpairedSlate("text-slate-400 dark:text-slate-500")).toBe(false);
    expect(hasUnpairedSlate("text-slate-600 dark:text-slate-400")).toBe(false);
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
