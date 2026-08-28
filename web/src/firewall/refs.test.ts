// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { guestRefFromFwRulesetRef, locateFwRulesetRef, vnetRefFromFwRulesetRef } from "./refs";

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

describe("vnetRefFromFwRulesetRef", () => {
  it("derives the vnet ref from a vnet-scope fw ruleset ref", () => {
    expect(vnetRefFromFwRulesetRef("fw-ruleset::vnet/zone1/vnet1")).toBe("sdn-vnet::zone1/vnet1");
  });

  it("returns undefined for a cluster-scope ref", () => {
    expect(vnetRefFromFwRulesetRef("fw-ruleset::cluster")).toBeUndefined();
  });

  it("returns undefined for a guest-scope ref", () => {
    expect(vnetRefFromFwRulesetRef("fw-ruleset:pve1:guest/qemu/100")).toBeUndefined();
  });

  it("returns undefined for a malformed ref", () => {
    expect(vnetRefFromFwRulesetRef("not-a-ref")).toBeUndefined();
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
  it("locates a vnet ruleset", () => {
    expect(locateFwRulesetRef("fw-ruleset::vnet/zone1/vnet1")).toEqual({ scope: "vnet", vnetRef: "sdn-vnet::zone1/vnet1" });
  });
});
