// Unit coverage for the a11y bridge's pure half (a11yBridge.ts): the
// aria-label vocabulary, the one-proxy-per-entity projection, roving order,
// and screen-rect resolution. This is the contract T-905 (SR labels) and
// T-903 (palette/roving nav) build on, so it is exercised directly here.
import { describe, expect, it } from "vitest";
import type { Node as FlowNode } from "@xyflow/react";
import type { EntityNodeData } from "./EntityNode";
import {
  badgeAriaParts,
  buildA11yProxies,
  entityAriaLabel,
  nextRovingId,
  proxyScreenRect,
  rovingOrder,
} from "./a11yBridge";

function flowNode(
  id: string,
  data: Partial<EntityNodeData>,
  position = { x: 0, y: 0 },
  selected = false,
): FlowNode<EntityNodeData, "entity"> {
  return {
    id,
    type: "entity",
    position,
    selected,
    data: {
      label: id,
      kind: "bridge",
      status: "ok",
      badges: [],
      dimmed: false,
      highlighted: false,
      isGuestGroup: false,
      ...data,
    },
  };
}

describe("entityAriaLabel", () => {
  it("names kind, identity, and status", () => {
    expect(entityAriaLabel(flowNode("vmbr0", { kind: "bridge", status: "ok" }).data)).toBe(
      "bridge vmbr0, status ok",
    );
  });

  it("spells out the amber mgmt/corosync/mgmt-path trio in words", () => {
    const label = entityAriaLabel(
      flowNode("vmbr0", { kind: "bridge", status: "ok", badges: ["mgmt", "corosync"] }).data,
    );
    expect(label).toContain("carries the management IP");
    expect(label).toContain("carries a corosync link");
  });

  it("lists ordinary badges verbatim and calls out an open finding by severity/source (T-3501)", () => {
    const label = entityAriaLabel(
      flowNode("bond0", {
        kind: "bond",
        status: "degraded",
        badges: ["mode=802.3ad", "drift", "finding:drift:warning"],
      }).data,
    );
    expect(label).toBe("bond bond0, status degraded, badges: mode=802.3ad, drift, warning drift finding");
  });

  it("names a health finding as health, not drift (T-3501's core defect)", () => {
    const label = entityAriaLabel(
      flowNode("vmbr1", { kind: "bridge", status: "down", badges: ["drift", "finding:health:error"] }).data,
    );
    expect(label).toContain("error health finding");
    expect(label).not.toContain("has configuration drift");
  });

  it("surfaces the finding's own detail text when Findings is supplied", () => {
    const label = entityAriaLabel(
      flowNode("vmbr1", {
        kind: "bridge",
        status: "down",
        badges: ["drift", "finding:health:error"],
        findings: [{ source: "health", severity: "error", check: "bridge_no_carrier", detail: "enp2s0 has no carrier" }],
      }).data,
    );
    expect(label).toContain("error health finding: enp2s0 has no carrier");
  });

  it("describes a collapsed guest-group pill by its count", () => {
    expect(
      entityAriaLabel(
        flowNode("guest-group:pve1:bridge:pve1:vmbr0", {
          kind: "guest-group",
          isGuestGroup: true,
          collapsedCount: 23,
          label: "23 guests",
        }).data,
      ),
    ).toBe("guest group, 23 guests, status ok");
  });
});

