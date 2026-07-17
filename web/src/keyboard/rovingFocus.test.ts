// T-903 AC4: pure ordering-logic coverage for roving arrow-key focus,
// against a small synthetic topology entity list standing in for real
// Graph-view (elk x/y) or Switch-view (DOM rect) positions — the same
// {id,x,y} shape either source produces (see useRovingFocus.ts).
import { describe, expect, it } from "vitest";
import { nextFocusId, visualAdjacencyOrder, type PositionedEntity } from "./rovingFocus";

// A 2-row, mixed-order "topology entity list": row 1 has bond0 left of
// vmbr0; row 2 (offset well past the row-band tolerance) has eno1 left of
// eno2. Declared out of visual order on purpose, so a passing test proves
// the function re-sorts by position rather than trusting input order.
const ENTITIES: PositionedEntity[] = [
  { id: "eno2", x: 220, y: 200 },
  { id: "vmbr0", x: 200, y: 0 },
  { id: "bond0", x: 0, y: 4 }, // within ROW_BAND_PX of vmbr0 — same row
  { id: "eno1", x: 20, y: 204 },
];

describe("visualAdjacencyOrder", () => {
  it("orders top-to-bottom by row band, then left-to-right within each row", () => {
    expect(visualAdjacencyOrder(ENTITIES).map((e) => e.id)).toEqual(["bond0", "vmbr0", "eno1", "eno2"]);
  });

  it("is stable for entities already given in visual order", () => {
    const inOrder: PositionedEntity[] = [
      { id: "a", x: 0, y: 0 },
      { id: "b", x: 100, y: 0 },
      { id: "c", x: 0, y: 100 },
    ];
    expect(visualAdjacencyOrder(inOrder).map((e) => e.id)).toEqual(["a", "b", "c"]);
  });

  it("returns an empty array for no entities", () => {
    expect(visualAdjacencyOrder([])).toEqual([]);
  });
});

describe("nextFocusId", () => {
  it("advances in visual-adjacency order on ArrowRight/ArrowDown from the current entity", () => {
    expect(nextFocusId(ENTITIES, "bond0", "right")).toBe("vmbr0");
    expect(nextFocusId(ENTITIES, "vmbr0", "down")).toBe("eno1");
    expect(nextFocusId(ENTITIES, "eno1", "right")).toBe("eno2");
  });

  it("moves backward through visual-adjacency order on ArrowLeft/ArrowUp", () => {
    expect(nextFocusId(ENTITIES, "eno2", "left")).toBe("eno1");
    expect(nextFocusId(ENTITIES, "eno1", "up")).toBe("vmbr0");
  });

  it("wraps around at either end rather than dead-ending", () => {
    expect(nextFocusId(ENTITIES, "eno2", "right")).toBe("bond0");
    expect(nextFocusId(ENTITIES, "bond0", "left")).toBe("eno2");
  });

  it("focuses the first entity in visual order when nothing is focused yet and the direction is forward", () => {
    expect(nextFocusId(ENTITIES, undefined, "down")).toBe("bond0");
  });

  it("focuses the last entity in visual order when nothing is focused yet and the direction is backward", () => {
    expect(nextFocusId(ENTITIES, undefined, "up")).toBe("eno2");
  });

  it("returns undefined when there are no entities at all", () => {
    expect(nextFocusId([], undefined, "right")).toBeUndefined();
    expect(nextFocusId([], "anything", "right")).toBeUndefined();
  });
});
