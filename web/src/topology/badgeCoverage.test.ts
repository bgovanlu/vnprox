// T-3406-followup-02's "revised scope for the second guard": the opacity
// defect that recurred four times (findingBadges.ts's `findingBadgeClass`
// doc comment has the full history) was never visible in the source — a
// translucent badge background is a perfectly good class string and only
// becomes a contrast defect once something dark sits behind it, which is a
// fact about the render tree, not the class list. A source-only scan
// (index.css.test.ts / mapContainerFloor.test.ts's idiom) therefore cannot
// decide it; the only honest guard is that `web/e2e/a11y.spec.ts`'s axe
// sweep actually renders every surface a badge chip can appear on, so a
// regression is caught the way this one eventually was — by axe, on a real
// page.
//
// This is a coverage-SWEEP guard, not a coverage-CONTENT one: it does not
// (cannot, from Vitest/jsdom) re-run axe itself. It asserts the narrower,
// checkable thing the card's own "consolidate the duplication" fix makes
// possible — that the census of "surfaces which composite a badge from the
// shared vocabulary" (findingBadges.ts's `findingBadgeClass`/
// `mgmtBadgeClass`/`MGMT_BADGE_CLASS`, now written exactly once) is
// source-derived rather than remembered, and that every entry in it maps to
// a real, named test in a11y.spec.ts — mirroring src/help/coverage.test.ts's
// forward-census + registration-integrity pattern (route → topic there,
// badge-composing file → axe test here), the same "coverage.test.ts
// precedent" web/e2e/a11y.spec.ts's own T-3406 sweep comment cites for why
// a hand-maintained list is the wrong shape for this kind of claim.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = resolve(__dirname, "..");
const E2E = resolve(__dirname, "..", "..", "e2e");

function read(absolute: string): string {
  return readFileSync(absolute, "utf8");
}

function rel(full: string): string {
  return full.slice(SRC.length + 1);
}

/** Every non-test `.tsx` module under web/src, as absolute paths. Mirrors
 * coverage.test.ts's `sourceFiles()` walk. */
function sourceFiles(): string[] {
  const found: string[] = [];
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        if (entry !== "node_modules") walk(full);
        continue;
      }
      if (!entry.endsWith(".tsx") || /\.test\.tsx?$/.test(entry)) continue;
      found.push(full);
    }
  };
  walk(SRC);
  return found;
}

/** Every call site of one of findingBadges.ts's shared badge-class helpers —
 * the census of "surfaces which composite a badge from the consolidated
 * vocabulary" this guard exists to keep honest. `canvasDraw.ts` deliberately
 * does not appear here: a `<canvas>` has no Tailwind classes to defer to, so
 * it mirrors the same red/amber/slate hue steps as literal hex (its own
 * doc comment explains why), which this scan — keyed on the Tailwind-class
 * helpers themselves — correctly does not and cannot reach. That surface's
 * coverage is the golden-parity render tests canvasDraw.ts's own header
 * comment names, not this one. */
const BADGE_CLASS_CALL = /\b(?:findingBadgeClass|mgmtBadgeClass)\s*\(|\bMGMT_BADGE_CLASS\b/;

function badgeCompositingFiles(): string[] {
  return sourceFiles()
    .filter((full) => BADGE_CLASS_CALL.test(read(full)))
    .map(rel)
    .sort();
}

/** Maps each badge-compositing file to a substring of a real `test("axe: …")`
 * name in a11y.spec.ts that puts it on screen. Hand-maintained (unlike the
 * census above, which is source-derived) because "which axe test renders
 * this component" is not mechanically recoverable from either file alone —
 * exactly like coverage.test.ts's ROUTE_HELP table, whose integrity is
 * likewise checked below rather than assumed. */
const BADGE_SURFACE_COVERAGE: Readonly<Record<string, string>> = {
  // The chassis header badge row (line ~628) is the exact surface T-3406-
  // followup-02's defect was found on — the dark name plate under the
  // Switch view's default scan.
  "topology/SwitchFaceplate.tsx": "axe: Topology (Switch view, the default)",
  // Graph view v1 renders EntityNode.tsx directly (v2 canvas paints the
  // same data via canvasDraw.ts's own literal-hex path, out of this scan's
  // scope per BADGE_CLASS_CALL's doc comment above).
  "topology/EntityNode.tsx": "axe: Topology (Graph view, v1)",
  // UnrefFindingsBanner mounts in TopologyPage.tsx above BOTH the Switch and
  // Graph views (its own header comment: "so the two views can never
  // contradict each other about it") — it renders only when a finding's
  // Refs are empty, which the mock cluster does not guarantee on every run,
  // so this cannot claim the banner's badge is *always* on screen when axe
  // runs. What it can and does claim: there is no route or toggle a
  // banner-carrying finding would need that the existing Switch-view scan
  // does not already cover — the banner has no surface of its own to miss.
  "topology/UnrefFindingsBanner.tsx": "axe: Topology (Switch view, the default)",
};

describe("badge-composing surfaces are reached by the axe sweep (T-3406-followup-02)", () => {
  it("the source scan itself finds a plausible, non-empty set, including the two known sites", () => {
    // Anti-vacuity (coverage.test.ts's own precedent): a regex that stops
    // matching the real call sites would make every check below pass over
    // an empty set and certify nothing.
    const files = badgeCompositingFiles();
    expect(files.length).toBeGreaterThanOrEqual(3);
    expect(files).toContain("topology/SwitchFaceplate.tsx");
    expect(files).toContain("topology/EntityNode.tsx");
  });

  it("gives every badge-compositing file a coverage-table entry", () => {
    // Forward check: a new call site of findingBadgeClass/mgmtBadgeClass
    // that nobody wired into BADGE_SURFACE_COVERAGE fails here by name,
    // rather than silently shipping unreached — the same failure mode as
    // T-3006's panel census, applied to this narrower vocabulary.
    const missing = badgeCompositingFiles().filter((f) => !(f in BADGE_SURFACE_COVERAGE));
    expect(missing).toEqual([]);
  });

  it("has no coverage-table entry for a file that no longer composites a badge", () => {
    // The other direction — a stale entry staying green after its call site
    // was deleted would be a coverage claim about nothing.
    const files = new Set(badgeCompositingFiles());
    const stale = Object.keys(BADGE_SURFACE_COVERAGE).filter((f) => !files.has(f));
    expect(stale).toEqual([]);
  });

  it("resolves every coverage-table entry to a real test in a11y.spec.ts", () => {
    const a11ySpec = read(join(E2E, "a11y.spec.ts"));
    const testNames = [...a11ySpec.matchAll(/^test\(\s*"([^"]+)"/gm)].map((m) => m[1]);
    // Anti-vacuity for the a11y.spec.ts side of the parity check.
    expect(testNames.length).toBeGreaterThanOrEqual(10);

    const dangling = Object.entries(BADGE_SURFACE_COVERAGE)
      .filter(([, testName]) => !testNames.includes(testName))
      .map(([file, testName]) => `${file} → "${testName}" (no such test in web/e2e/a11y.spec.ts)`);
    expect(dangling).toEqual([]);
  });
});
