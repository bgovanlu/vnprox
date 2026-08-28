// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { buildConnectClustersPreview } from "./previewGraph";
import { buildConnectClustersOps, wgPeerTarget, type ConnectClustersParams } from "./wizardOps";
import { wgEndpointNodeId } from "./wgTunnelEdges";

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

describe("buildConnectClustersPreview", () => {
  it("is empty until both a source node and a peer public key are entered", () => {
    expect(buildConnectClustersPreview(params({ sourceNode: "" }), TUNNEL_ID)).toEqual({ nodes: [], edges: [] });
    expect(buildConnectClustersPreview(params({ peerPublicKey: "" }), TUNNEL_ID)).toEqual({ nodes: [], edges: [] });
  });

  it("renders the source node (real, ok) and the peer endpoint (planned) connected by a planned wg-tunnel edge", () => {
    const graph = buildConnectClustersPreview(params(), TUNNEL_ID);
    expect(graph.nodes).toHaveLength(2);
    expect(graph.edges).toHaveLength(1);
    const [edge] = graph.edges;
    expect(edge).toMatchObject({ from: "node:pve1:pve1", to: wgEndpointNodeId(TUNNEL_ID, params().peerPublicKey), kind: "wg-tunnel" });
    expect(edge?.badges).toContain("planned");
  });
});

// T-1402 AC2: "The wizard's preview pane matches the changeset it actually
// submits (no drift between preview and submitted ops)." The wizard
// component threads one `tunnelId`, generated once, into both
// buildConnectClustersPreview and buildConnectClustersOps (see
// ConnectClustersWizard.tsx's own doc comment) — this test proves that
// shared-id contract holds structurally: the preview's peer node id and the
// submitted wg.peer.add op's target both encode the identical
// (tunnelId, publicKey) pair, for any params/tunnelId combination.
describe("preview <-> submitted-ops parity", () => {
  it("the preview's peer endpoint node id matches the wg.peer.add op's target for the same (params, tunnelId)", () => {
    const p = params();
    const graph = buildConnectClustersPreview(p, TUNNEL_ID);
    const ops = buildConnectClustersOps(p, TUNNEL_ID, 0);
    const peerOp = ops.find((op) => op.op === "wg.peer.add");

    const [, peerNode] = graph.nodes; // [0] = source node, [1] = peer endpoint
    expect(peerOp?.target).toBe(wgPeerTarget(p.sourceNode, TUNNEL_ID, p.peerPublicKey));
    expect(peerNode?.id).toBe(wgEndpointNodeId(TUNNEL_ID, p.peerPublicKey));
    // Both derive from the identical (tunnelId, publicKey) — the peer node
    // id and the op target agree on which peer this changeset is about.
    expect(peerOp?.target).toContain(TUNNEL_ID);
    expect(peerNode?.id).toContain(TUNNEL_ID);
    expect(peerOp?.target).toContain(p.peerPublicKey);
    expect(peerNode?.id).toContain(p.peerPublicKey);
  });

  it("holds for a different tunnel id too — the parity isn't a coincidence of one fixed constant", () => {
    const p = params({ peerPublicKey: "OTHERKEYbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb=" });
    const otherTunnelId = "0193f000-0000-7000-8000-000000000099";
    const graph = buildConnectClustersPreview(p, otherTunnelId);
    const ops = buildConnectClustersOps(p, otherTunnelId, 0);
    const peerOp = ops.find((op) => op.op === "wg.peer.add");
    const [, peerNode] = graph.nodes;
    expect(peerNode?.id).toBe(wgEndpointNodeId(otherTunnelId, p.peerPublicKey));
    expect(peerOp?.target).toBe(wgPeerTarget(p.sourceNode, otherTunnelId, p.peerPublicKey));
  });
});
