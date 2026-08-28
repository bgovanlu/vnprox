// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import type { ChangesetImpact, FailsimImpact } from "../api/types";
import {
  blastRadiusRequestFromChangesetImpact,
  blastRadiusRequestFromFailsimImpact,
  blastRadiusRequestFromFindingRefs,
  computeBlastRadiusFocus,
  nodeRefForName,
  resolveNodeAnchorsInRequest,
  summarizeBlastRadiusFocus,
  type FocusGraphEdge,
} from "./blastRadiusFocus";

// A small linear-ish fabric: node -> bond -> bridge -> two guests, plus an
// isolated pair (islandA/islandB) with no path to the rest — used to exercise
// the "unreachable but still on-map" case.
//
//   node:pve1  --  bond:pve1:bond0  --  bridge:pve1:vmbr0  --  guest:pve1:100
//                                                          \-  guest:pve1:101
//   node:pve2 (isolated: no edges at all)
const NODE_IDS = [
  "node:pve1:pve1",
  "bond:pve1:bond0",
  "bridge:pve1:vmbr0",
  "guest:pve1:100",
  "guest:pve1:101",
  "node:pve2:pve2",
];

const EDGES: FocusGraphEdge[] = [
  { id: "e1", source: "node:pve1:pve1", target: "bond:pve1:bond0" },
  { id: "e2", source: "bond:pve1:bond0", target: "bridge:pve1:vmbr0" },
  { id: "e3", source: "bridge:pve1:vmbr0", target: "guest:pve1:100" },
  { id: "e4", source: "bridge:pve1:vmbr0", target: "guest:pve1:101" },
];

function failsimImpact(overrides: Partial<FailsimImpact> = {}): FailsimImpact {
  return {
    target: "node:pve1:pve1",
    severity: "warning",
    disconnectedGuests: [],
    strandedVlans: [],
    mgmtPathLoss: [],
    notEvaluated: [],
    quorumRisk: false,
    cephRisk: false,
    ...overrides,
  };
}

describe("nodeRefForName", () => {
  it("mirrors inventory.Ref.String() for a bare Node entity", () => {
    expect(nodeRefForName("pve1")).toBe("node:pve1:pve1");
  });
});

describe("blastRadiusRequestFromFailsimImpact", () => {
  it("carries target + disconnectedGuests/strandedVlans/mgmtPathLoss as affected, converting node names", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({
        target: "bond:pve1:bond0",
        disconnectedGuests: ["guest:pve1:100"],
        strandedVlans: ["sdn-vnet:pve1:vnet1"],
        mgmtPathLoss: ["pve2"],
      }),
    );
    expect(req.origin).toBe("failsim");
    expect(req.target).toBe("bond:pve1:bond0");
    expect(req.affected.sort()).toEqual(["guest:pve1:100", "node:pve2:pve2", "sdn-vnet:pve1:vnet1"].sort());
    expect(req.caveats).toEqual([]);
  });

  it("turns quorumRisk/cephRisk/notEvaluated into caveats, never fabricated focus nodes", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ quorumRisk: true, cephRisk: true, notEvaluated: ["tunnels"] }),
    );
    expect(req.affected).toEqual([]);
    expect(req.caveats).toEqual(["puts corosync quorum at risk", "isolates a Ceph network", "not evaluated: tunnels"]);
  });

  it("dedupes affected refs (a guest that is both disconnected and, hypothetically, repeated)", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ disconnectedGuests: ["guest:pve1:100", "guest:pve1:100"] }),
    );
    expect(req.affected).toEqual(["guest:pve1:100"]);
  });
});

describe("blastRadiusRequestFromChangesetImpact", () => {
  function changesetImpact(overrides: Partial<ChangesetImpact> = {}): ChangesetImpact {
    return {
      nodes: [],
      carriers: [],
      guests: [],
      ops: [],
      disruption: "none",
      touchesMgmtPath: false,
      ...overrides,
    };
  }

  it("anchors on the first op target and folds the rest into affected", () => {
    const req = blastRadiusRequestFromChangesetImpact(
      "cs-1",
      changesetImpact({
        ops: [
          { op: "iface.bond.update", target: "bond:pve1:bond0", disruption: "brief", reason: "bond flaps" },
          { op: "iface.bridge.update", target: "bridge:pve1:vmbr0", disruption: "none", reason: "no-op" },
        ],
        nodes: ["pve1"],
        carriers: ["bridge:pve1:vmbr0"],
        guests: [{ ref: "guest:pve1:100", name: "web1", node: "pve1", vmid: 100, nic: "net0", carrier: "bridge:pve1:vmbr0" }],
        touchesMgmtPath: true,
        disruption: "brief",
      }),
    );
    expect(req.origin).toBe("changeset-impact");
    expect(req.target).toBe("bond:pve1:bond0");
    expect(req.affected.sort()).toEqual(
      ["bridge:pve1:vmbr0", "guest:pve1:100", "node:pve1:pve1"].sort(),
    );
    expect(req.caveats).toEqual(["touches the management path", "overall disruption: brief"]);
  });

  it("leaves target undefined for an impact with no op targets", () => {
    const req = blastRadiusRequestFromChangesetImpact("cs-2", changesetImpact());
    expect(req.target).toBeUndefined();
    expect(req.caveats).toEqual([]);
  });
});

