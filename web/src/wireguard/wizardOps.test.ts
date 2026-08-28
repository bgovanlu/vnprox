// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { buildConnectClustersOps, fwNodeRulesetTarget, wgPeerTarget, wgTunnelTarget, type ConnectClustersParams } from "./wizardOps";

const TUNNEL_ID = "0193f000-0000-7000-8000-000000000001";

function params(overrides: Partial<ConnectClustersParams> = {}): ConnectClustersParams {
  return {
    sourceNode: "pve1",
    ifName: "wg0",
    listenPort: 51820,
    carrier: "vmbr0",
    localAddress: "10.10.0.1/24",
    mtu: 0,
    peerPublicKey: "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
    peerEndpoint: "203.0.113.10:51820",
    peerAllowedIps: ["10.10.0.2/32"],
    presharedKey: "",
    keepaliveSec: 0,
    peerClusterId: "",
    fwSourceCidr: "203.0.113.10/32",
    ...overrides,
  };
}

describe("buildConnectClustersOps", () => {
  it("produces exactly wg.tunnel.create + wg.peer.add + fw.rule.create, in that order — one changeset's worth", () => {
    const ops = buildConnectClustersOps(params(), TUNNEL_ID, 0);
    expect(ops.map((o) => o.op)).toEqual(["wg.tunnel.create", "wg.peer.add", "fw.rule.create"]);
  });

  it("targets the tunnel/peer with the caller-supplied tunnel id, never generating its own", () => {
    const ops = buildConnectClustersOps(params(), TUNNEL_ID, 0);
    const [tunnelOp, peerOp] = ops;
    expect(tunnelOp?.target).toBe(wgTunnelTarget("pve1", TUNNEL_ID));
    expect(peerOp?.target).toBe(wgPeerTarget("pve1", TUNNEL_ID, "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa="));
  });

  it("always marks the peer external — the far side is never vnprox's own to apply against", () => {
    const [, peerOp] = buildConnectClustersOps(params(), TUNNEL_ID, 0);
    expect(peerOp?.params).toMatchObject({ external: true });
  });

  it("never puts the plaintext preshared key anywhere but the ingest-only field", () => {
    const [, peerOp] = buildConnectClustersOps(params({ presharedKey: "shh" }), TUNNEL_ID, 0);
    expect(peerOp?.params).toMatchObject({ presharedKey: "shh" });
    expect(peerOp?.params).not.toHaveProperty("presharedKeyEnc");
  });

  it("builds a firewall rule opening exactly the tunnel's UDP listen port, appended at the given position", () => {
    const [, , fwOp] = buildConnectClustersOps(params(), TUNNEL_ID, 3);
    expect(fwOp?.op).toBe("fw.rule.create");
    expect(fwOp?.target).toBe(fwNodeRulesetTarget("pve1"));
    expect(fwOp?.params).toMatchObject({ direction: "in", action: "ACCEPT", proto: "udp", dport: "51820", source: "203.0.113.10/32", pos: 3 });
  });

  it("tags the peer with the chosen federated cluster, on the ordinary wg.peer.add op — no extra op, no second route", () => {
    const ops = buildConnectClustersOps(params({ peerClusterId: "cl-east" }), TUNNEL_ID, 0);
    expect(ops.map((o) => o.op)).toEqual(["wg.tunnel.create", "wg.peer.add", "fw.rule.create"]);
    expect(ops[1]?.params).toMatchObject({ clusterId: "cl-east" });
  });

  it("omits clusterId entirely when the peer isn't tagged, rather than sending an empty string", () => {
    const [, peerOp] = buildConnectClustersOps(params({ peerClusterId: "" }), TUNNEL_ID, 0);
    expect(JSON.stringify(peerOp?.params)).not.toContain("clusterId");
  });

  it("omits optional tunnel fields (carrier/mtu) when left unset rather than sending empty strings/zeros", () => {
    const [tunnelOp] = buildConnectClustersOps(params({ carrier: "", mtu: 0 }), TUNNEL_ID, 0);
    // Fields are set to `undefined` (not omitted as object keys) so the
    // *wire* payload — what JSON.stringify actually sends — drops them,
    // rather than serializing an empty string / 0 that would overwrite a
    // real value on an update. toHaveProperty would still "find" an
    // undefined-valued key, so assert against the serialized form instead.
    const wire = JSON.stringify(tunnelOp?.params);
    expect(wire).not.toContain("carrier");
    expect(wire).not.toContain("mtu");
  });
});