describe("badgeAriaParts (T-905: reused verbatim by DOM entities outside EntityNodeData, e.g. SwitchFaceplate)", () => {
  it("is exactly what entityAriaLabel appends after status — no finding", () => {
    expect(badgeAriaParts(["mode=802.3ad"])).toEqual(["badges: mode=802.3ad"]);
  });

  it("spells out mgmt/corosync/mgmt-path in words", () => {
    expect(badgeAriaParts(["mgmt", "corosync", "mgmt-path"])).toEqual([
      "carries the management IP",
      "carries a corosync link",
      "on the management path",
    ]);
  });

  // T-3501: the legacy bare "drift" wire badge (kept for back-compat — see
  // findingBadges.ts) no longer gets a fabricated "has configuration drift"
  // sentence: with no source/severity attached to it, claiming to know it's
  // specifically *drift* would be exactly this task's own defect. It falls
  // back to the plain "badges: …" listing every other unrecognized badge
  // gets.
  it("lists the legacy bare drift badge plainly, without inventing a severity/source", () => {
    expect(badgeAriaParts(["drift"])).toEqual(["badges: drift"]);
  });

  it("names each open finding's severity and source, once per finding:<source>:<severity> token", () => {
    expect(badgeAriaParts(["mgmt", "finding:drift:warning", "finding:health:error"])).toEqual([
      "carries the management IP",
      "warning drift finding",
      "error health finding",
    ]);
  });

  it("appends the finding's own detail text when a matching Findings entry is supplied", () => {
    expect(
      badgeAriaParts(
        ["finding:health:error"],
        [{ source: "health", severity: "error", check: "bridge_no_carrier", detail: "enp4s0 has no carrier" }],
      ),
    ).toEqual(["error health finding: enp4s0 has no carrier"]);
  });

  it("returns an empty array for no badges", () => {
    expect(badgeAriaParts([])).toEqual([]);
  });
});

describe("buildA11yProxies", () => {
  it("emits exactly one proxy per node, carrying its label + graph box", () => {
    const nodes = [
      flowNode("a", { label: "vmbr0" }, { x: 10, y: 20 }),
      flowNode("b", { label: "bond0", kind: "bond" }, { x: 200, y: 20 }, true),
    ];
    const proxies = buildA11yProxies(nodes);
    expect(proxies).toHaveLength(2);
    expect(proxies[0]?.ariaLabel).toBe("bridge vmbr0, status ok");
    expect(proxies[0]?.graph).toMatchObject({ x: 10, y: 20 });
    expect(proxies[1]?.selected).toBe(true);
  });
});

describe("proxyScreenRect tracks the viewport (pan/zoom sync)", () => {
  it("moves and scales the proxy under a viewport transform", () => {
    const [proxy] = buildA11yProxies([flowNode("a", {}, { x: 100, y: 50 })]);
    expect(proxy).toBeDefined();
    if (!proxy) return;
    const panned = proxyScreenRect(proxy, { x: 30, y: 40, zoom: 1 });
    expect(panned).toMatchObject({ x: 130, y: 90 });
    const zoomed = proxyScreenRect(proxy, { x: 0, y: 0, zoom: 2 });
    expect(zoomed.x).toBe(200);
    expect(zoomed.width).toBe(proxy.graph.width * 2);
  });
});

describe("rovingOrder / nextRovingId", () => {
  const nodes = [
    flowNode("bottom", {}, { x: 0, y: 300 }),
    flowNode("top-right", {}, { x: 400, y: 0 }),
    flowNode("top-left", {}, { x: 0, y: 0 }),
  ];
  const order = rovingOrder(buildA11yProxies(nodes));

  it("orders top-to-bottom then left-to-right (reading order)", () => {
    expect(order.map((p) => p.id)).toEqual(["top-left", "top-right", "bottom"]);
  });

  it("advances and wraps in both directions", () => {
    expect(nextRovingId(order, "top-left", 1)).toBe("top-right");
    expect(nextRovingId(order, "bottom", 1)).toBe("top-left"); // wrap forward
    expect(nextRovingId(order, "top-left", -1)).toBe("bottom"); // wrap backward
  });

  it("starts at the first/last entity when nothing is focused yet", () => {
    expect(nextRovingId(order, undefined, 1)).toBe("top-left");
    expect(nextRovingId(order, undefined, -1)).toBe("bottom");
  });

  it("returns undefined for an empty order", () => {
    expect(nextRovingId([], undefined, 1)).toBeUndefined();
  });
});
