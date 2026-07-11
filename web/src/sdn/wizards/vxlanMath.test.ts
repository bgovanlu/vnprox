import { describe, expect, it } from "vitest";
import { UNDERLAY_MTU, VXLAN_OVERHEAD, VXLAN_SAFE_MTU, computeVxlanMtuDerivation } from "./vxlanMath";

describe("vxlanMath", () => {
  it("derives the exact T-402 worked example: 1500 underlay -> 1450 safe", () => {
    const d = computeVxlanMtuDerivation(0);
    expect(d.underlayMtu).toBe(1500);
    expect(d.overhead).toBe(50);
    expect(d.safeMtu).toBe(1450);
    expect(VXLAN_SAFE_MTU).toBe(1450);
    expect(UNDERLAY_MTU).toBe(1500);
    expect(VXLAN_OVERHEAD).toBe(50);
  });

  it("mtu left at 0 (blank) never warns", () => {
    expect(computeVxlanMtuDerivation(0).warn).toBe(false);
  });

  it("mtu at exactly the safe value does not warn", () => {
    expect(computeVxlanMtuDerivation(1450).warn).toBe(false);
  });

  it("mtu above the safe value warns (T-402 AC3 scenario: 1500 vnet mtu)", () => {
    const d = computeVxlanMtuDerivation(1500);
    expect(d.warn).toBe(true);
    expect(d.safeMtu).toBe(1450);
  });

  it("mtu below the safe value does not warn", () => {
    expect(computeVxlanMtuDerivation(1400).warn).toBe(false);
  });

  it("honors a non-default underlay MTU", () => {
    const d = computeVxlanMtuDerivation(8900, 9000);
    expect(d.safeMtu).toBe(8950);
    expect(d.warn).toBe(false);
    expect(computeVxlanMtuDerivation(9000, 9000).warn).toBe(true);
  });
});
