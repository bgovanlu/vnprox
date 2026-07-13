import { describe, expect, it } from "vitest";
import type { IpamCell } from "../api/types";
import { firstUsableIPv4, nextFreeAddress } from "./nextFree";

function cell(ip: string, state: IpamCell["state"]): IpamCell {
  return { ip, state };
}

describe("nextFreeAddress", () => {
  it("returns undefined for an empty or undefined cell list", () => {
    expect(nextFreeAddress(undefined)).toBeUndefined();
    expect(nextFreeAddress([])).toBeUndefined();
  });

  it("skips allocated/observed/reserved/gateway/conflict cells (acceptance criterion 4)", () => {
    const cells: IpamCell[] = [
      cell("10.0.0.1", "gateway"),
      cell("10.0.0.2", "allocated"),
      cell("10.0.0.3", "reserved"),
      cell("10.0.0.4", "observed"),
      cell("10.0.0.5", "conflict"),
      cell("10.0.0.6", "free"),
      cell("10.0.0.7", "free"),
    ];
    expect(nextFreeAddress(cells)).toBe("10.0.0.6");
  });

  it("returns undefined when nothing is free", () => {
    const cells: IpamCell[] = [cell("10.0.0.1", "gateway"), cell("10.0.0.2", "allocated")];
    expect(nextFreeAddress(cells)).toBeUndefined();
  });

  it("returns the lowest-addressed free cell (ascending input order)", () => {
    const cells: IpamCell[] = [cell("10.0.0.1", "allocated"), cell("10.0.0.2", "free"), cell("10.0.0.3", "free")];
    expect(nextFreeAddress(cells)).toBe("10.0.0.2");
  });
});

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
