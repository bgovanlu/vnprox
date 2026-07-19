// Pure-function coverage for T-1305's Conntrack Explorer URL state — the
// exact encode/decode/link-building logic the map's right-click "View live
// connections" entry (topology/TopologyPage.tsx's conntrackItemFor) and
// FlowPairPanel.tsx's own drill-down link both go through, kept directly
// testable without rendering anything (mirrors flows/urlState.test.ts's
// convention).
import { describe, expect, it } from "vitest";
import {
  conntrackNodeLinkPath,
  decodeConntrackExplorerState,
  encodeConntrackExplorerState,
} from "./urlState";

describe("encodeConntrackExplorerState", () => {
  it("omits every unset field", () => {
    expect(encodeConntrackExplorerState({}).toString()).toBe("");
  });

  it("encodes every set field", () => {
    const qs = encodeConntrackExplorerState({
      node: "pve1", guest: "guest:pve1:104", srcIp: "10.0.0.5", dstIp: "10.0.0.10", port: 443, state: "ESTABLISHED",
    });
    expect(qs.get("node")).toBe("pve1");
    expect(qs.get("guest")).toBe("guest:pve1:104");
    expect(qs.get("srcIp")).toBe("10.0.0.5");
    expect(qs.get("dstIp")).toBe("10.0.0.10");
    expect(qs.get("port")).toBe("443");
    expect(qs.get("state")).toBe("ESTABLISHED");
  });

  it("encodes port 0 (falsy but a valid filter value)", () => {
    expect(encodeConntrackExplorerState({ port: 0 }).get("port")).toBe("0");
  });
});

describe("decodeConntrackExplorerState", () => {
  it("round-trips every field", () => {
    const filter = { node: "pve2", guest: "guest:pve1:104", srcIp: "1.2.3.4", dstIp: "5.6.7.8", port: 22, state: "TIME_WAIT" };
    const decoded = decodeConntrackExplorerState(encodeConntrackExplorerState(filter));
    expect(decoded.filter).toEqual(filter);
  });

  it("degrades a missing/malformed port to undefined rather than throwing", () => {
    expect(decodeConntrackExplorerState("port=notanumber").filter.port).toBeUndefined();
    expect(decodeConntrackExplorerState("").filter.port).toBeUndefined();
  });

  it("accepts a raw query string or an already-built URLSearchParams identically", () => {
    const fromString = decodeConntrackExplorerState("node=pve1");
    const fromParams = decodeConntrackExplorerState(new URLSearchParams("node=pve1"));
    expect(fromString).toEqual(fromParams);
  });
});

describe("conntrackNodeLinkPath", () => {
  it("scopes to the given node", () => {
    expect(conntrackNodeLinkPath("/conntrack", "pve1")).toBe("/conntrack?node=pve1");
  });

  it("falls back to the unscoped base path for an empty/undefined node (e.g. a cluster-scoped sdn-vnet ref)", () => {
    expect(conntrackNodeLinkPath("/conntrack", undefined)).toBe("/conntrack");
    expect(conntrackNodeLinkPath("/conntrack", "")).toBe("/conntrack");
  });
});
