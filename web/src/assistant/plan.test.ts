// SPDX-License-Identifier: Apache-2.0

// T-2808: the tool planner. A model never picks a tool here — these rules
// do, from the question's own words and the caller's own capabilities.
import { describe, expect, it } from "vitest";
import { planTools } from "./plan";
import { permittedTools, hasCapAnywhere, ASSISTANT_TOOLS } from "./tools";
import type { Capabilities } from "../api/types";

const ALL: Capabilities = {
  netRead: true,
  netWrite: true,
  sdnRead: true,
  sdnWrite: true,
  fwRead: true,
  fwWrite: true,
  guestNet: true,
  audit: true,
  capture: true,
};

const everything = permittedTools({ pve1: ALL });

function names(question: string, permitted = everything): string[] {
  return planTools(question, permitted).map((c) => c.tool);
}

describe("planTools", () => {
  it("always reads topology and findings", () => {
    expect(names("what is going on?")).toEqual(["topology.get", "findings.list"]);
  });

  it("adds flows for a traffic question and ipam for a subnet question", () => {
    expect(names("what traffic is on vmbr0?")).toContain("flows.query");
    expect(names("which subnets are nearly full?")).toContain("ipam.subnets.list");
    expect(names("which subnets are nearly full?")).not.toContain("flows.query");
  });

  it("plans the diagnosis ladder only when the question names a ref", () => {
    expect(names("why is bridge:pve1:vmbr0 down?")).toContain("diagnose.run");
    expect(names("why is everything down?")).not.toContain("diagnose.run");
    const planned = planTools("why is bridge:pve1:vmbr0 down?", everything).find((c) => c.tool === "diagnose.run");
    expect(planned).toEqual({ tool: "diagnose.run", targetRef: "bridge:pve1:vmbr0" });
  });

  it("plans the path simulator only when the question supplies two addresses", () => {
    expect(names("can 10.0.0.1 reach 10.0.0.2?")).toContain("simulate.path");
    expect(names("can this guest reach the internet?")).not.toContain("simulate.path");
    const planned = planTools("can 10.0.0.1 reach 10.0.0.2?", everything).find((c) => c.tool === "simulate.path");
    expect(planned).toEqual({ tool: "simulate.path", src: "10.0.0.1", dst: "10.0.0.2" });
  });

  it("never plans a tool the caller is not permitted", () => {
    const noSdn: Capabilities = { ...ALL, sdnRead: false };
    const permitted = permittedTools({ pve1: noSdn });
    expect(permitted).not.toContain("ipam.subnets.list");
    expect(names("which subnets are nearly full?", permitted)).not.toContain("ipam.subnets.list");
  });

  it("plans nothing at all for a caller with no read capability", () => {
    const nothing: Capabilities = { ...ALL, netRead: false, sdnRead: false };
    expect(names("why is bridge:pve1:vmbr0 down?", permittedTools({ pve1: nothing }))).toEqual([]);
  });
});

describe("capability mapping", () => {
  it("each tool's declared capability is one GET /auth/me actually reports", () => {
    const caps: Capabilities = { ...ALL };
    for (const tool of ASSISTANT_TOOLS) {
      expect(Object.keys(caps)).toContain(tool.requiredCap);
    }
  });

  it("hasCapAnywhere is true when any node grants it and false when none does", () => {
    expect(hasCapAnywhere({ a: { ...ALL, sdnRead: false }, b: { ...ALL } }, "sdnRead")).toBe(true);
    expect(hasCapAnywhere({ a: { ...ALL, sdnRead: false } }, "sdnRead")).toBe(false);
    expect(hasCapAnywhere({}, "netRead")).toBe(false);
  });
});
