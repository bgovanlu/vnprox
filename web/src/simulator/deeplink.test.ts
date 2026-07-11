import { describe, expect, it } from "vitest";
import type { SimBlockingRule, SimResolvedEndpoint } from "../api/types";
import { blockingRuleDeepLinkPath, blockingRuleGuestRef } from "./deeplink";

const src: SimResolvedEndpoint = { kind: "guest-nic", guest: "guest:pve1:100", node: "pve1" };
const dst: SimResolvedEndpoint = { kind: "guest-nic", guest: "guest:pve2:101", node: "pve2" };

function rule(overrides: Partial<SimBlockingRule>): SimBlockingRule {
  return {
    enforcementPoint: "dest-guest-in",
    rulesetRef: "",
    origin: "guest",
    direction: "in",
    action: "DROP",
    pos: 0,
    rule: { pos: 0, enabled: true, direction: "in", action: "DROP" },
    ...overrides,
  };
}

describe("blockingRuleGuestRef", () => {
  it("uses the destination's guest ref for a dest-guest-in block", () => {
    expect(blockingRuleGuestRef(rule({ enforcementPoint: "dest-guest-in" }), src, dst)).toBe("guest:pve2:101");
  });

  it("uses the source's guest ref for a source-guest-out block", () => {
    expect(blockingRuleGuestRef(rule({ enforcementPoint: "source-guest-out" }), src, dst)).toBe("guest:pve1:100");
  });

  it("ignores rulesetRef entirely, even when the engine did populate it (cluster/group origin)", () => {
    const r = rule({ enforcementPoint: "dest-guest-in", origin: "cluster", rulesetRef: "fw-ruleset::cluster" });
    expect(blockingRuleGuestRef(r, src, dst)).toBe("guest:pve2:101");
  });

  it("returns undefined if the named endpoint carries no guest ref (should not happen in practice)", () => {
    const ipDst: SimResolvedEndpoint = { kind: "ip", ip: "10.0.0.5" };
    expect(blockingRuleGuestRef(rule({ enforcementPoint: "dest-guest-in" }), src, ipDst)).toBeUndefined();
  });
});

describe("blockingRuleDeepLinkPath", () => {
  it("builds the scope=guest deep link with pos/origin, matching ruleDeepLinkPath's contract", () => {
    const path = blockingRuleDeepLinkPath(rule({ pos: 5, origin: "guest" }), "guest:pve1:301");
    expect(path).toBe("/firewall?scope=guest&ref=guest%3Apve1%3A301&pos=5&origin=guest");
  });

  it("includes group when the origin is a security group", () => {
    const path = blockingRuleDeepLinkPath(rule({ origin: "group", groupName: "webservers" }), "guest:pve1:100");
    expect(path).toContain("group=webservers");
  });
});
