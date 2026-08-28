// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
  bridgeTargetsForGuest,
  defaultEgoSimulateRequest,
  deriveConntrackPanelState,
  deriveFlowsPanelState,
  findingsForGuest,
  formatByteCount,
  guestRefFromNicRef,
  guestsFromNicRows,
  isGuestRef,
  nicsForGuest,
  primaryNic,
  summarizeGuestRuleset,
} from "./guestEgo";
import type { GuestNicRow } from "../guests/guestNics";
import type { FlowRecord, ResolvedView, StreamFinding } from "../api/types";

function nic(overrides: Partial<GuestNicRow> & Pick<GuestNicRow, "ref">): GuestNicRow {
  return {
    label: "vm/net0",
    node: "pve1",
    linkDown: false,
    ...overrides,
  };
}

describe("isGuestRef", () => {
  it("accepts a bare guest ref", () => {
    expect(isGuestRef("guest:pve1:100")).toBe(true);
  });
  it("rejects a guest-nic ref (same string prefix, different kind)", () => {
    expect(isGuestRef("guest-nic:pve1:100/net0")).toBe(false);
  });
  it("rejects an unrelated kind", () => {
    expect(isGuestRef("bridge:pve1:vmbr0")).toBe(false);
  });
});

describe("nicsForGuest", () => {
  const rows: GuestNicRow[] = [
    nic({ ref: "guest-nic:pve1:100/net0", node: "pve1", bridgeOrVnet: "bridge:pve1:vmbr0" }),
    nic({ ref: "guest-nic:pve1:100/net1", node: "pve1", bridgeOrVnet: "bridge:pve1:vmbr1" }),
    nic({ ref: "guest-nic:pve1:101/net0", node: "pve1", bridgeOrVnet: "bridge:pve1:vmbr0" }),
    nic({ ref: "guest-nic:pve2:100/net0", node: "pve2", bridgeOrVnet: "bridge:pve2:vmbr0" }),
  ];

  it("matches only this guest's node+vmid, not a same-vmid guest on another node", () => {
    const result = nicsForGuest(rows, "guest:pve1:100");
    expect(result.map((r) => r.ref)).toEqual(["guest-nic:pve1:100/net0", "guest-nic:pve1:100/net1"]);
  });

  it("returns an empty list for a guest with no NICs in the row set", () => {
    expect(nicsForGuest(rows, "guest:pve1:999")).toEqual([]);
  });
});

describe("bridgeTargetsForGuest", () => {
  it("dedupes targets and drops unattached NICs", () => {
    const nics = [
      nic({ ref: "guest-nic:pve1:100/net0", bridgeOrVnet: "bridge:pve1:vmbr0" }),
      nic({ ref: "guest-nic:pve1:100/net1", bridgeOrVnet: "bridge:pve1:vmbr0" }),
      nic({ ref: "guest-nic:pve1:100/net2", bridgeOrVnet: undefined }),
    ];
    expect(bridgeTargetsForGuest(nics)).toEqual(["bridge:pve1:vmbr0"]);
  });

  it("returns an empty list when nothing is attached", () => {
    expect(bridgeTargetsForGuest([nic({ ref: "guest-nic:pve1:100/net0" })])).toEqual([]);
  });
});

describe("primaryNic", () => {
  it("picks the ref-sorted first NIC deterministically", () => {
    const nics = [
      nic({ ref: "guest-nic:pve1:100/net1" }),
      nic({ ref: "guest-nic:pve1:100/net0" }),
    ];
    expect(primaryNic(nics)?.ref).toBe("guest-nic:pve1:100/net0");
  });

  it("is undefined for a guest with no NICs", () => {
    expect(primaryNic([])).toBeUndefined();
  });
});

describe("defaultEgoSimulateRequest", () => {
  it("builds a guest-nic -> external request", () => {
    expect(defaultEgoSimulateRequest("guest-nic:pve1:100/net0")).toEqual({
      src: { kind: "guest-nic", ref: "guest-nic:pve1:100/net0" },
      dst: { kind: "external" },
    });
  });
});

