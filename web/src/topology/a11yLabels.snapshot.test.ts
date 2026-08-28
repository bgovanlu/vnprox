// SPDX-License-Identifier: Apache-2.0

// T-905 acceptance criterion 4: "a snapshot test asserts aria-label content
// for every entity/badge type against three-node-vlan and evpn-lab
// (covering the VTEP mesh/peer badges)". Runs the real projection pipeline
// (toFlowElements, the same glue TopologyPage.tsx feeds into both
// renderers) over both captured fixtures, then buildA11yProxies (a11yBridge
// .ts) over the result — the exact seam T-901 built and this card is
// layering the WCAG pass onto — and snapshots every entity's id + kind +
// ariaLabel, sorted for determinism. evpn-lab-topology.json (captured the
// same way three-node-vlan-topology.json was: the real pvemock ->
// vnproxd -> GET /topology pipeline against testdata/clusters/
// evpn-lab.yaml) exercises the SDN/EVPN entity kinds and the
// mgmt/mgmt-path/drift badges three-node-vlan's own fixture doesn't cover
// in the same combinations (this repo's frontend fixtures don't carry a
// distinct vtep/bgp-peer *topology entity* kind — those badges live on the
// separate EVPN/BGP status view, not GET /topology's node/edge badges; see
// this file's own second describe block for exactly which badges evpn-lab
// contributes).
import threeNodeVlanFixture from "./__fixtures__/three-node-vlan-topology.json";
import evpnLabFixture from "./__fixtures__/evpn-lab-topology.json";
import { describe, expect, it } from "vitest";
import { ALL_LAYERS, type TopologyResponse } from "../api/types";
import { buildA11yProxies } from "./a11yBridge";
import { toFlowElements } from "./toFlowElements";

function proxyLabels(fixture: TopologyResponse): { id: string; kind: string; ariaLabel: string }[] {
  const elements = toFlowElements({
    nodes: fixture.nodes,
    edges: fixture.edges,
    expandedGroups: new Set(),
    activeLayers: new Set(ALL_LAYERS),
    layoutPositions: new Map(),
    manualPositions: {},
  });
  return buildA11yProxies(elements.nodes)
    .map((p) => ({ id: p.id, kind: p.kind, ariaLabel: p.ariaLabel }))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

describe("a11y proxy labels: three-node-vlan", () => {
  const labels = proxyLabels(threeNodeVlanFixture as unknown as TopologyResponse);

  it("every entity gets a non-empty aria-label naming its kind and status", () => {
    expect(labels.length).toBeGreaterThan(0);
    for (const l of labels) {
      expect(l.ariaLabel).toMatch(/status (ok|down|degraded|unknown)/);
    }
  });

  it("matches the committed snapshot (id/kind/ariaLabel per entity)", () => {
    // T-3505: every physnic entry's snapshot gained a trailing ", media
    // type unknown" — neither fixture carries a mediaPort (both predate
    // T-3503), and entityAriaLabel now speaks portPhrases for physnic
    // entities the same way SwitchFaceplate.tsx's NicPort already did
    // before this task (this frontend's Graph view simply hadn't caught up
    // to what the Switch view already said about the identical entity).
    // Not a regression — see web/src/topology/viewParity.test.ts, which
    // pins this same phrase agreeing between both views over real data.
    expect(labels).toMatchSnapshot();
  });

  it("lists an ordinary badge verbatim (this fixture predates T-702's mgmt/mgmt-path badge painting — see the evpn-lab block below for that trio)", () => {
    expect(labels.some((l) => l.ariaLabel.includes("badges: mode=802.3ad"))).toBe(true);
  });
});

describe("a11y proxy labels: evpn-lab (SDN zone/vnet/subnet kinds, drift + mgmt-path badges)", () => {
  const labels = proxyLabels(evpnLabFixture as unknown as TopologyResponse);

  it("every entity gets a non-empty aria-label naming its kind and status", () => {
    expect(labels.length).toBeGreaterThan(0);
    for (const l of labels) {
      expect(l.ariaLabel).toMatch(/status (ok|down|degraded|unknown)/);
    }
  });

  it("matches the committed snapshot (id/kind/ariaLabel per entity)", () => {
    // T-3505: same "media type unknown" addition as the three-node-vlan
    // block above — see its comment.
    expect(labels).toMatchSnapshot();
  });

  it("covers the sdn-zone/sdn-vnet/sdn-subnet entity kinds this fixture adds over three-node-vlan", () => {
    const kinds = new Set(labels.map((l) => l.kind));
    expect(kinds.has("sdn-zone")).toBe(true);
    expect(kinds.has("sdn-vnet")).toBe(true);
    expect(kinds.has("sdn-subnet")).toBe(true);
  });

  // T-3501: this fixture predates the "finding:<source>:<severity>" badge
  // vocabulary — it only carries the legacy bare "drift" wire token, with no
  // source/severity attached. badgeAriaParts no longer fabricates a "has
  // configuration drift" sentence for that case (claiming to know it was
  // specifically *drift* is exactly this task's own defect); it falls back
  // to listing the token plainly, the same way it does for any other
  // unrecognized badge. a11yBridge.test.ts's badgeAriaParts describe block
  // covers the richer "warning drift finding"/"error health finding"
  // phrasing directly against the new token, which this captured JSON
  // fixture has no examples of.
  it("lists the legacy bare drift badge plainly (this fixture predates the finding:<source>:<severity> vocabulary)", () => {
    expect(labels.some((l) => l.ariaLabel.includes("badges: drift"))).toBe(true);
    expect(labels.some((l) => l.ariaLabel.includes("has configuration drift"))).toBe(false);
  });
});
