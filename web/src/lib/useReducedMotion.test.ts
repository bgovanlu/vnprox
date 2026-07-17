// T-905 acceptance criterion 5: "a Vitest test mocking
// `prefers-reduced-motion: reduce` asserts pan/zoom easing and pulse
// animations are disabled". jsdom has no real `matchMedia` implementation
// (unlike every real browser lib.dom.d.ts assumes), so each test installs
// a minimal fake covering exactly the surface useReducedMotion.ts reads
// (`.matches` + `addEventListener`/`removeEventListener` for "change").
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { motionConfig, prefersReducedMotion, useReducedMotion } from "./useReducedMotion";

function fakeMatchMedia(initialMatches: boolean) {
  let matches = initialMatches;
  const listeners = new Set<(e: MediaQueryListEvent) => void>();
  const mql: Partial<MediaQueryList> & { matches: boolean } = {
    get matches() {
      return matches;
    },
    media: "(prefers-reduced-motion: reduce)",
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

describe("prefersReducedMotion (one-shot read)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("is false when matchMedia is unavailable (jsdom default / SSR)", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(prefersReducedMotion()).toBe(false);
  });

  it("reflects a mocked prefers-reduced-motion: reduce match", () => {
    const { matchMedia } = fakeMatchMedia(true);
    vi.stubGlobal("matchMedia", matchMedia);
    expect(prefersReducedMotion()).toBe(true);
    expect(matchMedia).toHaveBeenCalledWith("(prefers-reduced-motion: reduce)");
  });

  it("is false when the media query does not match", () => {
    const { matchMedia } = fakeMatchMedia(false);
    vi.stubGlobal("matchMedia", matchMedia);
    expect(prefersReducedMotion()).toBe(false);
  });
});

describe("useReducedMotion (reactive hook)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("initializes from the current match and updates on a change event", () => {
    const { matchMedia, setMatches } = fakeMatchMedia(false);
    vi.stubGlobal("matchMedia", matchMedia);

    const { result } = renderHook(() => useReducedMotion());
    expect(result.current).toBe(false);

    act(() => {
      setMatches(true);
    });
    expect(result.current).toBe(true);
  });
});

describe("motionConfig — the named motion budget every consumer reads (AC5)", () => {
  it("full motion when reduced motion is off", () => {
    expect(motionConfig(false)).toEqual({ fitDurationMs: 500, pulseEnabled: true, transitionsEnabled: true });
  });

  it("disables pan/zoom easing (fitDurationMs) and pulse/transition animations when reduced motion is on", () => {
    expect(motionConfig(true)).toEqual({ fitDurationMs: 0, pulseEnabled: false, transitionsEnabled: false });
  });
});
