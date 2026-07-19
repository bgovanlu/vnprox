// T-1402 AC1: "A tunnel between two attached mock clusters renders as one
// map edge with the correct live status badge for each of T-1401's three
// states (healthy/stale-handshake/endpoint-drift)." Federation (T-1201) is
// not in this repo (see planning/reports/T-1402.md's federation-seam
// note), so the "far cluster" here is modeled as the standalone external
// endpoint every peer resolves to (wgTunnelEdges.ts's own doc comment) —
// the three live states this test asserts are identical either way, since
// they come straight from T-1401's own per-peer fields, not from anything
// federation-specific.
import { describe, expect, it } from "vitest";
import type { WireGuardTunnel } from "../api/types";
import { computeWgTunnelOverlay, wgEndpointNodeId, WG_ENDPOINT_KIND } from "./wgTunnelEdges";

const NOW = 1_800_000_000;

function tunnel(overrides: Partial<WireGuardTunnel> = {}): WireGuardTunnel {
  return {
    id: "01HWGTUNNEL0000000000000001",
    node: "pve1",
    ifName: "wg0",
    publicKey: "SRVpubKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
    addresses: ["10.10.0.1/24"],
    peers: [
      {
        publicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
        endpoint: "203.0.113.10:51820",
        allowedIps: ["10.10.0.2/32"],
        lastHandshakeUnix: NOW - 30,
        rxBytes: 1000,
        txBytes: 900,
        external: true,
        endpointDrifted: false,
      },
    ],
    status: { interfaceUp: true, peerCount: 1 },
    listenPort: 51820,
    mtu: 1420,
    ...overrides,
  };
}

const nodeIdForName = (name: string): string | undefined => (name === "pve1" ? "node:pve1:pve1" : undefined);

describe("computeWgTunnelOverlay", () => {
  it("renders a tunnel with a recent handshake as one healthy edge (ok, no drift)", () => {
    const { nodes, edges } = computeWgTunnelOverlay([tunnel()], nodeIdForName, NOW);
    expect(edges).toHaveLength(1);
    const [edge] = edges;
    expect(edge).toMatchObject({
      from: "node:pve1:pve1",
      to: wgEndpointNodeId("01HWGTUNNEL0000000000000001", "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa="),
      kind: "wg-tunnel",
      status: "ok",
    });
    expect(edge?.badges).not.toContain("drift");

    expect(nodes).toHaveLength(1);
    const [node] = nodes;
    expect(node).toMatchObject({ kind: WG_ENDPOINT_KIND, label: "203.0.113.10:51820" });
  });

  it("renders a stale-handshake peer as a degraded (amber) edge", () => {
    const stale = tunnel({
      peers: [
        {
          publicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
          endpoint: "203.0.113.10:51820",
          allowedIps: ["10.10.0.2/32"],
          lastHandshakeUnix: NOW - 15 * 60,
          rxBytes: 1000,
          txBytes: 900,
          external: true,
          endpointDrifted: false,
        },
      ],
    });
    const { edges } = computeWgTunnelOverlay([stale], nodeIdForName, NOW);
    expect(edges).toHaveLength(1);
    const [edge] = edges;
    expect(edge?.status).toBe("degraded");
    expect(edge?.badges).not.toContain("drift");
  });

  it("renders an endpoint-drifted peer with the 'drift' badge (dashed rendering)", () => {
    const drifted = tunnel({
      peers: [
        {
          publicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
          endpoint: "203.0.113.10:51820",
          observedEndpoint: "203.0.113.99:51820",
          allowedIps: ["10.10.0.2/32"],
          lastHandshakeUnix: NOW - 10,
          rxBytes: 1000,
          txBytes: 900,
          external: true,
          endpointDrifted: true,
        },
      ],
    });
    const { edges } = computeWgTunnelOverlay([drifted], nodeIdForName, NOW);
    expect(edges).toHaveLength(1);
    const [edge] = edges;
    expect(edge?.badges).toContain("drift");
  });

  it("skips a tunnel whose owning node isn't currently rendered on the canvas", () => {
    const orphan = tunnel({ node: "pve9" });
    const { nodes, edges } = computeWgTunnelOverlay([orphan], nodeIdForName, NOW);
    expect(nodes).toHaveLength(0);
    expect(edges).toHaveLength(0);
  });

  it("renders one edge per peer for a multi-peer tunnel", () => {
    const multi = tunnel({
      peers: [
        {
          publicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
          allowedIps: ["10.10.0.2/32"],
          lastHandshakeUnix: NOW - 10,
          rxBytes: 0,
          txBytes: 0,
          external: true,
          endpointDrifted: false,
        },
        {
          publicKey: "PEERtwoKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
          allowedIps: ["10.10.0.3/32"],
          lastHandshakeUnix: NOW - 20 * 60,
          rxBytes: 0,
          txBytes: 0,
          external: true,
          endpointDrifted: false,
        },
      ],
    });
    const { nodes, edges } = computeWgTunnelOverlay([multi], nodeIdForName, NOW);
    expect(nodes).toHaveLength(2);
    expect(edges).toHaveLength(2);
    expect(edges.map((e) => e.status).sort()).toEqual(["degraded", "ok"]);
  });
});
