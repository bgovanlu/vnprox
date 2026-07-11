import { describe, expect, it } from "vitest";
import type { FwLogRuleRef } from "../api/types";
import { ruleDeepLinkPath } from "./deeplink";

describe("ruleDeepLinkPath", () => {
  it("encodes scope, ref, pos, and origin", () => {
    const rule: FwLogRuleRef = { guestRef: "guest:pve1:100", origin: "guest", pos: 3 };
    const path = ruleDeepLinkPath(rule);
    const url = new URL(path, "http://example.test");
    expect(url.pathname).toBe("/firewall");
    expect(url.searchParams.get("scope")).toBe("guest");
    expect(url.searchParams.get("ref")).toBe("guest:pve1:100");
    expect(url.searchParams.get("pos")).toBe("3");
    expect(url.searchParams.get("origin")).toBe("guest");
    expect(url.searchParams.has("group")).toBe(false);
  });

  it("includes the group name when origin is group", () => {
    const rule: FwLogRuleRef = { guestRef: "guest:pve1:100", origin: "group", groupName: "webservers", pos: 0 };
    const url = new URL(ruleDeepLinkPath(rule), "http://example.test");
    expect(url.searchParams.get("origin")).toBe("group");
    expect(url.searchParams.get("group")).toBe("webservers");
  });

  it("URL-encodes a ref containing special characters", () => {
    const rule: FwLogRuleRef = { guestRef: "guest:pve node:100", origin: "cluster", pos: 1 };
    const path = ruleDeepLinkPath(rule);
    expect(path).not.toContain(" "); // must be percent-encoded, not a raw space
    const url = new URL(path, "http://example.test");
    expect(url.searchParams.get("ref")).toBe("guest:pve node:100");
  });
});
