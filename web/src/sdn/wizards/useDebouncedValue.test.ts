// SPDX-License-Identifier: Apache-2.0

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PREVIEW_DEBOUNCE_MS, useDebouncedValue } from "./useDebouncedValue";

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("useDebouncedValue", () => {
  it("holds the initial value immediately, with no debounce delay on mount", () => {
    const { result } = renderHook(() => useDebouncedValue("a", PREVIEW_DEBOUNCE_MS));
    expect(result.current).toBe("a");
  });

  it("does not update before the debounce window elapses", () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, PREVIEW_DEBOUNCE_MS), {
      initialProps: { v: "a" },
    });
    rerender({ v: "b" });
    act(() => {
      vi.advanceTimersByTime(PREVIEW_DEBOUNCE_MS - 1);
    });
    expect(result.current).toBe("a");
  });

  it("updates exactly once, PREVIEW_DEBOUNCE_MS after the last change in a rapid burst", () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, PREVIEW_DEBOUNCE_MS), {
      initialProps: { v: "a" },
    });
    // A rapid burst of param changes, each well inside the debounce window
    // — mirrors a user typing/adjusting several wizard fields quickly.
    for (const v of ["b", "c", "d", "e"]) {
      act(() => {
        vi.advanceTimersByTime(10);
      });
      rerender({ v });
    }
    expect(result.current).toBe("a"); // still unsettled — burst was inside the window

    act(() => {
      vi.advanceTimersByTime(PREVIEW_DEBOUNCE_MS);
    });
    expect(result.current).toBe("e"); // settles to the last value, once
  });

  it("commits <100ms after the last change, satisfying the perceived-latency budget", () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, PREVIEW_DEBOUNCE_MS), {
      initialProps: { v: 0 },
    });
    rerender({ v: 1 });
    act(() => {
      vi.advanceTimersByTime(99);
    });
    expect(result.current).toBe(1);
    expect(PREVIEW_DEBOUNCE_MS).toBeLessThan(100);
  });
});