describe("findingsForGuest", () => {
  function finding(overrides: Partial<StreamFinding>): StreamFinding {
    return {
      id: "x",
      source: "health",
      check: "stub",
      severity: "warning",
      detail: "",
      nodes: ["pve1"],
      fixable: false,
      ...overrides,
    };
  }

  it("matches a finding whose refs name the guest itself", () => {
    const f = finding({ id: "a", refs: ["guest:pve1:100"] });
    expect(findingsForGuest([f], "guest:pve1:100", [])).toEqual([f]);
  });

  it("matches a finding whose refs name one of the guest's NICs", () => {
    const f = finding({ id: "b", refs: ["guest-nic:pve1:100/net0"] });
    expect(findingsForGuest([f], "guest:pve1:100", ["guest-nic:pve1:100/net0"])).toEqual([f]);
  });

  it("excludes a finding for an unrelated ref, even on the same node", () => {
    const f = finding({ id: "c", refs: ["bridge:pve1:vmbr0"], nodes: ["pve1"] });
    expect(findingsForGuest([f], "guest:pve1:100", [])).toEqual([]);
  });

  it("excludes a finding with no refs at all", () => {
    const f = finding({ id: "d" });
    expect(findingsForGuest([f], "guest:pve1:100", [])).toEqual([]);
  });
});

describe("deriveFlowsPanelState", () => {
  const base = {
    targets: ["bridge:pve1:vmbr0"],
    clusterHasAnyFlows: true,
    clusterProbeLoading: false,
    clusterProbeError: false,
    guestItems: [] as FlowRecord[],
    guestLoading: false,
    guestError: false,
  };

  it("reports no-targets when the guest has no resolved bridge/VNet attachment", () => {
    expect(deriveFlowsPanelState({ ...base, targets: [] }).kind).toBe("no-targets");
  });

  it("reports loading while either query is in flight", () => {
    expect(deriveFlowsPanelState({ ...base, clusterProbeLoading: true }).kind).toBe("loading");
    expect(deriveFlowsPanelState({ ...base, guestLoading: true }).kind).toBe("loading");
  });

  it("reports error when either query failed", () => {
    expect(deriveFlowsPanelState({ ...base, clusterProbeError: true }).kind).toBe("error");
    expect(deriveFlowsPanelState({ ...base, guestError: true }).kind).toBe("error");
  });

  // The card's central case: "ingestion is off" must never render the same
  // as "ingestion is on but nothing for this guest".
  it("reports ingestion-disabled when the cluster-wide probe is genuinely empty", () => {
    expect(deriveFlowsPanelState({ ...base, clusterHasAnyFlows: false }).kind).toBe("ingestion-disabled");
  });

  it("reports empty (not ingestion-disabled) when the cluster has flows but this guest has none", () => {
    const state = deriveFlowsPanelState({ ...base, clusterHasAnyFlows: true, guestItems: [] });
    expect(state.kind).toBe("empty");
  });

  it("reports data with the guest-scoped items when present", () => {
    const item: FlowRecord = {
      at: 1,
      node: "pve1",
      srcIp: "10.0.0.1",
      dstIp: "10.0.0.2",
      proto: 6,
      bytes: 100,
      packets: 1,
      source: "sflow",
    };
    const state = deriveFlowsPanelState({ ...base, guestItems: [item] });
    expect(state).toEqual({ kind: "data", items: [item] });
  });
});

describe("deriveConntrackPanelState", () => {
  const base = {
    isLoading: false,
    isError: false,
    items: [] as { node: string }[],
    unavailableNodes: undefined as string[] | undefined,
    guestNode: "pve1",
  };

  it("reports loading/error before anything else", () => {
    expect(deriveConntrackPanelState({ ...base, isLoading: true }).kind).toBe("loading");
    expect(deriveConntrackPanelState({ ...base, isError: true }).kind).toBe("error");
  });

  // The card's conntrack case: distinguish "this node cannot provide
  // conntrack" from "no active connections right now".
  it("reports unavailable when the guest's own node is in unavailableNodes", () => {
    const state = deriveConntrackPanelState({ ...base, unavailableNodes: ["pve1"] });
    expect(state.kind).toBe("unavailable");
  });

  it("does not report unavailable for an unrelated node's outage", () => {
    const state = deriveConntrackPanelState({ ...base, unavailableNodes: ["pve2"] });
    expect(state.kind).toBe("empty");
  });

  it("reports empty when there are simply no matching entries", () => {
    expect(deriveConntrackPanelState(base).kind).toBe("empty");
  });

  it("reports data when entries exist", () => {
    const state = deriveConntrackPanelState({ ...base, items: [{ node: "pve1" }] });
    expect(state).toEqual({ kind: "data", items: [{ node: "pve1" }] });
  });
});

