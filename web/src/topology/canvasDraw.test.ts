// canvasDraw.ts's own doc comment explains why the module as a whole isn't
// unit-tested directly (it needs a real CanvasRenderingContext2D, which
// jsdom lacks). `pulseAlphaForPhase` (T-905's drift/mgmt "pulse") is the one
// piece of drawScene's logic that's plain math with no canvas dependency,
// so it's exhaustively Vitest-able on its own — this is also the function
// TopologyCanvasV2.tsx's reduced-motion gating relies on staying static
// (phase pinned at 0) when `prefers-reduced-motion: reduce` disables the
// pulse interval (see TopologyCanvasV2.tsx's own doc comment on that
// effect).
import { describe, expect, it } from "vitest";
import { pulseAlphaForPhase } from "./canvasDraw";

describe("pulseAlphaForPhase", () => {
  // Note: the reduced-motion/no-drift static fallback is `pulseAlpha: 1`
  // passed to drawScene directly (TopologyCanvasV2.tsx skips calling this
  // function at all in that case) — NOT `pulseAlphaForPhase(0)`, which sits
  // at the bottom of the breathing range (0.55), not 1. Asserted directly
  // against the formula's own low/high points instead.
  it("reaches its floor (0.55) at phase 0 and its ceiling (1) at a quarter-period", () => {
    const period = 1400;
    expect(pulseAlphaForPhase(0, period)).toBeCloseTo(0.55, 5);
    expect(pulseAlphaForPhase(period / 2, period)).toBeCloseTo(1, 5);
  });

  it("stays within the [0.55, 1] breathing range across a full period", () => {
    const period = 1400;
    for (let ms = 0; ms < period; ms += 37) {
      const alpha = pulseAlphaForPhase(ms, period);
      expect(alpha).toBeGreaterThanOrEqual(0.55 - 1e-9);
      expect(alpha).toBeLessThanOrEqual(1 + 1e-9);
    }
  });

  it("is periodic — phase and phase+period produce the same alpha", () => {
    const period = 1400;
    expect(pulseAlphaForPhase(300, period)).toBeCloseTo(pulseAlphaForPhase(300 + period, period), 5);
  });

  it("handles a negative phase (defensive — Date.now() deltas are never negative in practice)", () => {
    expect(() => pulseAlphaForPhase(-50, 1400)).not.toThrow();
    expect(pulseAlphaForPhase(-50, 1400)).toBeGreaterThanOrEqual(0.55 - 1e-9);
  });
});
