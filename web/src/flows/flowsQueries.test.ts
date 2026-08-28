// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { isFlowBatchEvent } from "./flowsQueries";

describe("isFlowBatchEvent", () => {
  it("accepts a well-formed flow.batch payload", () => {
    expect(isFlowBatchEvent({ event: "flow.batch", entries: [], droppedTotal: 0 })).toBe(true);
  });

  it("rejects other event names even with matching-shaped fields", () => {
    expect(isFlowBatchEvent({ event: "firewall.log.batch", entries: [], droppedTotal: 0 })).toBe(false);
  });

  it("rejects a payload with a missing/mistyped entries or droppedTotal", () => {
    expect(isFlowBatchEvent({ event: "flow.batch", droppedTotal: 0 })).toBe(false);
    expect(isFlowBatchEvent({ event: "flow.batch", entries: "not-an-array", droppedTotal: 0 })).toBe(false);
    expect(isFlowBatchEvent({ event: "flow.batch", entries: [] })).toBe(false);
    expect(isFlowBatchEvent({ event: "flow.batch", entries: [], droppedTotal: "3" })).toBe(false);
  });
});
