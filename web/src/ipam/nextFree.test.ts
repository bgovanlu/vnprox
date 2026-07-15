import { describe, expect, it } from "vitest";
import { firstUsableIPv4 } from "./nextFree";

describe("firstUsableIPv4 (T-701 acceptance criterion 1)", () => {
  it("returns network + 1 for a /24", () => {
    expect(firstUsableIPv4("10.50.0.0/24")).toBe("10.50.0.1");
  });

  it("returns network + 1 for a /30", () => {
    expect(firstUsableIPv4("192.168.50.0/30")).toBe("192.168.50.1");
  });

  it("masks a non-network-aligned address down first", () => {
    expect(firstUsableIPv4("10.50.0.130/24")).toBe("10.50.0.1");
  });

  it("returns the network address itself for a /31 (no network/broadcast pair)", () => {
    expect(firstUsableIPv4("10.0.0.0/31")).toBe("10.0.0.0");
  });

  it("returns the network address itself for a /32", () => {
    expect(firstUsableIPv4("10.0.0.5/32")).toBe("10.0.0.5");
  });

  it("returns undefined for a malformed or non-IPv4 CIDR", () => {
    expect(firstUsableIPv4("")).toBeUndefined();
    expect(firstUsableIPv4("not-a-cidr")).toBeUndefined();
    expect(firstUsableIPv4("10.50.0.0")).toBeUndefined();
    expect(firstUsableIPv4("2001:db8::/32")).toBeUndefined();
  });
});