describe("blastRadiusRequestFromFindingRefs", () => {
  it("has no target — a finding names no single failure point", () => {
    const req = blastRadiusRequestFromFindingRefs(["bond:pve1:bond0", "bridge:pve1:vmbr0"], "LLDP mismatch");
    expect(req.origin).toBe("finding");
    expect(req.target).toBeUndefined();
    expect(req.affected).toEqual(["bond:pve1:bond0", "bridge:pve1:vmbr0"]);
    expect(req.label).toBe("LLDP mismatch");
  });
});

describe("resolveNodeAnchorsInRequest", () => {
  // internal/topology.Project never renders a bare KindNode entity, so
  // "node:<name>:<name>" never appears in a real GET /topology response —
  // every such ref must be resolved to that node's actual rendered anchor
  // before computeBlastRadiusFocus ever sees it (nodeAnchor.ts's T-1402
  // defect, applied here).
  const anchorFor = (name: string): string | undefined => (name === "pve1" ? "bond:pve1:bond0" : undefined);

  it("resolves a whole-node target to its rendered anchor", () => {
    const req = blastRadiusRequestFromFailsimImpact(failsimImpact({ target: "node:pve1:pve1" }));
    const resolved = resolveNodeAnchorsInRequest(req, anchorFor);
    expect(resolved.target).toBe("bond:pve1:bond0");
  });

  it("resolves mgmtPathLoss-derived node refs folded into affected", () => {
    const req = blastRadiusRequestFromFailsimImpact(failsimImpact({ mgmtPathLoss: ["pve1"] }));
    const resolved = resolveNodeAnchorsInRequest(req, anchorFor);
    expect(resolved.affected).toEqual(["bond:pve1:bond0"]);
  });

  it("leaves an unresolvable node ref (no rendered anchor) unchanged, so it still reports off-map rather than throwing", () => {
    const req = blastRadiusRequestFromFailsimImpact(failsimImpact({ target: "node:pve9:pve9" }));
    const resolved = resolveNodeAnchorsInRequest(req, anchorFor);
    expect(resolved.target).toBe("node:pve9:pve9");
  });

  it("leaves every non-node ref untouched", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ target: "bond:pve1:bond0", disconnectedGuests: ["guest:pve1:100"] }),
    );
    const resolved = resolveNodeAnchorsInRequest(req, anchorFor);
    expect(resolved.target).toBe("bond:pve1:bond0");
    expect(resolved.affected).toEqual(["guest:pve1:100"]);
  });

  it("composes with computeBlastRadiusFocus end to end: a whole-node target focuses via its anchor", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ target: "node:pve1:pve1", disconnectedGuests: ["guest:pve1:100"] }),
    );
    const nodeIdForName = (name: string): string | undefined => (name === "pve1" ? "node:pve1:pve1" : undefined);
    // Here the "anchor" is deliberately still the raw node ref pretending to
    // be an already-rendered id (this suite's own NODE_IDS fixture does
    // include it, unlike a real GET /topology response) — the point of this
    // test is only that the resolve step runs before computeBlastRadiusFocus,
    // not that nodeAnchor.ts's real tie-break logic is re-tested here.
    const resolved = resolveNodeAnchorsInRequest(req, nodeIdForName);
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, resolved);
    expect(focus.active).toBe(true);
    expect(focus.roles.get("node:pve1:pve1")).toBe("target");
  });
});

