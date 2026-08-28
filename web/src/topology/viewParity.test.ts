// SPDX-License-Identifier: Apache-2.0

// T-3505 AC1: "The same entity reports the same status, source and severity
// in both views — pinned by a test over one fixture, not by inspection."
//
// Builds BOTH the Switch view's model (buildSwitchModel, switchModel.ts) and
// the Graph view's model (toFlowElements + a11yBridge's entityAriaLabel)
// from the exact same pvecube-reference-topology.json capture — a verbatim
// GET /topology response, re-captured against real hardware by T-3504
// (see that commit's message: physnic enp1s0/enp3s0 up with real
// mediaPort=tp/speedMbps, enp2s0/enp4s0 down keeping their media but losing
// their speed, `lo` genuinely carrying neither) — and cross-checks every
// entity both views represent, the same "one real fixture, not two
// hand-written ones that can silently drift apart" discipline
// a11yLabels.snapshot.test.ts already follows for the a11y bridge alone.
//
// Every physnic this capture reports a media type for is copper (`tp`) —
// the only kind of hardware the one available node has (T-3503's own "left
// for hardware" note: the fibre/DA branch has never been produced by real
// data). `lo` is the fixture's one genuine "unknown media" NIC (ETHTOOL_GSET
// has nothing to say about a loopback device), which this test asserts is
// itself a legitimate, agreeing parity result — both views must say "we
// don't know" identically, not just when there's something to report. But
// real-tp-only data can never catch the two views disagreeing about
// *fibre/SFP*, which is exactly the branch T-3505's plumbing (EntityNodeData
// carrying mediaPort/speedMbps through to the Graph view for the first
// time) could get wrong silently. A synthetic physnic with mediaPort=fibre,
// wired to vmbr0 as a second bare-NIC uplink (alongside the real enp1s0),
// closes that gap.
import pvecubeFixture from "./__fixtures__/pvecube-reference-topology.json";
import { describe, expect, it } from "vitest";
import { ALL_LAYERS, type TopologyEdge, type TopologyNode, type TopologyResponse } from "../api/types";
import { entityAriaLabel } from "./a11yBridge";
import { parsedFindingBadges, worstFindingSeverity } from "./findingBadges";
import { bodyForNic, portPhrases } from "./portMedia";
import { buildSwitchModel, type SwitchAccessPort, type SwitchPortNic, type SwitchUplink } from "./switchModel";
import { toFlowElements } from "./toFlowElements";

const fixture = pvecubeFixture as unknown as TopologyResponse;

const SYNTHETIC_NIC: TopologyNode = {
  id: "physnic:pvecube:enp9s0",
  kind: "physnic",
  label: "enp9s0",
  layer: "phys",
  nodeGroup: "pvecube",
  status: "ok",
  badges: [],
  mediaPort: "fibre",
  speedMbps: 10000,
};
const SYNTHETIC_EDGE: TopologyEdge = {
  from: SYNTHETIC_NIC.id,
  to: "bridge:pvecube:vmbr0",
  kind: "port-of",
  status: "ok",
  badges: [],
};

const nodes: TopologyNode[] = [...fixture.nodes, SYNTHETIC_NIC];
const edges: TopologyEdge[] = [...fixture.edges, SYNTHETIC_EDGE];

/** Test helper: assert-and-narrow, matching switchModel.test.ts's own —
 * `noUncheckedIndexedAccess`/`Map.get` make every lookup `T | undefined`;
 * this throws with a clear message rather than a forbidden non-null
 * assertion or optional chaining that would silently no-op the assertion
 * below it. */
function must<T>(v: T | undefined, what: string): T {
  if (v === undefined) throw new Error(`expected ${what} to be defined`);
  return v;
}

/** One entity's Switch-view-side facts, flattened out of a SwitchModel/
 * NodeSwitchGroup tree into a flat ref -> facts map so it can be compared
 * against the Graph view's own flat FlowNode list entry-by-entity. */
