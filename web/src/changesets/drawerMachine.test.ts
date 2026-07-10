import { describe, expect, it } from "vitest";
import type { Changeset } from "../api/types";
import { canApply, canReview, computeDrawerView, isDraftEditable } from "./drawerMachine";

function makeChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "cs1",
    title: "Untitled draft",
    author: "root@pam",
    status: "draft",
    ops: [],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

const op: Changeset["ops"][number] = { op: "bridge.create", target: "bridge:pve1:vmbr1", params: {} };

describe("computeDrawerView", () => {
  it("is empty with no active changeset", () => {
    expect(computeDrawerView(undefined, false)).toBe("empty");
  });

  it("is drafting for a draft/validated changeset when review hasn't been requested", () => {
    expect(computeDrawerView(makeChangeset({ status: "draft" }), false)).toBe("drafting");
    expect(computeDrawerView(makeChangeset({ status: "validated" }), false)).toBe("drafting");
  });

  it("is reviewing for a draft/validated changeset once review is requested", () => {
    expect(computeDrawerView(makeChangeset({ status: "draft" }), true)).toBe("reviewing");
    expect(computeDrawerView(makeChangeset({ status: "validated" }), true)).toBe("reviewing");
  });

  it("ignores reviewRequested once the server status has moved past draft/validated", () => {
    expect(computeDrawerView(makeChangeset({ status: "applying" }), true)).toBe("applying");
    expect(computeDrawerView(makeChangeset({ status: "awaiting_confirm" }), true)).toBe("awaiting_confirm");
  });

  it.each(["committed", "rolled_back", "failed", "discarded"] as const)(
    "is done for the terminal status %s regardless of reviewRequested",
    (status) => {
      expect(computeDrawerView(makeChangeset({ status }), false)).toBe("done");
      expect(computeDrawerView(makeChangeset({ status }), true)).toBe("done");
    },
  );
});

describe("isDraftEditable", () => {
  it("is true only for draft/validated", () => {
    expect(isDraftEditable(undefined)).toBe(false);
    expect(isDraftEditable(makeChangeset({ status: "draft" }))).toBe(true);
    expect(isDraftEditable(makeChangeset({ status: "validated" }))).toBe(true);
    for (const status of ["applying", "awaiting_confirm", "committed", "rolled_back", "failed", "discarded"] as const) {
      expect(isDraftEditable(makeChangeset({ status }))).toBe(false);
    }
  });
});

describe("canReview", () => {
  it("requires at least one op", () => {
    expect(canReview(undefined)).toBe(false);
    expect(canReview(makeChangeset({ ops: [] }))).toBe(false);
    expect(canReview(makeChangeset({ ops: [op] }))).toBe(true);
  });
});

describe("canApply", () => {
  it("is false with no ops", () => {
    expect(canApply(makeChangeset({ ops: [] }), false)).toBe(false);
  });

  it("is false when any finding is error-severity, regardless of the warnings checkbox", () => {
    const cs = makeChangeset({ ops: [op], findings: [{ severity: "error", code: "x", message: "bad" }] });
    expect(canApply(cs, false)).toBe(false);
    expect(canApply(cs, true)).toBe(false);
  });

  it("requires the warnings checkbox when only warning-severity findings exist", () => {
    const cs = makeChangeset({ ops: [op], findings: [{ severity: "warning", code: "x", message: "meh" }] });
    expect(canApply(cs, false)).toBe(false);
    expect(canApply(cs, true)).toBe(true);
  });

  it("is true with clean findings", () => {
    expect(canApply(makeChangeset({ ops: [op], findings: [] }), false)).toBe(true);
  });
});
