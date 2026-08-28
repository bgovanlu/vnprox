// SPDX-License-Identifier: Apache-2.0

// T-4015: three-state (up/down/unknown) derivation tests for the general
// WireGuard management surface — mirrors interconnects.test.ts's fixture
// shape and the same "state cannot be read is not down" case T-3909
// established, generalized to every tunnel rather than only
// federation-linked ones.
import { describe, expect, it } from "vitest";
import type { WireGuardTunnel } from "../api/types";
import { wgTunnelState } from "./wgTunnelState";

const NOW = 1_800_000_000; // arbitrary fixed instant, mirrors wgEdgeStatus.test.ts's convention
const STALE_SEC = 5 * 60;

function tunnel(overrides: Partial<WireGuardTunnel> = {}): WireGuardTunnel {
  return {
    id: "tun-1",
    node: "pve1",
    ifName: "wg0",
    publicKey: "PUBKEY",
    addresses: ["10.99.0.1/30"],
    peers: [],
    status: { interfaceUp: true, peerCount: 0 },
    listenPort: 51820,
    mtu: 1420,
    ...overrides,
  };
}

describe("wgTunnelState", () => {
  it("reports 'up' when at least one peer handshaked within the stale threshold", () => {
    const t = tunnel({
      peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - 30 }],
    });
    expect(wgTunnelState(t, false, NOW)).toBe("up");
  });

  it("does not flag a peer right at the threshold boundary as down (age <= threshold is up)", () => {
    const t = tunnel({
      peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - STALE_SEC }],
    });
    expect(wgTunnelState(t, false, NOW)).toBe("up");
  });

  it("reports 'down' when every peer's handshake is past the stale threshold", () => {
    const t = tunnel({
      peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - (STALE_SEC + 60) }],
    });
    expect(wgTunnelState(t, false, NOW)).toBe("down");
  });

  it("reports 'down', not 'unknown', for a tunnel with zero peers — its state IS known, nothing is up", () => {
    const t = tunnel({ peers: [] });
    expect(wgTunnelState(t, false, NOW)).toBe("down");
  });

  it("reports 'down' for a peer with no handshake age at all (never handshaked)", () => {
    const t = tunnel({
      peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: undefined }],
    });
    expect(wgTunnelState(t, false, NOW)).toBe("down");
  });

  it("reports 'unknown' — not 'down' — when the local tunnel read hasn't resolved or failed, even for an otherwise-healthy-looking tunnel", () => {
    const t = tunnel({
      peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - 30 }],
    });
    expect(wgTunnelState(t, true, NOW)).toBe("unknown");
  });

  it("defaults nowUnix to the current instant when omitted (smoke test — no thrown error, a real verdict comes back)", () => {
    const t = tunnel({ peers: [] });
    expect(["up", "down"]).toContain(wgTunnelState(t, false));
  });
});
