// SPDX-License-Identifier: Apache-2.0

// The map's overlay literals carried comments asserting they were "distinct
// from every status/SIM_STROKE/FLOW_EDGE_COLOR/diff/recency colour already in
// use". Nothing measured that. This file does, and the claims were false:
// sixteen pairs sat under the 40deg floor the palette holds everywhere else —
// and one of them was introduced by T-4211, three commits before this file.
//
// Four remain (T-4306). The other twelve are gone the only way AC1 allows:
// because the colours themselves are gone. Three sim verdicts now resolve
// `--color-status-*` rather than restating them 0.2-17deg away; the flow
// overlay resolves `--color-status-info`, since traffic is informational and
// a flow edge is findable by being the only animated thing on the map;
// `flow-selected` was deleted outright, because +1.5px of width already said
// "selected"; and the blast-radius ring went neutral, because its scrim
// already isolates the focus set.
//
// T-4301's finding, in a new place. A comment promising a property is not a
// mechanism for having it — that phase measured a hand-copied palette which
// was "kept in sync by hand" and found three of twelve values correct on the
// day they were written.
//
// **But most of these collisions are deliberate, and that matters more than
// the count.** `SIM_STROKE` maps a path-simulator verdict onto severity on
// purpose — EntityEdge.tsx says so: allow=emerald, deny=red, unreachable=amber.
// A "deny" verdict SHOULD look like an error. So this file does not assert a
// floor. It pins the exact set of pairs that are under one, which makes the
// deliberate ones explicit and makes a SEVENTEENTH fail.
//
// That distinction is not academic: T-4211 moved the demo accent to hue 320
// and put it 2.9deg from BLAST_RADIUS_COLOR, a collision that did not exist
// before. T-4211's own gate passed, because it compares the accent against
// status hues and these literals are not status. A gate that measures the
// claim would have caught it in the same commit.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const CSS = readFileSync(resolve(__dirname, "..", "index.css"), "utf8");

