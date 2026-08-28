// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { readNumberList, readString, readStringList } from "./entityFields";

// The two shapes that matter are the ones GET /inventory/{ref} actually
// sends — Go field names holding Go types. Both wizards that read this map
// guessed a different shape and were silently broken in production while
// their own tests passed (T-2108); see entityFields.ts.
const BRIDGE = { Name: "vmbr0", Addresses: ["10.10.0.11/24", "fd00::1/64"], MTU: 1500 };
const LLDP_NEIGHBOR = { ChassisName: "sw-core-01", PortID: "Te1/0/1", TaggedVLANs: [100, 200, 300] };

describe("readString", () => {
  it("reads a Go-named string field", () => {
    expect(readString(LLDP_NEIGHBOR, "ChassisName")).toBe("sw-core-01");
    expect(readString(LLDP_NEIGHBOR, "PortID")).toBe("Te1/0/1");
  });

  it("falls back to a lower-camel spelling of the same name", () => {
    expect(readString({ chassisName: "sw-core-02" }, "ChassisName")).toBe("sw-core-02");
  });

  it("is undefined for an absent field or a non-string value", () => {
    expect(readString(BRIDGE, "Comments")).toBeUndefined();
    expect(readString(BRIDGE, "MTU")).toBeUndefined();
    expect(readString(BRIDGE, "Addresses")).toBeUndefined();
  });
});

describe("readStringList", () => {
  it("reads the JSON array the API sends", () => {
    expect(readStringList(BRIDGE, "Addresses")).toEqual(["10.10.0.11/24", "fd00::1/64"]);
  });

  it("also accepts a comma-joined string", () => {
    expect(readStringList({ Addresses: "10.10.0.11/24, fd00::1/64" }, "Addresses")).toEqual(["10.10.0.11/24", "fd00::1/64"]);
  });

  it("is empty — not undefined — for an absent field", () => {
    expect(readStringList(BRIDGE, "DeclaredPortNames")).toEqual([]);
  });

  it("drops non-string entries rather than coercing them", () => {
    expect(readStringList({ Addresses: ["10.0.0.1/24", 42, null] }, "Addresses")).toEqual(["10.0.0.1/24"]);
  });
});

describe("readNumberList", () => {
  it("reads the JSON number array the API sends", () => {
    expect(readNumberList(LLDP_NEIGHBOR, "TaggedVLANs")).toEqual([100, 200, 300]);
  });

  it("also accepts a comma-joined string", () => {
    expect(readNumberList({ TaggedVLANs: "100, 200,300" }, "TaggedVLANs")).toEqual([100, 200, 300]);
  });

  it("drops entries that are not finite numbers", () => {
    expect(readNumberList({ TaggedVLANs: "100, ,abc,200" }, "TaggedVLANs")).toEqual([100, 200]);
    expect(readNumberList({ TaggedVLANs: [100, "abc", null, 200] }, "TaggedVLANs")).toEqual([100, 200]);
  });

  it("is empty for an absent field", () => {
    expect(readNumberList(BRIDGE, "TaggedVLANs")).toEqual([]);
  });
});
