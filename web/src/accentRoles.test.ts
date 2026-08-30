// SPDX-License-Identifier: Apache-2.0

// T-4214 AC5: the gate that stops this sweep decaying.
//
// The accent's problem was never that the colours were wrong — every step in
// use measured above AA on its worst surface. It was that a ramp STEP is a
// value and cannot re-point per theme, so every call site had to name two
// steps and a `dark:` conditional, and one recipe got copy-pasted verbatim
// into nine files. `--color-accent-fg` / `-soft` / `-border` re-point, so the
// conditional is deletable — and stays deleted only if reintroducing one
// fails something.
//
// This is the same lesson the status sweep learned the hard way: T-4204's
// adoption decayed into 114 files because nothing failed when a call site
// went back to naming a value. A sweep without a gate is a snapshot.
//
// Deliberately NOT a general `dark:` purge (the card says so explicitly). The
// neutral pairing `text-slate-600 dark:text-slate-400` is intentional and is
// guarded by slateContrast.test.ts; it stays.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = resolve(__dirname);

/** Every `dark:`-prefixed accent utility that is allowed to remain, with the
 * reason it is allowed. An entry here is a decision someone made on purpose;
 * an accent `dark:` anywhere else is the drift this file exists to catch.
 *
 * Both survivors are HOVER states, and hover is the one case this codebase
 * has already decided is different: `Button.tsx` documents its own
 * `hover:bg-accent-500` as acceptable because "a hover state is transient and
 * not the gating resting contrast", a pairing it has re-derived four times.
 * Giving a transient state a re-pointing role token would mean deriving and
 * gating a fourth role for a colour that exists only while a pointer is over
 * it. If a future card wants `--color-accent-hover`, it should measure first
 * and delete these two entries; until then they are exceptions with a reason
 * rather than leftovers. */
const ALLOWED = new Map<string, string>([
  ["sdn/wizards/ZoneWizardPicker.tsx", "hover-only: dark:hover:border-accent-500 / dark:hover:bg-accent-950"],
  ["guest/GuestEgoView.tsx", "hover-only: dark:hover:border-accent-600"],
]);

/** Matches `dark:` applied to an accent utility, including through another
 * variant (`dark:hover:text-accent-400`). */
const DARK_ACCENT = /\bdark:(?:[a-z-]+:)*(?:text|bg|border|ring|outline|fill|stroke|from|to|via)-accent-/g;

/** Comments are stripped before scanning. Without this the guard fails on
 * EmptyIllustration.tsx, whose doc comment QUOTES the old
 * `text-accent-600 dark:text-accent-400` to explain what T-4214 removed — and
 * on this file, which does the same. A guard that cannot tell code from prose
 * about code reports the documentation as the defect. NoticeStack's
 * interpolation guard made exactly this mistake and had to be fixed the same
 * way; twice is a pattern worth naming. */
function stripComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/.*$/gm, "$1");
}

function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      sourceFiles(full, out);
    } else if (/\.tsx?$/.test(entry) && !entry.includes(".test.")) {
      out.push(full);
    }
  }
  return out;
}

describe("accent roles stay adopted (T-4214 AC5)", () => {
  it("has no `dark:` accent utility outside the allowlist", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(SRC)) {
      const rel = relative(SRC, file);
      const hits = stripComments(readFileSync(file, "utf8")).match(DARK_ACCENT);
      if (hits === null) continue;
      if (ALLOWED.has(rel)) continue;
      offenders.push(`${rel}: ${[...new Set(hits)].join(", ")}`);
    }
    expect(
      offenders,
      "Use --color-accent-fg / -soft / -border (they re-point per theme) instead of a `dark:` step, " +
        "or add an allowlist entry in this file with the reason.",
    ).toEqual([]);
  });

  it("keeps the allowlist honest — every entry still has a `dark:` accent in it", () => {
    // An allowlist that outlives its entries is a lie about what the codebase
    // contains, and the next reader would take it as evidence the exception
    // is still needed. Cleaned up by failing, not by hoping someone notices.
    for (const [rel, reason] of ALLOWED) {
      const hits = stripComments(readFileSync(join(SRC, rel), "utf8")).match(DARK_ACCENT);
      expect(hits, `${rel} is allowlisted (${reason}) but no longer has one — remove the entry`).not.toBeNull();
    }
  });
});
