import { describe, expect, it } from "vitest";
import type { SdnTree } from "../api/types";
import { zoneOptions } from "./dhcpView";

const tree: SdnTree = {
  generatedAt: 1_752_000_000,
  zones: [
    { id: "labz", type: "vlan", nodeStatus: [], vnets: [] },
    { id: "otherz", type: "simple", nodeStatus: [], vnets: [] },
  ],
  fabrics: [],
  prefixLists: [],
  routeMaps: [],
};

describe("zoneOptions", () => {
  it("lists every zone id", () => {
    expect(zoneOptions(tree)).toEqual(["labz", "otherz"]);
  });

  it("returns empty for an undefined tree (not-yet-loaded state)", () => {
    expect(zoneOptions(undefined)).toEqual([]);
  });
});