interface SwitchEntity {
  status: string;
  badges: string[];
  mediaPort?: string;
  speedMbps?: number;
}

function collectSwitchEntities(topology: ReturnType<typeof buildSwitchModel>): Map<string, SwitchEntity> {
  const out = new Map<string, SwitchEntity>();
  const addNic = (nic: SwitchPortNic): void => {
    if (nic.isGroup) return; // a T-1907 collapsed pill, not a real graph entity
    out.set(nic.ref, { status: nic.status, badges: nic.badges, mediaPort: nic.mediaPort, speedMbps: nic.speedMbps });
  };
  const addUplink = (u: SwitchUplink): void => {
    if (!u.isGroup) out.set(u.ref, { status: u.status, badges: u.badges });
    for (const m of u.members) addNic(m);
  };
  const addPort = (p: SwitchAccessPort): void => {
    if (p.isGroup) return; // the collapsed "+N guests" pill, not a real entity
    out.set(p.ref, { status: p.status, badges: p.badges });
  };
  for (const group of topology.nodes) {
    for (const sw of group.switches) {
      out.set(sw.ref, { status: sw.status, badges: sw.badges });
      for (const u of sw.uplinks) addUplink(u);
      for (const p of sw.accessPorts) addPort(p);
    }
    for (const u of group.freePorts) addUplink(u);
  }
  return out;
}

