import { describe, expect, it } from "vitest";
import type { IpamCell } from "../api/types";
import { nextFreeAddress } from "./nextFree";

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
