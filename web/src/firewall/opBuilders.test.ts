// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { RuleView } from "../api/types";
import {
  buildFwAliasDeleteOp,
  buildFwGroupDeleteOp,
  buildFwIpsetDeleteOp,
  buildFwOptionsUpdateOp,
  buildFwRuleCreateOp,
  buildFwRuleDeleteOp,
  buildFwRuleMoveOp,
  buildFwRuleToggleOp,
  buildFwRuleUpdateOp,
  type RuleFormValues,
} from "./opBuilders";

const target = "fw-ruleset:pve1:guest/qemu/100";

function emptyForm(): RuleFormValues {
  return { direction: "in", action: "ACCEPT", proto: "", source: "", dest: "", sport: "", dport: "", iface: "", macro: "", log: "", comment: "", enabled: true };
}

describe("buildFwRuleCreateOp", () => {
  it("builds a create op with the builder row's fields", () => {
    const op = buildFwRuleCreateOp(target, 2, { ...emptyForm(), macro: "HTTP", comment: "web" });
    expect(op).toEqual({
      op: "fw.rule.create",
      target,
      params: { direction: "in", action: "ACCEPT", macro: "HTTP", comment: "web", pos: 2, enabled: true },
    });
  });
});

describe("buildFwRuleUpdateOp", () => {
  it("only includes fields that changed from the initial form", () => {
    const initial = emptyForm();
    const form = { ...initial, comment: "changed", dport: "443" };
    const op = buildFwRuleUpdateOp(target, 1, initial, form);
    expect(op).toEqual({ op: "fw.rule.update", target, params: { pos: 1, comment: "changed", dport: "443" } });
  });

  it("produces an empty patch (besides pos) when nothing changed", () => {
    const initial = emptyForm();
    const op = buildFwRuleUpdateOp(target, 0, initial, { ...initial });
    expect(op).toEqual({ op: "fw.rule.update", target, params: { pos: 0 } });
  });
});

describe("buildFwRuleToggleOp", () => {
  it("builds a minimal enabled-only patch", () => {
    expect(buildFwRuleToggleOp(target, 3, false)).toEqual({
      op: "fw.rule.update", target, params: { pos: 3, enabled: false },
    });
  });
});

describe("buildFwRuleDeleteOp", () => {
  it("builds a delete op", () => {
    expect(buildFwRuleDeleteOp(target, 2)).toEqual({ op: "fw.rule.delete", target, params: { pos: 2 } });
  });
});

describe("buildFwRuleMoveOp", () => {
  it("carries the dragged rule's own fields as Expect", () => {
    const rule: RuleView = { pos: 0, enabled: true, direction: "in", action: "ACCEPT", comment: "ssh", proto: "tcp", dport: "22" };
    const op = buildFwRuleMoveOp(target, rule, 3);
    expect(op).toEqual({
      op: "fw.rule.move",
      target,
      params: {
        fromPos: 0,
        toPos: 3,
        expect: { direction: "in", action: "ACCEPT", comment: "ssh", proto: "tcp", dport: "22", enabled: true },
      },
    });
  });
});

describe("buildFwOptionsUpdateOp", () => {
  it("builds an options update op", () => {
    expect(buildFwOptionsUpdateOp(target, { enabled: false })).toEqual({
      op: "fw.options.update", target, params: { enabled: false },
    });
  });
});

describe("object delete op builders", () => {
  it("builds fw.alias.delete", () => {
    expect(buildFwAliasDeleteOp(target, "office")).toEqual({ op: "fw.alias.delete", target, params: { name: "office" } });
  });
  it("builds fw.ipset.delete", () => {
    expect(buildFwIpsetDeleteOp(target, "blocklist")).toEqual({ op: "fw.ipset.delete", target, params: { name: "blocklist" } });
  });
  it("builds fw.group.delete", () => {
    expect(buildFwGroupDeleteOp(target, "web")).toEqual({ op: "fw.group.delete", target, params: { name: "web" } });
  });
});
