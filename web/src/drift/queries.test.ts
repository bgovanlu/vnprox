import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { DRIFT_QUERY_KEY, applyDriftChanged, isDriftChangedEvent } from "./queries";
import type { DriftFinding } from "../api/types";

describe("isDriftChangedEvent", () => {
  it("accepts a well-formed drift.changed payload", () => {
    expect(isDriftChangedEvent({ event: "drift.changed", count: 3 })).toBe(true);
    expect(isDriftChangedEvent({ event: "drift.changed", count: 0 })).toBe(true);
  });

  it("rejects other event names even with a count field", () => {
    expect(isDriftChangedEvent({ event: "topology.delta", count: 3 })).toBe(false);
  });

  it("rejects a payload missing/mistyped count", () => {
    expect(isDriftChangedEvent({ event: "drift.changed" })).toBe(false);
    expect(isDriftChangedEvent({ event: "drift.changed", count: "3" })).toBe(false);
  });
});

describe("applyDriftChanged", () => {
  it("invalidates the drift query", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const seed: DriftFinding[] = [
      { id: "a", check: "mtu_consistency", severity: "warning", detail: "x", nodes: [], fixable: false },
    ];
    queryClient.setQueryData(DRIFT_QUERY_KEY, seed);

    applyDriftChanged(queryClient);

    expect(queryClient.getQueryState(DRIFT_QUERY_KEY)?.isInvalidated).toBe(true);
  });
});
