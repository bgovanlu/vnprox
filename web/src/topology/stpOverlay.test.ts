// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
  isStpBlockingEdge,
  isStpRootNode,
  stpBadgeLabel,
  stpPortRole,
  stpPortState,
  STP_ROOT_BADGE,
} from "./stpOverlay";

describe("isStpRootNode", () => {
  it("is true when the badge list carries stp-root", () => {
    expect(isStpRootNode(["ovs", STP_ROOT_BADGE])).toBe(true);
  });
  it("is false for an ordinary bridge with no stp-root badge", () => {
    expect(isStpRootNode(["vlans=10-20"])).toBe(false);
  });
  it("is false for an empty badge list", () => {
    expect(isStpRootNode([])).toBe(false);
  });
});

describe("stpPortRole", () => {
  it("parses a well-formed stp-role= token", () => {
    expect(stpPortRole(["stp-state=blocking", "stp-role=blocking"])).toBe("blocking");
  });
  it("returns undefined when no stp-role= token is present", () => {
    expect(stpPortRole(["active", "mii-down"])).toBeUndefined();
  });
});

describe("stpPortState", () => {
  it("parses a well-formed stp-state= token", () => {
    expect(stpPortState(["stp-role=root", "stp-state=forwarding"])).toBe("forwarding");
  });
  it("returns undefined when no stp-state= token is present", () => {
    expect(stpPortState([])).toBeUndefined();
  });
});

describe("isStpBlockingEdge", () => {
  it("is true exactly when the role is blocking — the loop-hunt signal", () => {
    expect(isStpBlockingEdge(["stp-role=blocking"])).toBe(true);
  });
  it("is false for root/designated/disabled roles", () => {
    expect(isStpBlockingEdge(["stp-role=root"])).toBe(false);
    expect(isStpBlockingEdge(["stp-role=designated"])).toBe(false);
    expect(isStpBlockingEdge(["stp-role=disabled"])).toBe(false);
  });
  it("is false when no STP badge is present at all", () => {
    expect(isStpBlockingEdge(["active"])).toBe(false);
  });
});

describe("stpBadgeLabel", () => {
  it("humanizes stp-root", () => {
    expect(stpBadgeLabel(STP_ROOT_BADGE)).toBe("STP root");
  });
  it("humanizes stp-role=", () => {
    expect(stpBadgeLabel("stp-role=blocking")).toBe("STP blocking");
  });
  it("humanizes stp-state= to the bare state word", () => {
    expect(stpBadgeLabel("stp-state=forwarding")).toBe("forwarding");
  });
  it("returns undefined for any other badge vocabulary word", () => {
    expect(stpBadgeLabel("mode=802.3ad")).toBeUndefined();
    expect(stpBadgeLabel("active")).toBeUndefined();
  });
});
