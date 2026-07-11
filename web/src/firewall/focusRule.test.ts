import { describe, expect, it } from "vitest";
import { ruleDeepLinkPath } from "../fwlog/deeplink";
import { blockingRuleDeepLinkPath } from "../simulator/deeplink";
import type { SimBlockingRule } from "../api/types";
import { parseFirewallDeepLink } from "./focusRule";

function queryOf(path: string): string {
  return path.split("?")[1] ?? "";
}

describe("parseFirewallDeepLink", () => {
  it("parses T-505's log-correlation deep link (ruleDeepLinkPath)", () => {
    const path = ruleDeepLinkPath({ guestRef: "guest:pve1:102", origin: "guest", pos: 0 });
    const parsed = parseFirewallDeepLink(queryOf(path));
    expect(parsed).toEqual({
      scope: "guest",
      ref: "guest:pve1:102",
      focusRule: { pos: 0, origin: "guest", groupName: undefined },
    });
  });

  it("parses T-504's simulator blocking-rule deep link (blockingRuleDeepLinkPath)", () => {
    const blockingRule: SimBlockingRule = {
      enforcementPoint: "dest-guest-in",
      rulesetRef: "guest:pve1:100",
      origin: "group",
      groupName: "webservers",
      direction: "in",
      action: "DROP",
      pos: 3,
      rule: { pos: 3, enabled: true, direction: "in", action: "DROP" },
    };
    const path = blockingRuleDeepLinkPath(blockingRule, "guest:pve1:100");
    const parsed = parseFirewallDeepLink(queryOf(path));
    expect(parsed).toEqual({
      scope: "guest",
      ref: "guest:pve1:100",
      focusRule: { pos: 3, origin: "group", groupName: "webservers" },
    });
  });

  it("degrades gracefully when pos/origin are missing (scope+ref alone still parse)", () => {
    expect(parseFirewallDeepLink("scope=guest&ref=guest%3Apve1%3A100")).toEqual({
      scope: "guest",
      ref: "guest:pve1:100",
    });
  });

  it("degrades gracefully for a malformed pos", () => {
    expect(parseFirewallDeepLink("scope=guest&ref=guest:pve1:100&pos=not-a-number&origin=guest")).toEqual({
      scope: "guest",
      ref: "guest:pve1:100",
    });
  });

  it("degrades gracefully for an invalid origin (e.g. 'default', which never names a real rule)", () => {
    expect(parseFirewallDeepLink("scope=guest&ref=guest:pve1:100&pos=0&origin=default")).toEqual({
      scope: "guest",
      ref: "guest:pve1:100",
    });
  });

  it("returns an empty object for no params at all", () => {
    expect(parseFirewallDeepLink("")).toEqual({});
  });
});