function blockTokens(selector: string): Map<string, string> {
  const opener = new RegExp(`^${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m");
  const found = opener.exec(CSS);
  if (found === null) throw new Error(`${selector} not found in index.css`);
  const open = CSS.indexOf("{", found.index);
  const out = new Map<string, string>();
  for (const m of CSS.slice(open + 1, CSS.indexOf("}", open)).matchAll(/(--[a-z0-9-]+)\s*:\s*(#[0-9a-f]{6})\s*;/g)) {
    const [, name, value] = m;
    if (name !== undefined && value !== undefined) out.set(name, value);
  }
  return out;
}

/** OKLCH hue, matching index.css.test.ts's own helper — the space this palette
 * is designed in. See that file for why sRGB-HSL gives the wrong answer for
 * exactly the warm pairs this table is full of. */
function hue(hex: string): number {
  const n = Number.parseInt(hex.slice(1), 16);
  const lin = (v: number): number => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  const r = lin((n >> 16) & 0xff);
  const g = lin((n >> 8) & 0xff);
  const b = lin(n & 0xff);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const a = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const bb = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  return ((((Math.atan2(bb, a) * 180) / Math.PI) % 360) + 360) % 360;
}

function separation(a: number, b: number): number {
  const d = Math.abs(a - b) % 360;
  return Math.min(d, 360 - d);
}

/** The literals, transcribed from their definitions. Transcription is exactly
 * what this repo distrusts, so `keeps this table in step with the source`
 * below re-reads each one out of the module that defines it. */
const OVERLAY = {
  // T-4306: `sim/allow`, `sim/deny` and `sim/unreachable` are gone from this
  // table because they are gone from the code — they now resolve
  // `--color-status-ok` / `-critical` / `-degraded` rather than restating them
  // 0.2-17deg away. That removes three census pairs by a DESIGN change, which
  // is the only way this list is allowed to shrink (AC1). `indeterminate`
  // stays: "could not decide" is not a health state.
  //
  // `flow` and `flow-selected` are gone for the same reason, one card later.
  // Cyan-500 sat 9deg from `info` and from the accent — the census's one
  // genuinely UNEXAMINED collision, because the overlay predates the status
  // scale. Asking what a flow edge means answered it: traffic is
  // informational, so the edge resolves `--color-status-info` instead of
  // sitting just off it. `flow-selected` is gone outright, not re-pointed:
  // selection was already carried by +1.5px of width, and the second cyan was
  // a whole hue spent restating it.
  //
  // `blast-radius` is gone because something had to leave the hue channel
  // entirely — see the arithmetic below. Its ring is neutral now; the overlay
  // scrims everything else to 0.72 alpha, so isolation does what a hue would.
  "sim/indeterminate": "#8b5cf6", // violet-500, the one verdict with no status equivalent
  "stp-blocking": "#c2410c",
} as const;

const FLOOR = 40;

describe("map overlay hues (found while closing T-4302)", () => {
  const base = blockTokens("@theme");
  const demo = new Map([...base, ...blockTokens("html.demo")]);

  const palette = new Map<string, string>(Object.entries(OVERLAY));
  for (const state of ["ok", "degraded", "critical", "info"]) {
    palette.set(`status/${state}`, base.get(`--color-status-${state}`) ?? "");
  }
  palette.set("accent(base)", base.get("--color-accent-600") ?? "");
  palette.set("accent(demo)", demo.get("--color-accent-600") ?? "");

  function collisions(): string[] {
    const names = [...palette.keys()];
    const out: string[] = [];
    for (let i = 0; i < names.length; i++) {
      for (let j = i + 1; j < names.length; j++) {
        const a = names[i] ?? "";
        const b = names[j] ?? "";
        // Pair names are sorted so the census does not depend on map
        // insertion order — otherwise adding a palette entry reshuffles the
        // expected list and the diff hides what actually changed.
        if (separation(hue(palette.get(a) ?? ""), hue(palette.get(b) ?? "")) < FLOOR) {
          out.push([a, b].sort().join(" | "));
        }
      }
    }
    return out.sort();
  }

  it("pins every pair under the 40deg floor, so a new one fails", () => {
    // Not a floor assertion — a census. Several entries here are the design
    // working as intended (a `deny` verdict looking like an error), and
    // asserting a floor would demand they be broken. What must not happen is
    // a SIXTEENTH appearing without anyone noticing, which is how
    // `accent(demo) | blast-radius` got here.
    expect(collisions()).toEqual([
      // Deliberate: T-4204 made `info` converge with the accent (0.2deg).
      "accent(base) | status/info",
      // Not deliberate, and not yet addressed: 27.3deg. It is the last pair
      // here that nobody has ruled on. `indeterminate` keeps a private hue
      // because "could not decide" is not a health state, and the demo accent
      // has nowhere left to go — so closing it needs the same kind of answer
      // blast-radius got (leave the hue channel), not a re-hue.
      "accent(demo) | sim/indeterminate",
      // Deliberate: T-3901 chose a burnt orange deliberately near, but not
      // equal to, "down"/"deny", so a cut port never reads as either.
      "status/critical | stp-blocking",
      "status/degraded | stp-blocking",
    ]);
  });

  it("keeps this table in step with the modules that define the literals", () => {
    // The table above is a transcription, and this repo has been bitten by
    // transcription twice (T-4301's hand-copied SceneTheme, T-4204's
    // "kept in sync by hand"). So the values are re-read from source: a
    // literal edited in canvasDraw.ts without editing this file fails here
    // rather than silently making the census above describe a palette that no
    // longer exists.
    const draw = readFileSync(resolve(__dirname, "canvasDraw.ts"), "utf8");
    const edge = readFileSync(resolve(__dirname, "EntityEdge.tsx"), "utf8");
    expect(edge, "stp-blocking has moved in EntityEdge.tsx").toContain(
      `const STP_BLOCKING_STROKE = "${OVERLAY["stp-blocking"]}"`,
    );
    // T-4306: canvasDraw.ts no longer defines a colour of its own. Both of
    // its literals became SceneTheme roles, so the check that used to
    // re-read them is now the opposite assertion — that they are not there.
    // Reintroducing either as a hex would put a hue back on the map outside
    // the palette, which is precisely what this file exists to notice.
    for (const gone of ["FLOW_EDGE_COLOR", "FLOW_EDGE_SELECTED_COLOR", "BLAST_RADIUS_COLOR"]) {
      expect(draw, `${gone} is back in canvasDraw.ts — overlay colours resolve through canvasPalette now`).not.toContain(
        `const ${gone} =`,
      );
    }
    // T-4306 left exactly one sim literal, and it moved out of canvasDraw.ts
    // into the shared mapping. Re-read from there — the other three are no
    // longer literals anywhere, which is the point.
    const sim = readFileSync(resolve(__dirname, "simVerdict.ts"), "utf8");
    expect(sim, "sim/indeterminate has moved in simVerdict.ts").toContain(
      `SIM_INDETERMINATE_COLOR = "${OVERLAY["sim/indeterminate"]}"`,
    );
    expect(draw, "canvasDraw.ts should no longer hold its own verdict palette").not.toContain('allow: "#');
  });

  it("records that no fourteenth hue can clear the floor — the circle is full", () => {
    // T-4302 proved eleven hues at 40deg need 440deg of a 360deg circle and
    // moved entity KIND to shape as a result. This is the same arithmetic at
    // product scale, and it is why the census above is a census: thirteen
    // distinct hues are already spent, so the best separation ANY new colour
    // can achieve against all of them is under the floor. The next overlay
    // that wants a hue cannot have one, and should use a different channel.
    const spent = [...palette.values()].map(hue);
    let best = 0;
    for (let h = 0; h < 360; h++) best = Math.max(best, Math.min(...spent.map((s) => separation(h, s))));
    expect(best).toBeLessThan(FLOOR);
  });
});
