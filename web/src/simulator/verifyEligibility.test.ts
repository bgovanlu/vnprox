import { describe, expect, it } from "vitest";
import type { SimEndpointSpec } from "../api/types";
import { verifyLiveGate } from "./verifyEligibility";

const guestNicSrc: SimEndpointSpec = { kind: "guest-nic", ref: "guest-nic:pve1:300/net0" };

describe("verifyLiveGate", () => {
  it("disables for an undefined src with plain-English copy", () => {
    const gate = verifyLiveGate(undefined, undefined, false, false);
    expect(gate.enabled).toBe(false);
    expect(gate.reason).toMatch(/QEMU guest source/);
  });

  it.each([{ kind: "external" as const }, { kind: "ip" as const, ip: "10.0.0.5" }])(
    "disables for a non-guest-nic src (%o)",
    (src) => {
      const gate = verifyLiveGate(src, undefined, false, false);
      expect(gate.enabled).toBe(false);
      expect(gate.reason).toMatch(/pick a guest NIC/);
    },
  );

  it("disables with a 'checking' message while the eligibility query is loading", () => {
    const gate = verifyLiveGate(guestNicSrc, undefined, true, false);
    expect(gate.enabled).toBe(false);
    expect(gate.reason).toMatch(/Checking/);
  });

  it("disables with a not-qemu reason for a resolved non-qemu guest", () => {
    const gate = verifyLiveGate(guestNicSrc, { eligible: false, reason: "not-qemu" }, false, false);
    expect(gate.enabled).toBe(false);
    expect(gate.reason).toMatch(/isn't a QEMU guest/);
  });

  it("disables with an agent-unreachable reason for a qemu guest with no detected guest agent", () => {
    const gate = verifyLiveGate(guestNicSrc, { eligible: false, reason: "agent-unreachable" }, false, false);
    expect(gate.enabled).toBe(false);
    expect(gate.reason).toMatch(/no guest agent was detected/);
  });

  it("disables on a failed eligibility check", () => {
    const gate = verifyLiveGate(guestNicSrc, undefined, false, true);
    expect(gate.enabled).toBe(false);
    expect(gate.reason).toBeTruthy();
  });

  it("enables for an eligible qemu guest-nic src with no grey-out reason", () => {
    const gate = verifyLiveGate(guestNicSrc, { eligible: true }, false, false);
    expect(gate.enabled).toBe(true);
    expect(gate.reason).toBeUndefined();
  });
});
