// SPDX-License-Identifier: Apache-2.0

// T-4015: op-builder tests for the general (non-federation) WireGuard
// management surface. Asserts the produced Op shapes are byte-identical in
// form to what wizardOps.ts's buildConnectClustersOps already produces for
// the same op types (T-4015 AC1: "one op vocabulary, two entry points") —
// and that a private key never appears in any built op (the UI-side half
// of the key-custody guarantee).
import { describe, expect, it } from "vitest";
import type { WgPeerAddParams, WgTunnelCreateParams, WgTunnelUpdateParams } from "../api/types";
import { wgPeerTarget, wgTunnelTarget } from "./wizardOps";
import {
  buildWgPeerAddOp,
  buildWgPeerRemoveOp,
  buildWgTunnelCreateOp,
  buildWgTunnelDeleteOp,
  buildWgTunnelUpdateOp,
  emptyWgPeerForm,
  emptyWgTunnelForm,
  looksLikeWgKey,
  parseAddressList,
  type WgPeerFormValues,
  type WgTunnelFormValues,
} from "./wgTunnelOps";

describe("buildWgTunnelCreateOp", () => {
  it("builds wg.tunnel.create targeting wg-tunnel:<node>:<id>, omitting unset numeric/carrier/address fields", () => {
    const form: WgTunnelFormValues = { ...emptyWgTunnelForm(), ifName: "wg0", listenPort: 51820 };
    const op = buildWgTunnelCreateOp("pve1", "tun-abc", form);
    expect(op.op).toBe("wg.tunnel.create");
    expect(op.target).toBe(wgTunnelTarget("pve1", "tun-abc"));
    expect(op.target).toBe("wg-tunnel:pve1:tun-abc");
    const params = op.params as WgTunnelCreateParams;
    expect(params).toEqual({ ifName: "wg0", listenPort: 51820, carrier: undefined, addresses: undefined, mtu: undefined });
  });

  it("carries carrier/addresses/mtu through when set", () => {
    const form: WgTunnelFormValues = { ifName: "wg1", carrier: "vmbr0", addresses: ["10.10.0.1/24"], listenPort: 51821, mtu: 1400 };
    const op = buildWgTunnelCreateOp("pve1", "tun-2", form);
    const params = op.params as WgTunnelCreateParams;
    expect(params).toEqual({ ifName: "wg1", carrier: "vmbr0", addresses: ["10.10.0.1/24"], listenPort: 51821, mtu: 1400 });
  });

  it("never carries a privateKey/private_key field — this builder has no such field to set", () => {
    const op = buildWgTunnelCreateOp("pve1", "tun-3", { ...emptyWgTunnelForm(), ifName: "wg0" });
    expect(JSON.stringify(op)).not.toMatch(/private/i);
  });
});

describe("buildWgTunnelUpdateOp", () => {
  it("sets only the fields that changed from initial (pointer-field diff convention)", () => {
    const initial: WgTunnelFormValues = { ifName: "wg0", carrier: "", addresses: ["10.10.0.1/24"], listenPort: 51820, mtu: 0 };
    const changed: WgTunnelFormValues = { ...initial, listenPort: 51821 };
    const op = buildWgTunnelUpdateOp("pve1", "tun-1", initial, changed);
    const params = op.params as WgTunnelUpdateParams;
    expect(params).toEqual({ listenPort: 51821 });
  });

  it("produces an empty params object when nothing changed", () => {
    const initial: WgTunnelFormValues = { ifName: "wg0", carrier: "", addresses: [], listenPort: 51820, mtu: 0 };
    const op = buildWgTunnelUpdateOp("pve1", "tun-1", initial, { ...initial });
    expect(op.params).toEqual({});
  });

  it("detects an address-list change even when the array reference differs but content is identical (no false-positive diff)", () => {
    const initial: WgTunnelFormValues = { ifName: "wg0", carrier: "", addresses: ["10.10.0.1/24"], listenPort: 51820, mtu: 0 };
    const same: WgTunnelFormValues = { ...initial, addresses: ["10.10.0.1/24"] };
    expect(buildWgTunnelUpdateOp("pve1", "tun-1", initial, same).params).toEqual({});
  });
});

describe("buildWgTunnelDeleteOp", () => {
  it("builds wg.tunnel.delete with empty params, targeting the tunnel", () => {
    const op = buildWgTunnelDeleteOp("pve1", "tun-1");
    expect(op).toEqual({ op: "wg.tunnel.delete", target: "wg-tunnel:pve1:tun-1", params: {} });
  });
});

describe("buildWgPeerAddOp", () => {
  it("builds wg.peer.add targeting wg-peer:<node>:<tunnelId>/<publicKey>, always external:true", () => {
    const form: WgPeerFormValues = { ...emptyWgPeerForm(), publicKey: "PUBKEY==", endpoint: "203.0.113.10:51820", allowedIps: ["10.10.0.2/32"] };
    const op = buildWgPeerAddOp("pve1", "tun-1", form);
    expect(op.op).toBe("wg.peer.add");
    expect(op.target).toBe(wgPeerTarget("pve1", "tun-1", "PUBKEY=="));
    const params = op.params as WgPeerAddParams;
    expect(params).toEqual({
      publicKey: "PUBKEY==",
      endpoint: "203.0.113.10:51820",
      presharedKey: undefined,
      allowedIps: ["10.10.0.2/32"],
      keepaliveSec: undefined,
      clusterId: undefined,
      external: true,
    });
  });

  it("re-submitting the same publicKey with different fields is the same op shape (upsert, not a duplicate-creating op)", () => {
    const form1: WgPeerFormValues = { ...emptyWgPeerForm(), publicKey: "KEY==", endpoint: "1.2.3.4:51820" };
    const form2: WgPeerFormValues = { ...emptyWgPeerForm(), publicKey: "KEY==", endpoint: "5.6.7.8:51820" };
    const op1 = buildWgPeerAddOp("pve1", "tun-1", form1);
    const op2 = buildWgPeerAddOp("pve1", "tun-1", form2);
    expect(op1.op).toBe(op2.op);
    expect(op1.target).toBe(op2.target);
  });

  it("never carries a preshared key's sealed form — only the write-only plaintext ingest field", () => {
    const op = buildWgPeerAddOp("pve1", "tun-1", { ...emptyWgPeerForm(), publicKey: "K==", presharedKey: "secret-psk" });
    expect(JSON.stringify(op)).not.toMatch(/presharedKeyEnc/);
  });
});

describe("buildWgPeerRemoveOp", () => {
  it("builds wg.peer.remove targeting the peer, carrying its public key", () => {
    const op = buildWgPeerRemoveOp("pve1", "tun-1", "KEY==");
    expect(op).toEqual({ op: "wg.peer.remove", target: "wg-peer:pve1:tun-1/KEY==", params: { publicKey: "KEY==" } });
  });
});

describe("looksLikeWgKey", () => {
  it("accepts a 44-char base64-looking key ending in '='", () => {
    expect(looksLikeWgKey("A".repeat(43) + "=")).toBe(true);
  });
  it("rejects anything else", () => {
    expect(looksLikeWgKey("too-short")).toBe(false);
    expect(looksLikeWgKey("")).toBe(false);
  });
});

describe("parseAddressList", () => {
  it("splits, trims, and drops empties", () => {
    expect(parseAddressList(" 10.0.0.1/32 , 10.0.0.2/32,, ")).toEqual(["10.0.0.1/32", "10.0.0.2/32"]);
  });
  it("returns an empty array for blank input", () => {
    expect(parseAddressList("")).toEqual([]);
  });
});