describe("summarizeGuestRuleset", () => {
  it("counts enabled allow/deny rules and carries gate messages through verbatim", () => {
    const resolved: ResolvedView = {
      guest: "guest:pve1:100",
      active: true,
      gates: [{ scope: "cluster", message: "cluster firewall disabled" }],
      rules: [
        { origin: "guest", rule: { pos: 0, enabled: true, direction: "in", action: "ACCEPT" }, pos: 0 },
        { origin: "guest", rule: { pos: 1, enabled: true, direction: "in", action: "DROP" }, pos: 1 },
        { origin: "guest", rule: { pos: 2, enabled: false, direction: "in", action: "ACCEPT" }, pos: 2 },
      ],
      defaultIn: { direction: "in", policy: "DROP", origin: "cluster" },
      defaultOut: { direction: "out", policy: "ACCEPT", origin: "cluster" },
    };
    expect(summarizeGuestRuleset(resolved)).toEqual({
      active: true,
      defaultIn: "DROP",
      defaultOut: "ACCEPT",
      totalRules: 3,
      enabledRules: 2,
      allowCount: 1,
      denyCount: 1,
      gateMessages: ["cluster firewall disabled"],
    });
  });

  it("handles no gates as an empty list, never undefined", () => {
    const resolved: ResolvedView = {
      guest: "guest:pve1:100",
      active: false,
      rules: [],
      defaultIn: { direction: "in", policy: "DROP", origin: "cluster" },
      defaultOut: { direction: "out", policy: "DROP", origin: "cluster" },
    };
    expect(summarizeGuestRuleset(resolved).gateMessages).toEqual([]);
  });
});

describe("guestRefFromNicRef", () => {
  it("derives the owning guest ref from a guest-nic ref", () => {
    expect(guestRefFromNicRef("guest-nic:pve1:100/net0")).toBe("guest:pve1:100");
  });

  it("passes a bare guest ref through unchanged", () => {
    expect(guestRefFromNicRef("guest:pve1:100")).toBe("guest:pve1:100");
  });
});

describe("guestsFromNicRows", () => {
  it("dedupes multi-NIC guests into one summary row, counting NICs", () => {
    const rows = [
      nic({ ref: "guest-nic:pve1:100/net0", label: "web/net0", node: "pve1" }),
      nic({ ref: "guest-nic:pve1:100/net1", label: "web/net1", node: "pve1" }),
      nic({ ref: "guest-nic:pve1:101/net0", label: "db/net0", node: "pve1" }),
    ];
    expect(guestsFromNicRows(rows)).toEqual([
      { ref: "guest:pve1:101", label: "db", node: "pve1", nicCount: 1 },
      { ref: "guest:pve1:100", label: "web", node: "pve1", nicCount: 2 },
    ]);
  });

  it("falls back to the raw label when it carries no '/'", () => {
    const rows = [nic({ ref: "guest-nic:pve1:100/net0", label: "no-slash-label", node: "pve1" })];
    expect(guestsFromNicRows(rows)).toEqual([
      { ref: "guest:pve1:100", label: "no-slash-label", node: "pve1", nicCount: 1 },
    ]);
  });
});

describe("formatByteCount", () => {
  it("renders sub-1024 counts in bytes", () => {
    expect(formatByteCount(512)).toBe("512 B");
  });
  it("renders kilobytes with one decimal", () => {
    expect(formatByteCount(2048)).toBe("2.0 KB");
  });
  it("renders megabytes", () => {
    expect(formatByteCount(5 * 1024 * 1024)).toBe("5.0 MB");
  });
});
