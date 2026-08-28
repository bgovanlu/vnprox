// SPDX-License-Identifier: Apache-2.0

// T-3909: pure derivation tests for the global map's WireGuard interconnect
// edges — three states (up/down/unknown), not two, and the unreachable/
// unavailable-data case that must degrade only the affected edges.
import { describe, expect, it } from "vitest";
import type { FederationCluster } from "../../api/federation";
import type { WireGuardTunnel } from "../../api/types";
import { deriveInterconnects } from "./interconnects";

const NOW = 1_800_000_000; // arbitrary fixed instant, mirrors wgEdgeStatus.test.ts's convention
const STALE_SEC = 5 * 60;

function cluster(overrides: Partial<FederationCluster> = {}): FederationCluster {
  return {
    id: "cl-a",
    name: "east",
    apiUrl: "https://east:8006",
    status: "ok",
    addedBy: "admin@pam",
    addedAt: 0,
    ...overrides,
  };
}

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

describe("deriveInterconnects", () => {
  it("emits no edge at all for a cluster with no effective tunnel linkage", () => {
    const clusters = [cluster({ wgTunnelId: undefined })];
    expect(deriveInterconnects(clusters, [], false, NOW)).toEqual([]);
  });

  it("reports 'up' for a linked tunnel with a peer handshaked within the stale threshold", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1", wgTunnelSource: "explicit" })];
    const tunnels = [tunnel({ id: "tun-1", peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - 30 }] })];
    const result = deriveInterconnects(clusters, tunnels, false, NOW);
    expect(result).toEqual([
      { clusterId: "cl-a", clusterName: "east", tunnelId: "tun-1", tunnelSource: "explicit", state: "up" },
    ]);
  });

  it("does not flag a peer right at the threshold boundary as down (age <= threshold is up, mirrors backend's WgTunnelHasFreshHandshake)", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1" })];
    const tunnels = [tunnel({ id: "tun-1", peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - STALE_SEC }] })];
    expect(deriveInterconnects(clusters, tunnels, false, NOW)[0]?.state).toBe("up");
  });

  it("reports 'down' for a linked tunnel found locally whose only peer's handshake is past the stale threshold", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1" })];
    const tunnels = [tunnel({ id: "tun-1", peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW - (STALE_SEC + 60) }] })];
    expect(deriveInterconnects(clusters, tunnels, false, NOW)[0]?.state).toBe("down");
  });

  it("reports 'down' for a linked tunnel found locally that has never handshaked at all", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1" })];
    const tunnels = [tunnel({ id: "tun-1", peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false }] })];
    expect(deriveInterconnects(clusters, tunnels, false, NOW)[0]?.state).toBe("down");
  });

  it("reports 'down' for a linked tunnel found locally with zero configured peers", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1" })];
    const tunnels = [tunnel({ id: "tun-1", peers: [] })];
    expect(deriveInterconnects(clusters, tunnels, false, NOW)[0]?.state).toBe("down");
  });

  it("reports 'unknown' when the linked tunnel id isn't present in this node's own GET /wireguard/tunnels", () => {
    const clusters = [cluster({ wgTunnelId: "tun-missing" })];
    const tunnels = [tunnel({ id: "tun-1" })];
    expect(deriveInterconnects(clusters, tunnels, false, NOW)[0]?.state).toBe("unknown");
  });

  it("reports 'unknown' for every linked cluster when the local tunnel read itself failed — the unreachable-data case", () => {
    const clusters = [cluster({ id: "cl-a", wgTunnelId: "tun-1" }), cluster({ id: "cl-b", name: "west", wgTunnelId: "tun-2" })];
    const result = deriveInterconnects(clusters, undefined, true, NOW);
    expect(result).toHaveLength(2);
    expect(result.every((ic) => ic.state === "unknown")).toBe(true);
  });

  it("reports 'unknown' when tunnels haven't resolved yet (still loading), distinct from an empty result set", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1" })];
    const result = deriveInterconnects(clusters, undefined, false, NOW);
    expect(result[0]?.state).toBe("unknown");
  });

  it("isolates one cluster's unresolved linkage from a sibling's healthy one — per-cluster isolation", () => {
    const clusters = [
      cluster({ id: "cl-a", name: "east", wgTunnelId: "tun-1" }),
      cluster({ id: "cl-b", name: "west", wgTunnelId: "tun-missing" }),
    ];
    const tunnels = [tunnel({ id: "tun-1", peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW }] })];
    const result = deriveInterconnects(clusters, tunnels, false, NOW);
    expect(result.find((ic) => ic.clusterId === "cl-a")?.state).toBe("up");
    expect(result.find((ic) => ic.clusterId === "cl-b")?.state).toBe("unknown");
  });

  it("defaults tunnelSource to 'explicit' when the registry omits it but a linkage id is present", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1", wgTunnelSource: undefined })];
    expect(deriveInterconnects(clusters, [tunnel({ id: "tun-1" })], false, NOW)[0]?.tunnelSource).toBe("explicit");
  });

  it("preserves a 'peer'-sourced linkage's tunnelSource", () => {
    const clusters = [cluster({ wgTunnelId: "tun-1", wgTunnelSource: "peer" })];
    expect(deriveInterconnects(clusters, [tunnel({ id: "tun-1" })], false, NOW)[0]?.tunnelSource).toBe("peer");
  });

  it("mixes linked and unlinked clusters correctly — only linked clusters produce an entry", () => {
    const clusters = [cluster({ id: "cl-a", wgTunnelId: "tun-1" }), cluster({ id: "cl-b", name: "west", wgTunnelId: undefined })];
    const tunnels = [tunnel({ id: "tun-1" })];
    const result = deriveInterconnects(clusters, tunnels, false, NOW);
    expect(result).toHaveLength(1);
    expect(result[0]?.clusterId).toBe("cl-a");
  });
});
