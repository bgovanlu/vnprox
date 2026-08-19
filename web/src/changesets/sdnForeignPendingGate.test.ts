import { describe, expect, it } from "vitest";
import { describeSdnPendingEntry, sdnForeignPendingBlocksApply, sdnPendingSetKey } from "./sdnForeignPendingGate";
import type { SdnPendingEntry } from "../api/types";

function entry(overrides: Partial<SdnPendingEntry>): SdnPendingEntry {
  return { kind: "zone", id: "foreignz", state: "new", ...overrides };
}

describe("sdnForeignPendingBlocksApply", () => {
  it("is false when entries is undefined (query still loading/errored)", () => {
    expect(sdnForeignPendingBlocksApply(undefined, undefined)).toBe(false);
  });

  it("is false when there is nothing foreign pending", () => {
    expect(sdnForeignPendingBlocksApply([], undefined)).toBe(false);
  });

  it("is true when foreign pending state exists and nothing has been acknowledged", () => {
    expect(sdnForeignPendingBlocksApply([entry({})], undefined)).toBe(true);
  });

  it("is false once the exact current set has been acknowledged", () => {
    const entries = [entry({})];
    expect(sdnForeignPendingBlocksApply(entries, sdnPendingSetKey(entries))).toBe(false);
  });

  it("is order-independent: acknowledging in one order still covers the other", () => {
    const a = entry({ id: "a" });
    const b = entry({ id: "b" });
    const ackedKey = sdnPendingSetKey([b, a]);
    expect(sdnForeignPendingBlocksApply([a, b], ackedKey)).toBe(false);
  });

  it("is true again once a NEW foreign entry appears that the prior acknowledgement didn't cover", () => {
    const ackedKey = sdnPendingSetKey([entry({ id: "foreignz1" })]);
    const current = [entry({ id: "foreignz1" }), entry({ id: "foreignz2" })];
    expect(sdnForeignPendingBlocksApply(current, ackedKey)).toBe(true);
  });

  it("is true again when a field changed on an already-acknowledged entry", () => {
    const ackedKey = sdnPendingSetKey([entry({ fields: { mtu: 1500 } })]);
    expect(sdnForeignPendingBlocksApply([entry({ fields: { mtu: 1600 } })], ackedKey)).toBe(true);
  });

  it("stays false when the acknowledged set covers MORE than what's currently pending", () => {
    const ackedKey = sdnPendingSetKey([entry({ id: "a" }), entry({ id: "b" })]);
    expect(sdnForeignPendingBlocksApply([entry({ id: "a" })], ackedKey)).toBe(false);
  });
});

describe("describeSdnPendingEntry", () => {
  it("renders kind, id, and state", () => {
    expect(describeSdnPendingEntry(entry({ kind: "vnet", id: "vnet1", state: "changed" }))).toBe(
      "vnet vnet1 (changed)",
    );
  });
});
