import { describe, expect, it } from "vitest";
import {
  cidrError,
  gatewayError,
  ipError,
  sdnNameError,
  sdnNameWarning,
  subnetStepValid,
  vniError,
} from "./validation";

describe("sdnNameError", () => {
  it("accepts letters and digits starting with a letter", () => {
    expect(sdnNameError("homelab")).toBeUndefined();
    expect(sdnNameError("vnet1")).toBeUndefined();
    expect(sdnNameError("Overlay1")).toBeUndefined();
  });
  it("leaves emptiness to the step's required check", () => {
    expect(sdnNameError("")).toBeUndefined();
    expect(sdnNameError("   ")).toBeUndefined();
  });
  it("rejects the characters PVE rejects", () => {
    expect(sdnNameError("dc-evpn")).toBeDefined(); // hyphen
    expect(sdnNameError("bad_vnet")).toBeDefined(); // underscore
    expect(sdnNameError("1zone")).toBeDefined(); // leading digit
    expect(sdnNameError("has space")).toBeDefined();
    expect(sdnNameError("dotted.name")).toBeDefined();
  });
});

describe("sdnNameWarning", () => {
  it("warns only when longer than 8 characters", () => {
    expect(sdnNameWarning("homelab")).toBeUndefined();
    expect(sdnNameWarning("eightchr")).toBeUndefined(); // exactly 8
    expect(sdnNameWarning("ninechars")).toBeDefined();
  });
});

describe("vniError", () => {
  it("requires a VNI in range", () => {
    expect(vniError(0)).toBeDefined();
    expect(vniError(1)).toBeUndefined();
    expect(vniError(4094)).toBeUndefined();
    expect(vniError(4095)).toBeDefined();
    expect(vniError(1.5)).toBeDefined();
  });
});

describe("ipError", () => {
  it("accepts valid IPv4/IPv6 and rejects junk", () => {
    expect(ipError("10.0.0.1")).toBeUndefined();
    expect(ipError("fe80::1")).toBeUndefined();
    expect(ipError("")).toBeDefined();
    expect(ipError("10.0.0.999")).toBeDefined();
    expect(ipError("not-an-ip")).toBeDefined();
  });
});

describe("cidrError", () => {
  it("accepts a valid IPv4 CIDR and empty; rejects malformed", () => {
    expect(cidrError("10.10.0.0/24")).toBeUndefined();
    expect(cidrError("")).toBeUndefined();
    expect(cidrError("10.10.0.0")).toBeDefined(); // no prefix
    expect(cidrError("10.10.0.0/40")).toBeDefined(); // prefix too big
    expect(cidrError("10.10.0.999/24")).toBeDefined();
  });
});

describe("gatewayError", () => {
  it("flags a gateway outside the CIDR", () => {
    expect(gatewayError("10.10.0.1", "10.10.0.0/24")).toBeUndefined();
    expect(gatewayError("", "10.10.0.0/24")).toBeUndefined();
    expect(gatewayError("10.9.9.1", "10.10.0.0/24")).toBeDefined();
    expect(gatewayError("nonsense", "10.10.0.0/24")).toBeDefined();
  });
});

describe("subnetStepValid", () => {
  it("is valid when empty, or CIDR + in-range gateway", () => {
    expect(subnetStepValid({ cidr: "", gateway: "", isolated: false })).toBe(true);
    expect(subnetStepValid({ cidr: "10.10.0.0/24", gateway: "10.10.0.1", isolated: false })).toBe(true);
    expect(subnetStepValid({ cidr: "10.10.0.0/24", gateway: "", isolated: true })).toBe(true);
  });
  it("is invalid on a bad CIDR or out-of-range gateway", () => {
    expect(subnetStepValid({ cidr: "bogus", gateway: "", isolated: false })).toBe(false);
    expect(subnetStepValid({ cidr: "10.10.0.0/24", gateway: "10.9.9.1", isolated: false })).toBe(false);
  });
});
