// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { macroExpansionLabel, macroPortLabel, ruleMatchLabel } from "./format";
import type { RuleView } from "../api/types";

describe("ruleMatchLabel", () => {
  it("prefers the macro name over proto/port when present", () => {
    const rule: RuleView = { pos: 0, enabled: true, direction: "in", action: "ACCEPT", macro: "HTTP" };
    expect(ruleMatchLabel(rule)).toBe("HTTP");
  });

  it("renders proto/dport when no macro is set", () => {
    const rule: RuleView = { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "22" };
    expect(ruleMatchLabel(rule)).toBe("tcp/22");
  });

  it("includes source, dest, and iface when present", () => {
    const rule: RuleView = {
      pos: 0, enabled: true, direction: "in", action: "ACCEPT",
      proto: "tcp", dport: "80", source: "office_net", dest: "+blocklist", iface: "net0",
    };
    expect(ruleMatchLabel(rule)).toBe("tcp/80 from office_net to +blocklist on net0");
  });

  it("falls back to 'any' when nothing is specified", () => {
    const rule: RuleView = { pos: 0, enabled: true, direction: "in", action: "ACCEPT" };
    expect(ruleMatchLabel(rule)).toBe("any");
  });
});

describe("macroPortLabel", () => {
  it("renders proto/port", () => {
    expect(macroPortLabel({ proto: "tcp", dport: "80" })).toBe("tcp/80");
  });
  it("renders just the protocol when there is no port (e.g. icmp)", () => {
    expect(macroPortLabel({ proto: "icmp" })).toBe("icmp");
  });
});

describe("macroExpansionLabel", () => {
  it("renders the expansion preview", () => {
    expect(macroExpansionLabel("HTTP", [{ proto: "tcp", dport: "80" }])).toBe("HTTP → tcp/80");
  });

  it("joins multiple port pairs", () => {
    expect(macroExpansionLabel("DNS", [{ proto: "udp", dport: "53" }, { proto: "tcp", dport: "53" }])).toBe(
      "DNS → udp/53, tcp/53",
    );
  });

  it("labels an unknown macro honestly rather than showing nothing", () => {
    expect(macroExpansionLabel("NotARealMacro", undefined)).toBe("NotARealMacro (unknown macro)");
    expect(macroExpansionLabel("NotARealMacro", [])).toBe("NotARealMacro (unknown macro)");
  });
});
