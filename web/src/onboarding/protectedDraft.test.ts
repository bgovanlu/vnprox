// SPDX-License-Identifier: Apache-2.0

// Covers T-605 AC1/AC2's "correctly detects and pre-fills management +
// corosync interfaces from the fixture" against a suggest response shaped
// exactly like testdata/clusters/three-node-vlan.yaml's real detection
// output (see this task's report for why three-node-vlan, not
// messy-brownfield, is the fixture exercised end-to-end).
import { describe, expect, it } from "vitest";
import type { ProtectedInterfacesResponse, ProtectedInterfacesSuggestResponse } from "../api/types";
import { draftFromSuggestion, draftToRequestNodes, isRefSelected, selectedCount, toggleRef } from "./protectedDraft";

const THREE_NODE_VLAN_SUGGESTION: ProtectedInterfacesSuggestResponse = {
  nodes: {
    pve1: ["bridge:pve1:vmbr0"],
    pve2: ["bridge:pve2:vmbr0"],
    pve3: ["bridge:pve3:vmbr0"],
  },
};

describe("draftFromSuggestion", () => {
  it("pre-fills every node's suggested ref when nothing was previously confirmed", () => {
    const draft = draftFromSuggestion(THREE_NODE_VLAN_SUGGESTION, undefined);
    expect(draft).toEqual({
      pve1: ["bridge:pve1:vmbr0"],
      pve2: ["bridge:pve2:vmbr0"],
      pve3: ["bridge:pve3:vmbr0"],
    });
    expect(isRefSelected(draft, "pve1", "bridge:pve1:vmbr0")).toBe(true);
    expect(isRefSelected(draft, "pve2", "bridge:pve1:vmbr0")).toBe(false);
    expect(selectedCount(draft)).toBe(3);
  });

  it("unions with a previously-confirmed set instead of overwriting it", () => {
    const existing: ProtectedInterfacesResponse = {
      nodes: { pve1: ["bond:pve1:bond0"] },
      updatedAt: 111,
      version: 1,
    };
    const draft = draftFromSuggestion(THREE_NODE_VLAN_SUGGESTION, existing);
    expect(draft.pve1).toEqual(["bond:pve1:bond0", "bridge:pve1:vmbr0"]);
    expect(draft.pve2).toEqual(["bridge:pve2:vmbr0"]);
  });

  it("handles both inputs being undefined (queries still in flight)", () => {
    expect(draftFromSuggestion(undefined, undefined)).toEqual({});
  });
});

describe("toggleRef", () => {
  it("unchecking a suggested ref removes it without touching other nodes (the 'correct' half of confirm-or-correct)", () => {
    const draft = draftFromSuggestion(THREE_NODE_VLAN_SUGGESTION, undefined);
    const next = toggleRef(draft, "pve2", "bridge:pve2:vmbr0");
    expect(next.pve2).toEqual([]);
    expect(next.pve1).toEqual(["bridge:pve1:vmbr0"]);
    expect(draft.pve2).toEqual(["bridge:pve2:vmbr0"]); // original untouched
  });

  it("toggling an unselected ref back on re-adds it", () => {
    const draft = draftFromSuggestion(THREE_NODE_VLAN_SUGGESTION, undefined);
    const unchecked = toggleRef(draft, "pve1", "bridge:pve1:vmbr0");
    const rechecked = toggleRef(unchecked, "pve1", "bridge:pve1:vmbr0");
    expect(rechecked.pve1).toEqual(["bridge:pve1:vmbr0"]);
  });
});

describe("draftToRequestNodes", () => {
  it("drops nodes whose ref list was fully unchecked rather than sending an empty array", () => {
    const draft = draftFromSuggestion(THREE_NODE_VLAN_SUGGESTION, undefined);
    const cleared = toggleRef(draft, "pve3", "bridge:pve3:vmbr0");
    expect(draftToRequestNodes(cleared)).toEqual({
      pve1: ["bridge:pve1:vmbr0"],
      pve2: ["bridge:pve2:vmbr0"],
    });
  });
});
