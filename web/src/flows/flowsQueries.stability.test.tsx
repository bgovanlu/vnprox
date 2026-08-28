// SPDX-License-Identifier: Apache-2.0

// T-2003-bug-01 root-cause regression.
//
// `useLiveFlowRecords(false)` used to `return { records: [], ... }` — a new
// array identity on every single render. TopologyPage passes that value
// straight into HistoryTimeline's `liveFlowRecords` prop, which is a
// dependency of the effect that calls back into TopologyPage's own
// `setPlayback`. New identity per render therefore meant: render -> effect
// -> setState -> render, with no exit, for the entire time the Topology
// Graph view was mounted.
//
// The user-visible symptom was not a slow map: React Router v7 wraps
// location updates in `startTransition`, and a transition cannot commit
// while the tree it is rendering keeps invalidating itself. Clicking any
// nav-rail link changed the URL and left Topology on screen — permanently.
// The end-to-end proof lives in web/e2e/nav-after-inspector.spec.ts; this
// pins the one-line cause so it cannot come back quietly.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { useLiveFlowRecords } from "./flowsQueries";

// Hoisted, not built per render: a fresh client each render would remount
// the hook and hide exactly the identity churn under test.
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("useLiveFlowRecords identity stability", () => {
  it("hands back the SAME empty records array on every render while disabled", () => {
    const { result, rerender } = renderHook(() => useLiveFlowRecords(false), { wrapper });

    const first = result.current.records;
    expect(first).toEqual([]);

    for (let i = 0; i < 5; i++) rerender();

    // toBe, not toEqual: two distinct empty arrays are `toEqual`, and that
    // is precisely the bug this test exists to catch.
    expect(result.current.records).toBe(first);
  });
});
