import { describe, expect, it } from "vitest";
import { firstHostAddress } from "./peerSuggest";

describe("firstHostAddress", () => {
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

  it("tolerates stray whitespace", () => {
    expect(firstHostAddress(" 10.10.0.12/24 , 10.10.0.13/24")).toBe("10.10.0.12");
  });
});
