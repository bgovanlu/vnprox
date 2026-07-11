import { describe, expect, it } from "vitest";
import { guestRefFromFwRulesetRef, locateFwRulesetRef } from "./refs";

describe("guestRefFromFwRulesetRef", () => {
  it("derives the guest ref from a guest-scope fw ruleset ref", () => {
    expect(guestRefFromFwRulesetRef("fw-ruleset:pve1:guest/qemu/100")).toBe("guest:pve1:100");
  });

  it("returns undefined for a cluster-scope ref", () => {
    expect(guestRefFromFwRulesetRef("fw-ruleset::cluster")).toBeUndefined();
  });

  it("returns undefined for a node-scope ref", () => {
    expect(guestRefFromFwRulesetRef("fw-ruleset:pve1:node")).toBeUndefined();
  });

  it("returns undefined for a malformed ref", () => {
    expect(guestRefFromFwRulesetRef("not-a-ref")).toBeUndefined();
  });
});

describe("locateFwRulesetRef", () => {
  it("locates a cluster ruleset", () => {
    expect(locateFwRulesetRef("fw-ruleset::cluster")).toEqual({ scope: "cluster" });
  });
  it("locates a node ruleset", () => {
    expect(locateFwRulesetRef("fw-ruleset:pve1:node")).toEqual({ scope: "node", node: "pve1" });
  });
  it("locates a guest ruleset", () => {
    expect(locateFwRulesetRef("fw-ruleset:pve1:guest/qemu/100")).toEqual({ scope: "guest", node: "pve1", guestRef: "guest:pve1:100" });
  });
});