describe("computeBlastRadiusFocus", () => {
  it("focuses the target, the affected entities, and every hop on the path between them", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ target: "node:pve1:pve1", disconnectedGuests: ["guest:pve1:100", "guest:pve1:101"] }),
    );
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);

    expect(focus.active).toBe(true);
    expect(focus.focusNodeIds).toEqual(
      new Set(["node:pve1:pve1", "bond:pve1:bond0", "bridge:pve1:vmbr0", "guest:pve1:100", "guest:pve1:101"]),
    );
    expect(focus.focusEdgeIds).toEqual(new Set(["e1", "e2", "e3", "e4"]));
    expect(focus.roles.get("node:pve1:pve1")).toBe("target");
    expect(focus.roles.get("guest:pve1:100")).toBe("affected");
    expect(focus.roles.get("bond:pve1:bond0")).toBe("path");
    expect(focus.roles.get("bridge:pve1:vmbr0")).toBe("path");
    expect([...focus.onMapRefs].sort()).toEqual(["guest:pve1:100", "guest:pve1:101", "node:pve1:pve1"].sort());
    expect(focus.offMapRefs).toEqual([]);
    expect(focus.unreachableRefs).toEqual([]);
    expect(focus.totalNodeCount).toBe(NODE_IDS.length);
  });

  it("reports an on-map affected entity with no path back to the anchor as unreachable, but still focuses it", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ target: "node:pve1:pve1", disconnectedGuests: ["node:pve2:pve2"] }),
    );
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);

    expect(focus.active).toBe(true);
    expect(focus.unreachableRefs).toEqual(["node:pve2:pve2"]);
    expect(focus.focusNodeIds.has("node:pve2:pve2")).toBe(true);
    // No edge connects it, so no edge id was invented for it.
    expect([...focus.focusEdgeIds]).toEqual([]);
  });

  it("degrades to inactive (never blanks the map) when every named ref is off the current map — a stale result", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ target: "bond:pve9:bond9", disconnectedGuests: ["guest:pve9:999"] }),
    );
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);

    expect(focus.active).toBe(false);
    expect(focus.focusNodeIds.size).toBe(0);
    expect(focus.focusEdgeIds.size).toBe(0);
    expect([...focus.offMapRefs].sort()).toEqual(["bond:pve9:bond9", "guest:pve9:999"].sort());
    expect(summarizeBlastRadiusFocus(focus)).toContain("None of the 2 named entities are on the current map");
  });

  it("partially degrades: an entity since removed is named off-map, the rest of the lens still focuses", () => {
    const req = blastRadiusRequestFromFailsimImpact(
      failsimImpact({ target: "node:pve1:pve1", disconnectedGuests: ["guest:pve1:100", "guest:pve9:gone"] }),
    );
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);

    expect(focus.active).toBe(true);
    expect(focus.offMapRefs).toEqual(["guest:pve9:gone"]);
    expect(focus.focusNodeIds.has("guest:pve1:100")).toBe(true);
    expect(focus.focusNodeIds.has("guest:pve9:gone")).toBe(false);
    expect(summarizeBlastRadiusFocus(focus)).toContain("1 named entity is not on the current map");
  });

  it("picks the first on-map affected ref as the anchor when the request names no target (finding)", () => {
    const req = blastRadiusRequestFromFindingRefs(["guest:pve1:100", "guest:pve1:101"], "LLDP mismatch");
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);

    expect(focus.active).toBe(true);
    expect(focus.roles.get("guest:pve1:100")).toBe("affected");
    expect(focus.roles.get("guest:pve1:101")).toBe("affected");
    // Path between the two guests runs back through the shared bridge.
    expect(focus.focusNodeIds.has("bridge:pve1:vmbr0")).toBe(true);
  });

  it("a single-ref request with nothing else to connect focuses just that node, no path work needed", () => {
    const req = blastRadiusRequestFromFindingRefs(["guest:pve1:100"], "solo finding");
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);

    expect(focus.active).toBe(true);
    expect(focus.focusNodeIds).toEqual(new Set(["guest:pve1:100"]));
    expect(focus.focusEdgeIds.size).toBe(0);
    expect(focus.unreachableRefs).toEqual([]);
  });

  it("accepts a plain readonly array of node ids as well as a Set", () => {
    const req = blastRadiusRequestFromFindingRefs(["guest:pve1:100"], "array form");
    const focus = computeBlastRadiusFocus([...NODE_IDS], EDGES, req);
    expect(focus.active).toBe(true);
  });
});

describe("summarizeBlastRadiusFocus", () => {
  it("states the showing-N-of-M count in real text", () => {
    const req = blastRadiusRequestFromFindingRefs(["guest:pve1:100"], "x");
    const focus = computeBlastRadiusFocus(NODE_IDS, EDGES, req);
    expect(summarizeBlastRadiusFocus(focus)).toBe(`Showing 1 of ${String(NODE_IDS.length)} entities.`);
  });
});