describe("Graph/Switch view parity (T-3505 AC1): pvecube reference topology + one synthetic known-media NIC", () => {
  const switchEntities = collectSwitchEntities(buildSwitchModel(nodes, edges));

  const flowElements = toFlowElements({
    nodes,
    edges,
    expandedGroups: new Set(),
    activeLayers: new Set(ALL_LAYERS),
    layoutPositions: new Map(),
    manualPositions: {},
  });
  const graphByRef = new Map(flowElements.nodes.map((n) => [n.id, n]));

  it("sanity: the fixture really exercises the cases this test relies on — real copper media/speed on enp1s0, no reading at all on lo, and the synthetic fibre NIC", () => {
    expect(switchEntities.get("physnic:pvecube:enp1s0")?.mediaPort).toBe("tp");
    expect(switchEntities.get("physnic:pvecube:enp1s0")?.speedMbps).toBe(100);
    expect(switchEntities.get("physnic:pvecube:lo")?.mediaPort).toBeUndefined();
    expect(switchEntities.get(SYNTHETIC_NIC.id)?.mediaPort).toBe("fibre");
  });

  it("every entity the Switch view represents also exists in the Graph view, with the identical status", () => {
    expect(switchEntities.size).toBeGreaterThan(10); // 4 bridges + several NICs/ports, not a near-empty comparison
    for (const [ref, sw] of switchEntities) {
      const graphNode = graphByRef.get(ref);
      expect(graphNode, `graph view has no node for ${ref}`).toBeDefined();
      expect(graphNode?.data.status, `status mismatch for ${ref}`).toBe(sw.status);
    }
  });

  it("every entity's open-finding source/severity set agrees between the two views (T-3501 parity, over the same fixture T-3503 proved its own AC1 against)", () => {
    let sawAFinding = false;
    for (const [ref, sw] of switchEntities) {
      const graphNode = graphByRef.get(ref);
      if (!graphNode) continue;
      const swParsed = parsedFindingBadges(sw.badges)
        .map((p) => `${p.source}:${p.severity}`)
        .sort();
      const graphParsed = parsedFindingBadges(graphNode.data.badges)
        .map((p) => `${p.source}:${p.severity}`)
        .sort();
      if (swParsed.length > 0) sawAFinding = true;
      expect(graphParsed, `finding source/severity mismatch for ${ref}`).toEqual(swParsed);
      expect(worstFindingSeverity(graphNode.data.badges), `worst severity mismatch for ${ref}`).toBe(
        worstFindingSeverity(sw.badges),
      );
    }
    // Proves the loop above isn't vacuously true: the reference topology's
    // enp2s0/enp4s0 carrier-error findings must actually have been visited.
    expect(sawAFinding).toBe(true);
  });

  it("the down carrier-error NICs (enp2s0/enp4s0) report 'error', not a bare drift, in both views", () => {
    for (const ref of ["physnic:pvecube:enp2s0", "physnic:pvecube:enp4s0"]) {
      const sw = switchEntities.get(ref);
      const graphNode = graphByRef.get(ref);
      expect(sw, ref).toBeDefined();
      expect(graphNode, ref).toBeDefined();
      expect(worstFindingSeverity(sw?.badges ?? [])).toBe("error");
      expect(worstFindingSeverity(graphNode?.data.badges ?? [])).toBe("error");
    }
  });

  it("lo genuinely reports no media type — the honest 'unknown media' case agrees identically in both views", () => {
    expect(switchEntities.get("physnic:pvecube:lo")?.mediaPort).toBeUndefined();
    const graphNode = graphByRef.get("physnic:pvecube:lo");
    expect(graphNode?.data.mediaPort).toBeUndefined();
    expect(entityAriaLabel(must(graphNode, "physnic:pvecube:lo").data)).toContain("media type unknown");
  });

  it("real captured copper NICs (enp1s0 up, enp2s0/enp4s0 down) plumb the same mediaPort/speedMbps into both views: a down port keeps its media but loses its speed, in both, identically (T-3503's own distinction, now proved on the Graph view too)", () => {
    const cases: readonly [string, number | undefined][] = [
      ["physnic:pvecube:enp1s0", 100],
      ["physnic:pvecube:enp3s0", 1000],
      ["physnic:pvecube:enp2s0", undefined],
      ["physnic:pvecube:enp4s0", undefined],
    ];
    for (const [ref, speedMbps] of cases) {
      const sw = must(switchEntities.get(ref), ref);
      const graphNode = must(graphByRef.get(ref), ref);
      expect(sw.mediaPort, ref).toBe("tp");
      expect(graphNode.data.mediaPort, ref).toBe("tp");
      expect(sw.speedMbps, ref).toBe(speedMbps);
      expect(graphNode.data.speedMbps, ref).toBe(speedMbps);
      const switchPhrases = portPhrases(bodyForNic(sw.mediaPort), sw.speedMbps);
      const graphLabel = entityAriaLabel(graphNode.data);
      for (const phrase of switchPhrases) expect(graphLabel, ref).toContain(phrase);
    }
  });

  it("a KNOWN media type agrees between the two views: the same mediaPort/speedMbps reaches both, and both speak the identical phrases — the case the captured fixture alone could not catch", () => {
    const sw = switchEntities.get(SYNTHETIC_NIC.id);
    const graphNode = graphByRef.get(SYNTHETIC_NIC.id);
    expect(sw?.mediaPort).toBe("fibre");
    expect(sw?.speedMbps).toBe(10000);
    // The plumbing check: toFlowElements actually copied the fields through
    // (this is precisely the bug class a projection change like T-3505's
    // could introduce silently — the type system doesn't catch a dropped
    // field, only a test over real data does).
    expect(graphNode?.data.mediaPort).toBe(sw?.mediaPort);
    expect(graphNode?.data.speedMbps).toBe(sw?.speedMbps);

    // SwitchFaceplate.tsx's NicPort speaks portPhrases(bodyForNic(nic
    // .mediaPort), nic.speedMbps) for this exact NIC were it rendered there
    // — reproduced here from the Switch-side facts alone (no import of the
    // React component), then asserted against the Graph view's own
    // accessible label (a11yBridge.ts's entityAriaLabel, which reuses the
    // very same portPhrases call per this task's deliverable).
    const switchPhrases = portPhrases(bodyForNic(sw?.mediaPort), sw?.speedMbps);
    expect(switchPhrases).toEqual(["fibre or direct-attach SFP", "link speed 10G"]);
    const graphLabel = entityAriaLabel(must(graphNode, SYNTHETIC_NIC.id).data);
    for (const phrase of switchPhrases) expect(graphLabel).toContain(phrase);
  });
});
