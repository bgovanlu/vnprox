import { describe, expect, it } from "vitest";
import { addressesField, firstHostAddress } from "./peerSuggest";

describe("firstHostAddress", () => {
  // The shape GET /inventory/{ref} actually returns: topology.Detail builds
  // `fields` with json.Marshal(entity), so inventory.Bridge.Addresses
  // ([]string, no json tag) arrives as an ARRAY under the key `Addresses`.
  // The original implementation only handled a comma-joined string under
  // `addresses`, which meant the VXLAN wizard's peer auto-suggest never
  // suggested anything (T-2108). These two cases are the ones that matter;
  // the string cases below are kept because a fieldMap-derived value is
  // still comma-joined.
  it("takes the first entry of the API's CIDR array", () => {
    expect(firstHostAddress(["10.10.0.11/24", "fd00::1/64"])).toBe("10.10.0.11");
  });

  it("returns undefined for an empty array", () => {
    expect(firstHostAddress([])).toBeUndefined();
  });

  it("strips the CIDR prefix from the first address", () => {
    expect(firstHostAddress("10.10.0.11/24")).toBe("10.10.0.11");
  });

  it("picks the first of several comma-joined addresses", () => {
    expect(firstHostAddress("10.10.0.11/24,fd00::1/64")).toBe("10.10.0.11");
  });

  it("returns undefined for undefined/empty input", () => {
    expect(firstHostAddress(undefined)).toBeUndefined();
    expect(firstHostAddress("")).toBeUndefined();
  });

  it("returns undefined rather than guessing at an unexpected type", () => {
    expect(firstHostAddress(42)).toBeUndefined();
    expect(firstHostAddress({ addresses: "10.0.0.1/24" })).toBeUndefined();
    expect(firstHostAddress([1, 2])).toBeUndefined();
  });

  it("tolerates stray whitespace", () => {
    expect(firstHostAddress(" 10.10.0.12/24 , 10.10.0.13/24")).toBe("10.10.0.12");
    expect(firstHostAddress([" 10.10.0.12/24 "])).toBe("10.10.0.12");
  });
});

describe("addressesField", () => {
  it("reads the Go-marshalled `Addresses` key the API really sends", () => {
    expect(addressesField({ Addresses: ["10.10.0.11/24"], Name: "vmbr0" })).toEqual(["10.10.0.11/24"]);
  });

  it("also accepts a lowercase, comma-joined `addresses` key", () => {
    expect(addressesField({ addresses: "10.10.0.11/24,fd00::1/64" })).toEqual(["10.10.0.11/24", "fd00::1/64"]);
  });

  it("is empty when neither spelling is present", () => {
    expect(addressesField({ Name: "vmbr0" })).toEqual([]);
  });
});
