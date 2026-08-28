// SPDX-License-Identifier: Apache-2.0

// The script is data, so these are assertions about the data — including
// the two the card and its dependency make load-bearing:
//
//   * six surfaces, because "the six surfaces the datasheet leads with" is
//     a count, not a suggestion;
//   * no POST-backed screen, because a public demo refuses every mutating
//     METHOD at the edge and a tour that walked a visitor onto the path
//     simulator would walk them onto a 403 (T-2801-followup-01).
import { describe, expect, it } from "vitest";

import { TOUR_STEPS, TOUR_STEP_IDS, tourStep } from "./tourScript";

describe("the tour script", () => {
  it("covers six surfaces", () => {
    expect(TOUR_STEPS).toHaveLength(6);
  });

  it("has unique, non-empty step ids", () => {
    expect(new Set(TOUR_STEP_IDS).size).toBe(TOUR_STEPS.length);
    for (const step of TOUR_STEPS) {
      expect(step.id).not.toBe("");
      expect(step.id).not.toBe("done");
    }
  });

  it("never sends a visitor to a screen whose data is POST-backed", () => {
    // POST /simulate/path and POST /diagnose are read surfaces with
    // mutating methods; the edge refuses both. If a future step lands on
    // one of these routes, the tour would end in a 403 and this fails
    // rather than the visitor discovering it.
    const refusedAtTheEdge = ["/diagnose", "/simulate"];
    for (const step of TOUR_STEPS) {
      expect(refusedAtTheEdge, `tour step ${step.id} routes to ${step.route}`).not.toContain(step.route);
    }
  });

  it("routes only to real app routes", () => {
    for (const step of TOUR_STEPS) {
      expect(step.route.startsWith("/")).toBe(true);
    }
    expect(TOUR_STEPS.map((s) => s.route)).toEqual(["/topology", "/ports", "/tools", "/flows", "/sdn", "/history"]);
  });

  it("says something on every stop", () => {
    for (const step of TOUR_STEPS) {
      expect(step.title.length).toBeGreaterThan(0);
      expect(step.body.length).toBeGreaterThan(80);
      expect(step.lookFor.length).toBeGreaterThan(20);
    }
  });

  // The coupling to T-2801's fixture is deliberate: a script that never
  // names an entity from internal/demo/dataset/cluster.yaml is one that was
  // written without looking at the data it describes, and renaming a
  // fixture node should break something.
  it("names entities that exist in the demo dataset", () => {
    const prose = TOUR_STEPS.map((s) => `${s.body} ${s.lookFor}`).join(" ");
    for (const entity of ["pve1", "pve2", "pve3", "vmbr0", "vnet100", "vnet200", "app01", "cache01", "db01"]) {
      expect(prose, `the tour never mentions ${entity}`).toContain(entity);
    }
  });

  it("resolves a step by id, and nothing else", () => {
    expect(tourStep("map")?.route).toBe("/topology");
    expect(tourStep("done")).toBeUndefined();
    expect(tourStep("")).toBeUndefined();
  });
});
