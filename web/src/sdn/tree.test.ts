import { describe, expect, it } from "vitest";
import type { SdnTree } from "../api/types";
import { firstSelection, formatDiffValue, resolveSdnSelection } from "./tree";

const tree: SdnTree = {
  generatedAt: 1,
  zones: [
    {
      id: "vlanz",
      type: "vlan",
      nodeStatus: [{ node: "pve1", status: "ok" }],
      vnets: [
        {
          id: "vnet1",
          zone: "vlanz",
          subnets: [{ id: "10.0.0.0/24", vnet: "vnet1", cidr: "10.0.0.0/24" }],
        },
      ],
    },
    { id: "evpnz", type: "evpn", nodeStatus: [], vnets: [] },
  ],
  fabrics: [],
  controllers: [],
  prefixLists: [],
  routeMaps: [],
};

describe("resolveSdnSelection", () => {
  it("returns undefined with no tree or no selection", () => {
    expect(resolveSdnSelection(undefined, { kind: "zone", zoneId: "vlanz" })).toBeUndefined();
    expect(resolveSdnSelection(tree, undefined)).toBeUndefined();
  });

  it("resolves a zone selection", () => {
    const got = resolveSdnSelection(tree, { kind: "zone", zoneId: "vlanz" });
    expect(got?.zone.id).toBe("vlanz");
    expect(got?.vnet).toBeUndefined();
  });

  it("resolves a vnet selection", () => {
    const got = resolveSdnSelection(tree, { kind: "vnet", zoneId: "vlanz", vnetId: "vnet1" });
    expect(got?.vnet?.id).toBe("vnet1");
    expect(got?.subnet).toBeUndefined();
  });

  it("resolves a subnet selection", () => {
    const got = resolveSdnSelection(tree, {
      kind: "subnet",
      zoneId: "vlanz",
      vnetId: "vnet1",
      subnetId: "10.0.0.0/24",
    });
    expect(got?.subnet?.cidr).toBe("10.0.0.0/24");
  });

  it("returns undefined when a selected id no longer exists (stale selection after refetch)", () => {
    expect(resolveSdnSelection(tree, { kind: "zone", zoneId: "gone" })).toBeUndefined();
    expect(resolveSdnSelection(tree, { kind: "vnet", zoneId: "vlanz", vnetId: "gone" })).toBeUndefined();
    expect(
      resolveSdnSelection(tree, { kind: "subnet", zoneId: "vlanz", vnetId: "vnet1", subnetId: "gone" }),
    ).toBeUndefined();
  });
});

describe("formatDiffValue", () => {
  it("renders an em dash for missing values", () => {
    expect(formatDiffValue(undefined)).toBe("—");
    expect(formatDiffValue(null)).toBe("—");
    expect(formatDiffValue([])).toBe("—");
  });
  it("joins arrays", () => {
    expect(formatDiffValue(["pve1", "pve2"])).toBe("pve1, pve2");
  });
  it("renders booleans as yes/no", () => {
    expect(formatDiffValue(true)).toBe("yes");
    expect(formatDiffValue(false)).toBe("no");
  });
  it("passes through scalars", () => {
    expect(formatDiffValue(1500)).toBe("1500");
    expect(formatDiffValue("vmbr0")).toBe("vmbr0");
  });
});

describe("firstSelection", () => {
  it("selects the first zone", () => {
    expect(firstSelection(tree)).toEqual({ kind: "zone", zoneId: "vlanz" });
  });
  it("returns undefined for an empty tree", () => {
    expect(
      firstSelection({ zones: [], fabrics: [], controllers: [], prefixLists: [], routeMaps: [], generatedAt: 1 }),
    ).toBeUndefined();
  });
});
