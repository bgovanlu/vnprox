// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { wgPeerEdgePaint, worstWgPeerPaint, WG_HANDSHAKE_STALE_THRESHOLD_SEC } from "./wgEdgeStatus";

const NOW = 1_800_000_000; // arbitrary fixed instant

describe("wgPeerEdgePaint", () => {
  it("paints a recently-handshaken, non-drifted peer healthy (ok, no drift badge)", () => {
    const paint = wgPeerEdgePaint({ lastHandshakeUnix: NOW - 30, endpointDrifted: false }, NOW);
    expect(paint.status).toBe("ok");
    expect(paint.badges).toEqual(["wg"]);
  });

  it("paints a peer whose handshake is past the stale threshold as degraded (amber)", () => {
    const paint = wgPeerEdgePaint({ lastHandshakeUnix: NOW - (WG_HANDSHAKE_STALE_THRESHOLD_SEC + 60), endpointDrifted: false }, NOW);
    expect(paint.status).toBe("degraded");
    expect(paint.badges).toEqual(["wg"]);
  });

  it("does not flag a peer right at the threshold boundary as stale (strictly greater-than, mirrors backend age > threshold)", () => {
    const paint = wgPeerEdgePaint({ lastHandshakeUnix: NOW - WG_HANDSHAKE_STALE_THRESHOLD_SEC, endpointDrifted: false }, NOW);
    expect(paint.status).toBe("ok");
  });

  it("never flags a peer that has no handshake age at all as stale — a freshly-created tunnel renders healthy immediately", () => {
    const paint = wgPeerEdgePaint({ lastHandshakeUnix: undefined, endpointDrifted: false }, NOW);
    expect(paint.status).toBe("ok");
    expect(paint.badges).toEqual(["wg"]);

    const zeroPaint = wgPeerEdgePaint({ lastHandshakeUnix: 0, endpointDrifted: false }, NOW);
    expect(zeroPaint.status).toBe("ok");
  });

  it("adds the 'drift' badge (dashed rendering) for an endpoint-drifted peer, independent of handshake recency", () => {
    const paint = wgPeerEdgePaint({ lastHandshakeUnix: NOW - 10, endpointDrifted: true }, NOW);
    expect(paint.status).toBe("ok");
    expect(paint.badges).toEqual(["wg", "drift"]);
  });

  it("can combine stale + drift (independent axes, not mutually exclusive)", () => {
    const paint = wgPeerEdgePaint(
      { lastHandshakeUnix: NOW - (WG_HANDSHAKE_STALE_THRESHOLD_SEC + 60), endpointDrifted: true },
      NOW,
    );
    expect(paint.status).toBe("degraded");
    expect(paint.badges).toEqual(["wg", "drift"]);
  });
});

describe("worstWgPeerPaint", () => {
  it("rolls up to ok/no-drift when every peer is healthy", () => {
    const paints = [wgPeerEdgePaint({ lastHandshakeUnix: NOW, endpointDrifted: false }, NOW)];
    expect(worstWgPeerPaint(paints)).toEqual({ status: "ok", badges: ["wg"] });
  });

  it("rolls up to degraded if any peer is stale, and carries drift if any peer drifted", () => {
    const healthy = wgPeerEdgePaint({ lastHandshakeUnix: NOW, endpointDrifted: false }, NOW);
    const stale = wgPeerEdgePaint({ lastHandshakeUnix: NOW - 10_000, endpointDrifted: false }, NOW);
    const drifted = wgPeerEdgePaint({ lastHandshakeUnix: NOW, endpointDrifted: true }, NOW);
    expect(worstWgPeerPaint([healthy, stale, drifted])).toEqual({ status: "degraded", badges: ["wg", "drift"] });
  });
});
