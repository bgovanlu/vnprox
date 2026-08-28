// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { Op } from "../api/types";
import { applyFixToOps } from "./findingFields";

describe("applyFixToOps", () => {
  const ops: Op[] = [
    { op: "bridge.create", target: "bridge:pve1:vmbr1", params: { mtu: 99999 } },
    { op: "vlan.create", target: "vlan:pve1:vmbr0.30", params: { parent: "vmbr0", vid: 30 } },
  ];

  it("replaces exactly the op sharing the fix op's (type, target) pair", () => {
    const fix: Op[] = [{ op: "bridge.create", target: "bridge:pve1:vmbr1", params: { mtu: 9216 } }];
    const patched = applyFixToOps(ops, fix);
    expect(patched[0]).toEqual(fix[0]);
    expect(patched[1]).toBe(ops[1]); // untouched, same reference
  });

  it("does not touch an op whose target matches but whose type differs", () => {
    const fix: Op[] = [{ op: "bridge.update", target: "bridge:pve1:vmbr1", params: { mtu: 9216 } }];
    expect(applyFixToOps(ops, fix)).toEqual(ops);
  });

  it("is a no-op for an empty fix", () => {
    expect(applyFixToOps(ops, [])).toEqual(ops);
  });
});
