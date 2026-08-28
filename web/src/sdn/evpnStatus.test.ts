// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { EvpnStatus } from "../api/types";
import { buildEvpnMatrix, evpnStateEntityStatus, formatEvpnUptime, resolveEvpnSelection } from "./evpnStatus";

// T-404 acceptance criterion 1: established/idle/active states colored
// correctly.
describe("evpnStateEntityStatus", () => {
  it("maps Established to ok (green)", () => {
    expect(evpnStateEntityStatus("Established")).toBe("ok");
  });
  it("maps Idle to down (red)", () => {
    expect(evpnStateEntityStatus("Idle")).toBe("down");
  });
  it("maps Active/Connect/OpenSent/OpenConfirm to degraded (amber)", () => {
    expect(evpnStateEntityStatus("Active")).toBe("degraded");
    expect(evpnStateEntityStatus("Connect")).toBe("degraded");
    expect(evpnStateEntityStatus("OpenSent")).toBe("degraded");
    expect(evpnStateEntityStatus("OpenConfirm")).toBe("degraded");
  });
  it("maps empty to unknown", () => {
    expect(evpnStateEntityStatus("")).toBe("unknown");
  });
  it("maps an unrecognized non-empty state to degraded, not unknown", () => {
    expect(evpnStateEntityStatus("SomeFutureFRRState")).toBe("degraded");
  });
});

const status: EvpnStatus = {
  generatedAt: 1_752_000_000,
  nodes: [
    {
      node: "pve1",
      frrInstalled: true,
      routerId: "10.20.0.11",
      asn: 65001,
      peers: [
        { peerAddr: "10.20.0.12", peerNode: "pve2", state: "Established", pfxRcd: 6, uptimeSecs: 5025 },
        { peerAddr: "10.20.0.13", peerNode: "pve3", state: "Idle", stateReason: "Admin" },
      ],
      vnis: [{ vni: 10001, type: "L2" }],
    },
    {
      node: "pve2",
      frrInstalled: true,
      peers: [{ peerAddr: "10.20.0.11", peerNode: "pve1", state: "Established" }],
      vnis: [],
    },
    { node: "pve3", frrInstalled: false, peers: [], vnis: [] },
  ],
  exitNodes: [],
  controllers: [],
  findings: [],
};

describe("buildEvpnMatrix", () => {
  it("rows are every node, columns are the union of observed peer addresses", () => {
    const m = buildEvpnMatrix(status);
    expect(m.nodes).toEqual(["pve1", "pve2", "pve3"]);
    expect(m.peerAddrs).toEqual(["10.20.0.11", "10.20.0.12", "10.20.0.13"]);
  });

  it("cellFor returns the matching Peer for an observed session", () => {
    const m = buildEvpnMatrix(status);
    const cell = m.cellFor("pve1", "10.20.0.12");
    expect(cell?.state).toBe("Established");
    expect(cell?.peerNode).toBe("pve2");
  });

  it("cellFor returns undefined for a node/peer pair with no observed session", () => {
    const m = buildEvpnMatrix(status);
    // pve3 has no FRR installed at all -> no cells in its row.
    expect(m.cellFor("pve3", "10.20.0.11")).toBeUndefined();
    // pve2 never observed a session to 10.20.0.13.
    expect(m.cellFor("pve2", "10.20.0.13")).toBeUndefined();
  });
});

describe("resolveEvpnSelection", () => {
  it("resolves a valid selection to its node/peer", () => {
    const resolved = resolveEvpnSelection(status, { node: "pve1", peerAddr: "10.20.0.12" });
    expect(resolved?.node.node).toBe("pve1");
    expect(resolved?.peer.state).toBe("Established");
  });
  it("returns undefined for a stale/unknown selection", () => {
    expect(resolveEvpnSelection(status, { node: "pve1", peerAddr: "9.9.9.9" })).toBeUndefined();
    expect(resolveEvpnSelection(status, { node: "does-not-exist", peerAddr: "10.20.0.12" })).toBeUndefined();
    expect(resolveEvpnSelection(undefined, { node: "pve1", peerAddr: "10.20.0.12" })).toBeUndefined();
    expect(resolveEvpnSelection(status, undefined)).toBeUndefined();
  });
});

describe("formatEvpnUptime", () => {
  it("formats an em dash for zero/undefined", () => {
    expect(formatEvpnUptime(undefined)).toBe("—");
    expect(formatEvpnUptime(0)).toBe("—");
  });
  it("formats seconds-only durations", () => {
    expect(formatEvpnUptime(45)).toBe("45s");
  });
  it("formats minutes", () => {
    expect(formatEvpnUptime(125)).toBe("2m05s");
  });
  it("formats hours", () => {
    expect(formatEvpnUptime(3600 + 23 * 60 + 45)).toBe("1h23m");
    expect(formatEvpnUptime(5025)).toBe("1h23m"); // matches the matrix fixture's 01:23:45 uptime
  });
  it("formats days", () => {
    expect(formatEvpnUptime(3 * 86400 + 2 * 3600)).toBe("3d02h");
  });
});
