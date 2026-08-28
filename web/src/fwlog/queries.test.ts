// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { isFwLogBatchEvent } from "./queries";

describe("isFwLogBatchEvent", () => {
  it("accepts a well-formed firewall.log.batch payload", () => {
    expect(isFwLogBatchEvent({ event: "firewall.log.batch", entries: [], droppedTotal: 0 })).toBe(true);
  });

  it("rejects other event names even with matching-shaped fields", () => {
    expect(isFwLogBatchEvent({ event: "topology.delta", entries: [], droppedTotal: 0 })).toBe(false);
  });

  it("rejects a payload with a missing/mistyped entries or droppedTotal", () => {
    expect(isFwLogBatchEvent({ event: "firewall.log.batch", droppedTotal: 0 })).toBe(false);
    expect(isFwLogBatchEvent({ event: "firewall.log.batch", entries: "not-an-array", droppedTotal: 0 })).toBe(false);
    expect(isFwLogBatchEvent({ event: "firewall.log.batch", entries: [] })).toBe(false);
    expect(isFwLogBatchEvent({ event: "firewall.log.batch", entries: [], droppedTotal: "3" })).toBe(false);
  });
});
