// The topology map's height floor, guarded at the source.
//
// TopologyPage.tsx's map container is the only `flex-1` child of a
// fixed-height (`h-full`) flex column whose other children are banners that
// grow with the cluster — staleness (one entry per stale source per node),
// unref findings (one per down service), the LLDP notice, the over-cap
// notice, the diff and preview strips. With `min-h-0`, that container
// absorbs every shortfall and can resolve to ZERO height, at which point
// the map is not small: it is gone, and Playwright reports the canvas as
// `hidden`. That is the failure quarantined as T-2505-followup-01; the
// geometry dump that identified it measured the map at 139px of a 796px
// page, with 385px of it taken by one uncapped banner.
//
// Why a source-level assertion rather than a rendered one: jsdom does not
// run Tailwind, so `getComputedStyle` on the rendered container returns the
// raw class list rather than a resolved height — a render test could only
// assert the same string this does, with more machinery in between. And
// TopologyPage itself is too entangled in routing, session and query state
// to mount cheaply. `index.css.test.ts` sets the precedent for asserting a
// CSS fact this suite cannot resolve, and for the same reason.
//
// What this actually protects against is a plausible, well-meaning edit:
// `min-h-0` is the correct idiom for a scrollable flex child almost
// everywhere else in this codebase, so a future refactor "fixing" this one
// to match would reintroduce a bug whose symptom is a blank map under load.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// `__dirname` rather than `import.meta.url` — under jsdom the latter is an
// http: URL, not a file: one (see index.css.test.ts / i18nCoverage.test.ts).
const source = readFileSync(resolve(__dirname, "TopologyPage.tsx"), "utf8");

/** The className string on the element carrying `ref={entityContainerRef}`
 * — the map container. Located by that ref rather than by a class fragment,
 * so the test still finds it if the styling changes. */
function mapContainerClasses(): string {
  const idx = source.indexOf("ref={entityContainerRef}");
  expect(idx, "the map container's ref={entityContainerRef} was not found").toBeGreaterThan(-1);
  const after = source.slice(idx, idx + 4000);
  const match = /className="([^"]+)"/.exec(after);
  expect(match, "no className found on the map container element").not.toBeNull();
  return match?.[1] ?? "";
}

describe("topology map container height floor", () => {
  it("declares a minimum height, so flex-1 can never resolve to zero", () => {
    expect(mapContainerClasses()).toMatch(/\bmin-h-\[/);
  });

  it("does not use a bare min-h-0, which is what let the map be squeezed away", () => {
    // The specific regression. `min-h-0` here means "this child may shrink
    // below its content", which for the only flexible child of a
    // fixed-height column means "may shrink to nothing".
    //
    // Matched on whole class tokens rather than by substring: a naive
    // /\bmin-h-0\b/ also matches the deliberate `print:min-h-0` variant
    // beside it, which is the opposite of a regression.
    expect(mapContainerClasses().split(/\s+/)).not.toContain("min-h-0");
  });

  it("stays a flex child, so it still fills the space it does have", () => {
    // The floor must not have been bought by giving the container a fixed
    // height: on a roomy viewport with no banners, the map should still
    // expand to fill the column.
    expect(mapContainerClasses()).toMatch(/\bflex-1\b/);
  });

  it("drops the floor when printing, where there is no viewport to fill", () => {
    // A 22rem floor on a printed page would push a short map onto its own
    // sheet. The print variants next to it (print:h-auto) exist for the
    // same reason.
    expect(mapContainerClasses()).toMatch(/\bprint:min-h-0\b/);
  });
});
