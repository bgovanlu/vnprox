// T-909: same fake-matchMedia harness useReducedMotion.test.ts established
// (jsdom has no real matchMedia), applied to the narrow-viewport signal.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { isNarrowViewport, NARROW_VIEWPORT_QUERY, useNarrowViewport } from "./useNarrowViewport";

function fakeMatchMedia(initialMatches: boolean) {
  let matches = initialMatches;
  const listeners = new Set<(e: MediaQueryListEvent) => void>();
  const mql: Partial<MediaQueryList> & { matches: boolean } = {
    get matches() {
      return matches;
    },
    media: NARROW_VIEWPORT_QUERY,
    addEventListener: (_type: string, listener: EventListenerOrEventListenerObject) => {
      listeners.add(listener as (e: MediaQueryListEvent) => void);
    },
    removeEventListener: (_type: string, listener: EventListenerOrEventListenerObject) => {
      listeners.delete(listener as (e: MediaQueryListEvent) => void);
    },
  };
  return {
    matchMedia: vi.fn().mockReturnValue(mql),
    setMatches: (next: boolean) => {
      matches = next;
      for (const l of listeners) l({ matches: next } as MediaQueryListEvent);
    },
  };
}

describe("isNarrowViewport (one-shot read)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("is false when matchMedia is unavailable (jsdom default / SSR)", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(isNarrowViewport()).toBe(false);
  });

  it("reflects a mocked phone-width match", () => {
    const { matchMedia } = fakeMatchMedia(true);
    vi.stubGlobal("matchMedia", matchMedia);
    expect(isNarrowViewport()).toBe(true);
    expect(matchMedia).toHaveBeenCalledWith(NARROW_VIEWPORT_QUERY);
  });

  it("is false when the media query does not match (desktop width)", () => {
    const { matchMedia } = fakeMatchMedia(false);
    vi.stubGlobal("matchMedia", matchMedia);
    expect(isNarrowViewport()).toBe(false);
  });
});

describe("useNarrowViewport (reactive hook)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("initializes from the current match and updates on a change event (resize/rotation)", () => {
    const { matchMedia, setMatches } = fakeMatchMedia(false);
    vi.stubGlobal("matchMedia", matchMedia);

    const { result } = renderHook(() => useNarrowViewport());
    expect(result.current).toBe(false);

    act(() => {
      setMatches(true);
    });
    expect(result.current).toBe(true);

    act(() => {
      setMatches(false);
    });
    expect(result.current).toBe(false);
  });

  it("starts narrow when the viewport is already phone-width on mount", () => {
    const { matchMedia } = fakeMatchMedia(true);
    vi.stubGlobal("matchMedia", matchMedia);

    const { result } = renderHook(() => useNarrowViewport());
    expect(result.current).toBe(true);
  });
});
