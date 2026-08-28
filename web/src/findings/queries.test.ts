// SPDX-License-Identifier: Apache-2.0

import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { FINDINGS_QUERY_KEY, applyFindingsChanged, isFindingsChangedEvent } from "./queries";
import type { StreamFinding } from "../api/types";

describe("isFindingsChangedEvent", () => {
  it("accepts a well-formed findings.changed payload", () => {
    expect(isFindingsChangedEvent({ event: "findings.changed", count: 3 })).toBe(true);
    expect(isFindingsChangedEvent({ event: "findings.changed", count: 0 })).toBe(true);
  });

  it("rejects other event names even with a count field", () => {
    expect(isFindingsChangedEvent({ event: "drift.changed", count: 3 })).toBe(false);
    expect(isFindingsChangedEvent({ event: "topology.delta", count: 3 })).toBe(false);
  });

  it("rejects a payload missing/mistyped count", () => {
    expect(isFindingsChangedEvent({ event: "findings.changed" })).toBe(false);
    expect(isFindingsChangedEvent({ event: "findings.changed", count: "3" })).toBe(false);
  });
});

describe("applyFindingsChanged", () => {
  it("invalidates the findings query", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const seed: StreamFinding[] = [
      { id: "health:1", source: "health", check: "bond_slave_down", severity: "warning", detail: "x", nodes: [], fixable: false },
    ];
    queryClient.setQueryData(FINDINGS_QUERY_KEY, seed);

    applyFindingsChanged(queryClient);

    expect(queryClient.getQueryState(FINDINGS_QUERY_KEY)?.isInvalidated).toBe(true);
  });
});
