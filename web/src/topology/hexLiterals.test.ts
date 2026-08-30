// SPDX-License-Identifier: Apache-2.0

// T-4301 criterion 2, restated so it can actually be met.
//
// The criterion was "zero hex literals in web/src/topology/**". That card
// closed recording why it cannot be met as written: several of the remaining
// literals are *nominal* scales — added/removed/changed, an age bucket that
// is not a severity — for which hue is the correct channel and the status
// scale would state something false. Chasing the number to zero would mean
// breaking a design to satisfy a count.
//
// But "cannot be met" was doing too much work, because it also covered
// literals that were simply the accent drawn in a dead colour. The
// hover ring, the selection outline and the minimap's viewport rectangle were
// Tailwind blue-500/600 — the **pre-T-4201 brand blue** that T-4301's own text
// says the product "no longer uses anywhere else". In demo mode, where
// `html.demo` moves every accent step to another hue family, the map drew a
// blue selection ring while every other selected control on the page went
// purple. Those were stragglers, not exceptions, and the audit that classified
// the leftovers never separated the two.
//
// So the criterion becomes: **every hex literal left is a named exception with
// a recorded reason.** That is checkable, it does not demand a design be
// broken to satisfy it, and — unlike a count — it fails when someone adds a
// literal for a reason nobody has argued for, which is the case the original
// criterion actually cared about.
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const DIR = resolve(__dirname);

/** Every file allowed to hold a colour literal, and why. A file not listed
 * here may hold none. */
const ALLOWED: Record<string, string> = {
  "diffOverlay.ts":
    "added/removed/changed is a NOMINAL set — hue is the correct channel for it, " +
    "and the status scale would state something false (a removed entity is not 'critical'). " +
    "T-4303 ruled on this and exempted it deliberately.",
  "recencyOverlay.ts":
    "an age bucket is ORDINAL and is not severity — a thing changed a minute ago is not " +
    "'critical'. Same T-4303 exemption, same reason.",
  "canvasPalette.ts":
    "the UNRESOLVED sentinel (deliberately a colour no design would choose, so an " +
    "unresolved token is loud rather than plausible) and the mgmt badge pair, which marks a " +
    "management/corosync path — not a status and not the brand, and the design language has " +
    "nothing for 'these are different KINDS of thing' (T-4302: no such scale can exist at the " +
    "status scale's own separation).",
  "simVerdict.ts":
    "sim/indeterminate only. The other three verdicts resolve --color-status-* after T-4306; " +
    "'could not decide' has no status equivalent, so it keeps a private hue.",
  "EntityEdge.tsx":
    "STP_BLOCKING_STROKE. T-3901 chose a burnt orange deliberately adjacent to 'down'/'deny' " +
    "so a cut port reads as related to them without being either; the dash is what separates " +
    "them. A token would lose the adjacency that is the point.",
  "MetricsTab.tsx":
    "recharts series strokes. Not the map — a time-series chart, where the series colours are " +
    "a nominal set with the same no-categorical-scale problem, and recharts takes colours as " +
    "props rather than reading CSS custom properties.",
};

/** Matches a quoted 6-digit hex. Deliberately requires the quotes so prose in
 * a doc comment (`this was "#2563eb"`) is still caught — a stale value in a
 * comment is exactly the drift this repo has been bitten by, so the test
 * strips comments explicitly rather than letting them through by accident. */
const HEX = /"#[0-9a-fA-F]{6}"/g;

function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

function sourceFiles(): string[] {
  return readdirSync(DIR, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => `${e.parentPath.slice(DIR.length + 1)}${e.parentPath.length > DIR.length ? "/" : ""}${e.name}`)
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f));
}

describe("colour literals in the topology renderer (T-4301 criterion 2)", () => {
  it("holds no colour literal outside the named exceptions", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles()) {
      if (ALLOWED[file] !== undefined) continue;
      const code = stripComments(readFileSync(resolve(DIR, file), "utf8"));
      const hits = code.match(HEX);
      if (hits) offenders.push(`${file}: ${[...new Set(hits)].join(", ")}`);
    }
    expect(
      offenders,
      "a colour literal in a file with no recorded reason. Resolve it through canvasPalette.ts's " +
        "ROLE table, or add the file to ALLOWED above WITH the argument for why a token would be " +
        "wrong — an entry with no reason is the thing this test exists to prevent.",
    ).toEqual([]);
  });

  // The file the card counted 35 literals in. It is the one that draws the map
  // itself, so "the map is on tokens" is true or false here specifically.
  it("canvasDraw.ts draws entirely from the palette", () => {
    const code = stripComments(readFileSync(resolve(DIR, "canvasDraw.ts"), "utf8"));
    expect(code.match(HEX)).toBeNull();
  });

  // Guard the guard: every allowlisted file must actually still contain a
  // literal. An entry that stops being true is an exception nobody needs,
  // and a stale allowlist quietly widens what this test permits.
  it("has no stale exception", () => {
    for (const [file, reason] of Object.entries(ALLOWED)) {
      const code = stripComments(readFileSync(resolve(DIR, file), "utf8"));
      expect(code.match(HEX), `${file} no longer holds a literal — drop it from ALLOWED (${reason})`).not.toBeNull();
    }
  });

  // The specific value this card's tail removed. It is called out by name
  // because it is not a generic literal: it is a colour the product
  // deliberately stopped using, and its reappearance would mean the map had
  // drifted back off the accent.
  it("nothing draws in the pre-T-4201 brand blue", () => {
    for (const file of sourceFiles()) {
      const code = stripComments(readFileSync(resolve(DIR, file), "utf8"));
      // diffOverlay's `removed` badge is blue-600 by coincidence of value, not
      // of meaning — it is a nominal diff colour, exempt above.
      if (file === "diffOverlay.ts") continue;
      expect(code, `${file} draws in the retired brand blue`).not.toMatch(/"#(3b82f6|2563eb)"/i);
    }
  });
});
