// SPDX-License-Identifier: Apache-2.0

// T-3106 acceptance criterion 2: a gate that fails on a new hardcoded
// user-facing string inside the bounded, localized subset
// (web/src/onboarding/OnboardingWalkthrough.tsx — see that file's own doc
// comment and web/src/i18n/i18n.ts's for why this is the chosen boundary).
// Same source-derived-scan style as web/src/help/coverage.test.ts and
// web/src/ipam/nextFreeCoverage.test.ts (T-3104): rather than hand-listing
// today's strings (which would only re-assert the current file and say
// nothing about a string added tomorrow), this scans the subset's own
// source for two shapes of hardcoded copy:
//
//   1. JSX text content: `<Tag ...>some text</Tag>` where the text between
//      a matching open/close tag pair contains no `{`/`}` (i.e. it is a
//      plain string, not an expression container routed through `t()` or
//      <Trans>) and contains a run of 2+ letters (so pure punctuation/
//      digits/glyphs like "/" or "▾" don't trip it).
//   2. Label-ish string-literal JSX props: `aria-label="..."`,
//      `title="..."`, `placeholder="..."`, `alt="..."`, `label="..."` given
//      as a plain quoted string rather than `={t("...")}` or `={someExpr}`.
//
// This mirrors exactly the two "reasonable approaches" the task card names
// for this gate. It intentionally does *not* try to catch every hardcoded
// string literal anywhere in the file's JS logic (e.g. inside a `toast()`
// call's object literal) — that needs real AST analysis to do without a
// flood of false positives on legitimate non-copy strings (CSS classes,
// object keys, technical identifiers), which is out of scope for a
// source-regex gate. The real code in this subset routes those through
// `t()` too (see OnboardingWalkthrough.tsx); this gate enforces the
// decidable half.
//
// ANTI-VACUITY (the same convention coverage.test.ts and
// nextFreeCoverage.test.ts use): a floor asserts the scan actually found
// real `t()`/<Trans> usage in the subset, so a regex that stops matching
// anything can't pass by finding nothing to flag.
import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = resolve(__dirname, "..");
const SUBSET_DIR = join(SRC, "onboarding");

// Only .tsx files carry JSX; the subset's pure .ts modules
// (foundSummary.ts, onboardingMachine.ts, protectedDraft.ts, queries.ts)
// render nothing and were confirmed string-free by inspection (T-3106
// report).
function subsetFiles(): string[] {
  return readdirSync(SUBSET_DIR)
    .filter((f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"))
    .map((f) => join(SUBSET_DIR, f));
}

// `<Tag attrs...>TEXT</Tag>` — TEXT must contain no `{`/`}` (so an
// expression container, however it resolves, never matches) and the
// backreference requires a real matching close tag, so this can't cross
// into a sibling/nested element's own content.
const JSX_TEXT_RE = /<([A-Za-z][\w.]*)\b[^>]*>([^<>{}]+)<\/\1>/g;

// A label-ish prop given as a literal string, e.g. `aria-label="Foo"` —
// NOT `aria-label={t("foo")}` (that's a `{`, not a `"`, right after `=`).
const LABEL_PROP_RE = /\b(aria-label|title|placeholder|alt|label)="([^"]+)"/g;

const HAS_LETTERS_RE = /[A-Za-z]{2,}/;

function jsxTextViolations(source: string): string[] {
  return [...source.matchAll(JSX_TEXT_RE)]
    .map((m) => m[2]?.trim() ?? "")
    .filter((text) => HAS_LETTERS_RE.test(text));
}

function labelPropViolations(source: string): string[] {
  return [...source.matchAll(LABEL_PROP_RE)]
    .map((m) => m[2] ?? "")
    .filter((text) => HAS_LETTERS_RE.test(text));
}

describe("onboarding i18n coverage (T-3106 acceptance criterion 2)", () => {
  const files = subsetFiles();

  it("finds .tsx files to check (anti-vacuity floor)", () => {
    expect(files.length).toBeGreaterThanOrEqual(1);
  });

  it("the walkthrough actually routes copy through t()/<Trans> (anti-vacuity floor)", () => {
    const source = readFileSync(join(SUBSET_DIR, "OnboardingWalkthrough.tsx"), "utf8");
    const tCalls = [...source.matchAll(/\bt\(["'`]/g)].length;
    expect(tCalls).toBeGreaterThanOrEqual(20);
    expect(source).toContain("<Trans ");
    expect(source).toContain('useTranslation("onboarding")');
  });

  for (const file of files) {
    const name = file.split("/").pop() ?? file;
    it(`${name} has no hardcoded JSX text content`, () => {
      const source = readFileSync(file, "utf8");
      expect(jsxTextViolations(source)).toEqual([]);
    });

    it(`${name} has no hardcoded aria-label/title/placeholder/alt/label prop`, () => {
      const source = readFileSync(file, "utf8");
      expect(labelPropViolations(source)).toEqual([]);
    });
  }
});
