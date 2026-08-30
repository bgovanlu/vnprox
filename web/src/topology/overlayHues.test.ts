// SPDX-License-Identifier: Apache-2.0

// The map's overlay literals carry comments asserting they are "distinct from
// every status/SIM_STROKE/FLOW_EDGE_COLOR/diff/recency colour already in use".
// Nothing measured that. This file does, and the claim is false: fifteen pairs
// sit under the 40deg floor the palette holds everywhere else — and one of
// them was introduced tonight, by T-4211, three commits before this file.
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
  "sim/allow": "#10b981",
  "sim/deny": "#ef4444",
  "sim/unreachable": "#f59e0b",
  "sim/indeterminate": "#8b5cf6",
  "flow": "#06b6d4",
  "flow-selected": "#0e7490",
  "stp-blocking": "#c2410c",
  "blast-radius": "#c026d3",
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
      // Flow edges reading as "selected" / "informational". Unexamined rather
      // than chosen — the flow overlay predates the status scale.
      "accent(base) | flow",
      "accent(base) | flow-selected",
      // Deliberate: T-4204 made `info` converge with the accent (0.2deg).
      "accent(base) | status/info",
      // **Introduced tonight**, by T-4211's re-hue, at 2.9deg. See the header.
      "accent(demo) | blast-radius",
      "accent(demo) | sim/indeterminate",
      "blast-radius | sim/indeterminate",
      // One ramp, two shades — 7.9deg apart is the point of them.
      "flow | flow-selected",
      "flow | status/info",
      "flow-selected | status/info",
      // Deliberate: SIM_STROKE maps a simulator verdict onto severity, and
      // EntityEdge.tsx says so — a `deny` SHOULD look like an error.
      "sim/allow | status/ok",
      "sim/deny | status/critical",
      // T-3901 chose a burnt orange deliberately near, but not equal to,
      // "down"/"deny", so a cut port never reads as either.
      "sim/deny | stp-blocking",
      "sim/unreachable | status/degraded",
      "sim/unreachable | stp-blocking",
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
    const expected: [string, string][] = [
      ["flow", `const FLOW_EDGE_COLOR = "${OVERLAY.flow}"`],
      ["flow-selected", `const FLOW_EDGE_SELECTED_COLOR = "${OVERLAY["flow-selected"]}"`],
      ["blast-radius", `const BLAST_RADIUS_COLOR = "${OVERLAY["blast-radius"]}"`],
    ];
    for (const [name, needle] of expected) {
      expect(draw, `${name} has moved in canvasDraw.ts — update OVERLAY above`).toContain(needle);
    }
    expect(edge, "stp-blocking has moved in EntityEdge.tsx").toContain(
      `const STP_BLOCKING_STROKE = "${OVERLAY["stp-blocking"]}"`,
    );
    for (const verdict of ["allow", "deny", "unreachable", "indeterminate"] as const) {
      expect(draw, `sim/${verdict} has moved`).toContain(`${verdict}: "${OVERLAY[`sim/${verdict}`]}"`);
    }
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
