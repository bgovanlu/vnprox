import { describe, expect, it } from "vitest";
import type { SimUrlState } from "./urlState";
import { decodeSimState, encodeSimState, simUrlStatePath, simUrlStateToRequest } from "./urlState";

describe("encodeSimState / decodeSimState round-trip (T-504 AC4)", () => {
  const cases: { name: string; state: SimUrlState }[] = [
    {
      name: "guest-nic src, ip dst, proto+port",
      state: {
        src: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" },
        dst: { kind: "ip", ip: "10.0.0.5" },
        proto: "tcp",
        port: 443,
      },
    },
    {
      name: "guest-nic to external, no proto/port (any)",
      state: {
        src: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" },
        dst: { kind: "external" },
      },
    },
    {
      name: "only src picked so far (dst not yet chosen)",
      state: { src: { kind: "guest-nic", ref: "guest-nic:pve2:200/net0" } },
    },
    {
      name: "nothing picked yet",
      state: {},
    },
  ];

  for (const { name, state } of cases) {
    it(`round-trips: ${name}`, () => {
      const encoded = encodeSimState(state);
      const decoded = decodeSimState(encoded);
      expect(decoded).toEqual(state);
    });
  }

  it("round-trips through a real query string (paste -> same state)", () => {
    const state: SimUrlState = {
      src: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" },
      dst: { kind: "guest-nic", ref: "guest-nic:pve1:102/net0" },
      proto: "tcp",
      port: 80,
    };
    const path = simUrlStatePath("/tools", state);
    expect(path.startsWith("/tools?")).toBe(true);
    const qs = path.split("?")[1];
    expect(decodeSimState(qs ?? "")).toEqual(state);
  });

  it("degrades an incomplete endpoint (kind without its ref/ip) to undefined rather than throwing", () => {
    const params = new URLSearchParams("srcKind=guest-nic&dstKind=ip");
    expect(() => decodeSimState(params)).not.toThrow();
    const decoded = decodeSimState(params);
    expect(decoded.src).toBeUndefined();
    expect(decoded.dst).toBeUndefined();
  });

  it("omits proto/port from the query string when unset", () => {
    const params = encodeSimState({ src: { kind: "external" }, dst: { kind: "external" } });
    expect(params.has("proto")).toBe(false);
    expect(params.has("port")).toBe(false);
  });

  it("ignores a zero/negative port (treated as unset, matching the API's '0 = any' convention)", () => {
    const decoded = decodeSimState("srcKind=external&dstKind=external&port=0");
    expect(decoded.port).toBeUndefined();
  });
});

describe("simUrlStateToRequest", () => {
  it("returns undefined until both endpoints are known", () => {
    expect(simUrlStateToRequest({})).toBeUndefined();
    expect(simUrlStateToRequest({ src: { kind: "external" } })).toBeUndefined();
  });

  it("builds a request once both sides are set", () => {
    const req = simUrlStateToRequest({
      src: { kind: "external" },
      dst: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" },
      proto: "icmp",
    });
    expect(req).toEqual({
      src: { kind: "external" },
      dst: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" },
      proto: "icmp",
      port: undefined,
    });
  });
});
